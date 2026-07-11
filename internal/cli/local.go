// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/spf13/cobra"

	"github.com/ArdurAI/sith/internal/localops"
)

const maxLocalEditBytes = 10 << 20

type localTargetOptions struct {
	context   string
	namespace string
}

func newLocalCommands(root *rootOptions, client localops.Client) []*cobra.Command {
	return []*cobra.Command{
		newYAMLCommand(client),
		newDescribeCommand(root, client),
		newLogsCommand(client),
		newExecCommand(client),
		newPortForwardCommand(client),
		newEditCommand(client),
	}
}

func newYAMLCommand(client localops.Client) *cobra.Command {
	options := &localTargetOptions{}
	revealSecrets := false
	command := &cobra.Command{
		Use:   "yaml <kind>/<name>",
		Short: "Print one object's raw YAML from an explicit context",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			target, err := explicitResourceTarget(args[0], options)
			if err != nil {
				return err
			}
			view, err := client.View(command.Context(), target, revealSecrets)
			if err != nil {
				return err
			}
			if _, err := command.OutOrStdout().Write(view.YAML); err != nil {
				return fmt.Errorf("write object YAML: %w", err)
			}
			return nil
		},
	}
	addLocalTargetFlags(command, options)
	command.Flags().BoolVar(&revealSecrets, "show-secrets", false, "show Secret data instead of redacting it")
	return command
}

func newDescribeCommand(root *rootOptions, client localops.Client) *cobra.Command {
	options := &localTargetOptions{}
	command := &cobra.Command{
		Use:   "describe <kind>/<name>",
		Short: "Show one object and its related Kubernetes events",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			target, err := explicitResourceTarget(args[0], options)
			if err != nil {
				return err
			}
			description, err := client.Describe(command.Context(), target)
			if err != nil {
				return err
			}
			switch root.output {
			case "json":
				if err := json.NewEncoder(command.OutOrStdout()).Encode(description); err != nil {
					return fmt.Errorf("write description JSON: %w", err)
				}
				return nil
			case "yaml":
				return writeYAML(command.OutOrStdout(), description, "description")
			default:
				return writeDescription(command.OutOrStdout(), description)
			}
		},
	}
	addLocalTargetFlags(command, options)
	return command
}

func newLogsCommand(client localops.Client) *cobra.Command {
	targetOptions := &localTargetOptions{}
	options := localops.LogOptions{}
	var tail int64
	var since time.Duration
	command := &cobra.Command{
		Use:   "logs <pod>",
		Short: "Stream logs from one pod in an explicit context",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			target, err := explicitPodTarget(args[0], targetOptions)
			if err != nil {
				return err
			}
			if command.Flags().Changed("tail") {
				options.TailLines = &tail
			}
			options.Since = since
			stream, err := client.Logs(command.Context(), target, options)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(command.OutOrStdout(), stream)
			closeErr := stream.Close()
			if copyErr != nil {
				copyErr = fmt.Errorf("copy pod logs: %w", copyErr)
			}
			if closeErr != nil {
				closeErr = fmt.Errorf("close pod logs: %w", closeErr)
			}
			return errors.Join(copyErr, closeErr)
		},
	}
	addLocalTargetFlags(command, targetOptions)
	command.Flags().StringVarP(&options.Container, "container", "c", "", "container name (required when ambiguous)")
	command.Flags().BoolVarP(&options.Follow, "follow", "f", false, "follow the log stream")
	command.Flags().BoolVar(&options.Previous, "previous", false, "show the previous terminated container instance")
	command.Flags().BoolVar(&options.Timestamps, "timestamps", false, "include timestamps")
	command.Flags().Int64Var(&tail, "tail", -1, "number of recent lines; -1 means all")
	command.Flags().DurationVar(&since, "since", 0, "only logs newer than this duration, such as 5m")
	return command
}

func newExecCommand(client localops.Client) *cobra.Command {
	targetOptions := &localTargetOptions{}
	options := localops.ExecOptions{}
	command := &cobra.Command{
		Use:   "exec <pod> -- <command> [args...]",
		Short: "Execute a command in one pod using the selected kubeconfig identity",
		Args: func(command *cobra.Command, args []string) error {
			if command.ArgsLenAtDash() != 1 || len(args) < 2 {
				return fmt.Errorf("exec requires exactly one pod followed by -- and a command")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			target, err := explicitPodTarget(args[0], targetOptions)
			if err != nil {
				return err
			}
			options.Command = append([]string(nil), args[1:]...)
			streams := localops.Streams{
				Stdin: command.InOrStdin(), Stdout: command.OutOrStdout(), Stderr: command.ErrOrStderr(),
			}
			if options.TTY {
				terminal, err := prepareTerminal(command.InOrStdin(), command.OutOrStdout())
				if err != nil {
					return err
				}
				defer terminal.Close()
				streams.Sizes = terminal
			}
			return client.Exec(command.Context(), target, options, streams)
		},
	}
	addLocalTargetFlags(command, targetOptions)
	command.Flags().StringVarP(&options.Container, "container", "c", "", "container name (required when ambiguous)")
	command.Flags().BoolVarP(&options.Stdin, "stdin", "i", false, "pass stdin to the container")
	command.Flags().BoolVarP(&options.TTY, "tty", "t", false, "allocate a TTY")
	return command
}

func newPortForwardCommand(client localops.Client) *cobra.Command {
	options := &localTargetOptions{}
	var addresses []string
	command := &cobra.Command{
		Use:   "port-forward <pod|service>/<name> <local:remote> [ports...]",
		Short: "Forward loopback TCP ports to one pod in an explicit context",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			target, err := explicitForwardTarget(args[0], options)
			if err != nil {
				return err
			}
			session, err := client.PortForward(command.Context(), localops.ForwardRequest{
				Target: target, Addresses: addresses, Ports: append([]string(nil), args[1:]...),
				Out: command.OutOrStdout(), ErrOut: command.ErrOrStderr(),
			})
			if err != nil {
				return err
			}
			var waitErr error
			select {
			case <-session.Ready():
				ports, err := session.Ports()
				if err != nil {
					waitErr = fmt.Errorf("read forwarded ports: %w", err)
					break
				}
				for _, port := range ports {
					if _, err := fmt.Fprintf(command.OutOrStdout(), "port-forward ready %d -> %d\n", port.Local, port.Remote); err != nil {
						waitErr = fmt.Errorf("write port-forward status: %w", err)
						break
					}
				}
			case err := <-session.Done():
				waitErr = portForwardResult(err)
			case <-command.Context().Done():
				waitErr = command.Context().Err()
			}
			if waitErr == nil {
				select {
				case err := <-session.Done():
					waitErr = portForwardResult(err)
				case <-command.Context().Done():
					waitErr = command.Context().Err()
				}
			}
			return errors.Join(waitErr, session.Close())
		},
	}
	addLocalTargetFlags(command, options)
	command.Flags().StringSliceVar(&addresses, "address", nil, "loopback address: localhost, 127.0.0.1, or ::1")
	return command
}

func newEditCommand(client localops.Client) *cobra.Command {
	options := &localTargetOptions{}
	var filename string
	var yes bool
	command := &cobra.Command{
		Use:   "edit <kind>/<name>",
		Short: "Preview and apply one local Kubernetes YAML edit",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			target, err := explicitResourceTarget(args[0], options)
			if err != nil {
				return err
			}
			manifest, err := editedManifest(command, client, target, filename)
			if err != nil {
				return err
			}
			preview, err := client.PreviewApply(command.Context(), target, manifest)
			if err != nil {
				return err
			}
			diff, err := unifiedYAMLDiff(preview.CurrentYAML, preview.DryRunYAML, target)
			if err != nil {
				return err
			}
			if diff == "" {
				_, err := fmt.Fprintln(command.OutOrStdout(), "no changes")
				return err
			}
			if _, err := io.WriteString(command.OutOrStdout(), diff); err != nil {
				return fmt.Errorf("write edit diff: %w", err)
			}
			if !yes {
				confirmed, err := confirmApply(command.InOrStdin(), command.ErrOrStderr())
				if err != nil {
					return err
				}
				if !confirmed {
					_, err := fmt.Fprintln(command.ErrOrStderr(), "edit canceled")
					return err
				}
			}
			evidence, err := client.Apply(command.Context(), target, manifest)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "%s/%s edited in context %s\n",
				evidence.Ref.Kind, evidence.Ref.Name, evidence.Ref.Scope)
			return err
		},
	}
	addLocalTargetFlags(command, options)
	command.Flags().StringVarP(&filename, "file", "f", "", "read edited YAML from a file instead of opening an editor")
	command.Flags().BoolVarP(&yes, "yes", "y", false, "apply after preview without prompting")
	return command
}

func addLocalTargetFlags(command *cobra.Command, options *localTargetOptions) {
	command.Flags().StringVar(&options.context, "context", "", "required kubeconfig context")
	command.Flags().StringVarP(&options.namespace, "namespace", "n", "default", "Kubernetes namespace")
}

func explicitResourceTarget(value string, options *localTargetOptions) (localops.Target, error) {
	if strings.TrimSpace(options.context) == "" {
		return localops.Target{}, fmt.Errorf("--context is required")
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return localops.Target{}, fmt.Errorf("resource must be expressed as <kind>/<name>")
	}
	return localops.Target{
		Context: options.context, Namespace: options.namespace, Kind: parts[0], Name: parts[1],
	}, nil
}

func explicitPodTarget(value string, options *localTargetOptions) (localops.Target, error) {
	if strings.TrimSpace(options.context) == "" {
		return localops.Target{}, fmt.Errorf("--context is required")
	}
	name := value
	if strings.Contains(value, "/") {
		parts := strings.Split(value, "/")
		if len(parts) != 2 || (parts[0] != "pod" && parts[0] != "pods") || parts[1] == "" {
			return localops.Target{}, fmt.Errorf("pod must be expressed as <name> or pod/<name>")
		}
		name = parts[1]
	}
	if strings.TrimSpace(name) == "" {
		return localops.Target{}, fmt.Errorf("pod name is required")
	}
	return localops.Target{Context: options.context, Namespace: options.namespace, Kind: "Pod", Name: name}, nil
}

func explicitForwardTarget(value string, options *localTargetOptions) (localops.Target, error) {
	target, err := explicitResourceTarget(value, options)
	if err != nil {
		return localops.Target{}, err
	}
	switch strings.ToLower(target.Kind) {
	case "pod", "pods":
		target.Kind = "Pod"
	case "service", "services", "svc":
		target.Kind = "Service"
	default:
		return localops.Target{}, fmt.Errorf("port-forward target must be pod/<name> or service/<name>")
	}
	return target, nil
}

func writeDescription(output io.Writer, description localops.Description) error {
	if _, err := io.WriteString(output, "Object:\n"); err != nil {
		return fmt.Errorf("write description: %w", err)
	}
	if _, err := output.Write(description.Object.YAML); err != nil {
		return fmt.Errorf("write described object: %w", err)
	}
	if _, err := io.WriteString(output, "\nEvents:\n"); err != nil {
		return fmt.Errorf("write description: %w", err)
	}
	if len(description.Events) == 0 {
		_, err := io.WriteString(output, "  <none>\n")
		return err
	}
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "TYPE\tREASON\tCOUNT\tMESSAGE"); err != nil {
		return err
	}
	for _, event := range description.Events {
		var object map[string]any
		if err := json.Unmarshal(event.Observed, &object); err != nil {
			return fmt.Errorf("decode described event: %w", err)
		}
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n",
			objectString(object, "type"), objectString(object, "reason"), objectNumber(object, "count"),
			strings.ReplaceAll(objectString(object, "message"), "\n", " ")); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func objectString(object map[string]any, key string) string {
	value, _ := object[key].(string)
	if value == "" {
		return "-"
	}
	return value
}

func objectNumber(object map[string]any, key string) string {
	switch value := object[key].(type) {
	case float64:
		return strconv.FormatInt(int64(value), 10)
	case json.Number:
		return value.String()
	default:
		return "-"
	}
}

func editedManifest(
	command *cobra.Command,
	client localops.Client,
	target localops.Target,
	filename string,
) ([]byte, error) {
	if filename != "" {
		return readBoundedFile(filename)
	}
	view, err := client.View(command.Context(), target, true)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp("", "sith-edit-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("create secure edit file: %w", err)
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if err := file.Chmod(0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("secure edit file: %w", err), file.Close())
	}
	if _, err := file.Write(view.YAML); err != nil {
		return nil, errors.Join(fmt.Errorf("write edit file: %w", err), file.Close())
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close edit file: %w", err)
	}
	if err := runEditor(command.Context(), command.InOrStdin(), command.OutOrStdout(), command.ErrOrStderr(), name); err != nil {
		return nil, err
	}
	return readBoundedFile(name)
}

func readBoundedFile(filename string) ([]byte, error) {
	// #nosec G304 -- the path is an explicit local CLI argument; content is size-bounded below.
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open edit file: %w", err)
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxLocalEditBytes+1))
	closeErr := file.Close()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("read edit file: %w", err), closeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close edit file: %w", closeErr)
	}
	if len(payload) > maxLocalEditBytes {
		return nil, fmt.Errorf("edit file exceeds %d bytes", maxLocalEditBytes)
	}
	return payload, nil
}

func runEditor(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, filename string) error {
	editor := os.Getenv("KUBE_EDITOR")
	if strings.TrimSpace(editor) == "" {
		editor = os.Getenv("EDITOR")
	}
	if strings.TrimSpace(editor) == "" {
		editor = "vi"
	}
	arguments := strings.Fields(editor)
	if len(arguments) == 0 {
		return fmt.Errorf("editor command is empty")
	}
	// #nosec G204,G702 -- the editor is user-selected and executed directly without a shell.
	process := exec.CommandContext(ctx, arguments[0], append(arguments[1:], filename)...)
	process.Stdin, process.Stdout, process.Stderr = stdin, stdout, stderr
	if err := process.Run(); err != nil {
		return fmt.Errorf("run editor %q: %w", arguments[0], err)
	}
	return nil
}

func unifiedYAMLDiff(current, proposed []byte, target localops.Target) (string, error) {
	return difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: difflib.SplitLines(string(current)), B: difflib.SplitLines(string(proposed)),
		FromFile: target.Kind + "/" + target.Name + " (current)",
		ToFile:   target.Kind + "/" + target.Name + " (server dry-run)",
		Context:  3,
	})
}

func confirmApply(input io.Reader, output io.Writer) (bool, error) {
	if _, err := io.WriteString(output, "Apply this server-validated edit? [y/N] "); err != nil {
		return false, fmt.Errorf("write edit prompt: %w", err)
	}
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("read edit confirmation: %w", err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func portForwardResult(err error) error {
	if err != nil {
		return fmt.Errorf("port-forward ended: %w", err)
	}
	return nil
}
