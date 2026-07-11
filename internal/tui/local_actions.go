// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/pmezard/go-difflib/difflib"
	"golang.org/x/term"

	"github.com/ArdurAI/sith/internal/localops"
)

const (
	localLogTail       = int64(200)
	maxPanelBytes      = 1 << 20
	maxTUIEditFileSize = 10 << 20
)

func (model *Model) selectedLocalTarget() (localops.Target, error) {
	if model.local == nil {
		return localops.Target{}, fmt.Errorf("local operations are unavailable")
	}
	records := model.snapshot().Records
	if len(records) == 0 || model.cursor < 0 || model.cursor >= len(records) {
		return localops.Target{}, fmt.Errorf("select a resource row first")
	}
	record := records[model.cursor]
	return localops.Target{
		Context: record.Cluster, Namespace: record.Namespace, Kind: record.Kind, Name: record.Name,
	}, nil
}

func (model *Model) startLocalText(
	title string,
	operation func(context.Context, localops.Target) (string, error),
) (tea.Model, tea.Cmd) {
	target, err := model.selectedLocalTarget()
	if err != nil {
		model.setLocalError(err)
		return model, nil
	}
	model.panel = &localPanel{title: title, loading: true}
	return model, func() tea.Msg {
		content, err := operation(model.ctx, target)
		return localTextMsg{title: title, content: content, err: err}
	}
}

func (model *Model) yamlCommand(ctx context.Context, target localops.Target) (string, error) {
	view, err := model.local.View(ctx, target, false)
	if err != nil {
		return "", err
	}
	return string(view.YAML), nil
}

func (model *Model) describeCommand(ctx context.Context, target localops.Target) (string, error) {
	description, err := model.local.Describe(ctx, target)
	if err != nil {
		return "", err
	}
	var content strings.Builder
	content.WriteString(string(description.Object.YAML))
	content.WriteString("\nEvents:\n")
	if len(description.Events) == 0 {
		content.WriteString("  <none>\n")
		return content.String(), nil
	}
	for _, event := range description.Events {
		var object struct {
			Type    string `json:"type"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(event.Observed, &object); err != nil {
			return "", fmt.Errorf("decode event: %w", err)
		}
		fmt.Fprintf(&content, "  %-8s %-24s %s\n", object.Type, object.Reason, strings.ReplaceAll(object.Message, "\n", " "))
	}
	return content.String(), nil
}

func (model *Model) startLogs() (tea.Model, tea.Cmd) {
	target, err := model.selectedLocalTarget()
	if err != nil {
		model.setLocalError(err)
		return model, nil
	}
	if target.Kind != "Pod" {
		model.setLocalError(fmt.Errorf("logs require a selected Pod"))
		return model, nil
	}
	model.panel = &localPanel{title: "LOGS " + target.Context + "/" + target.Namespace + "/" + target.Name, loading: true}
	return model, func() tea.Msg {
		stream, err := model.local.Logs(model.ctx, target, localops.LogOptions{Follow: true, TailLines: pointer(localLogTail)})
		return localStreamMsg{stream: stream, err: err}
	}
}

func readLocalStream(stream io.ReadCloser) tea.Cmd {
	return func() tea.Msg {
		buffer := make([]byte, 4096)
		count, err := stream.Read(buffer)
		return localChunkMsg{stream: stream, chunk: string(buffer[:count]), err: err}
	}
}

func (model *Model) startExec() (tea.Model, tea.Cmd) {
	target, err := model.selectedLocalTarget()
	if err != nil {
		model.setLocalError(err)
		return model, nil
	}
	if target.Kind != "Pod" {
		model.setLocalError(fmt.Errorf("exec requires a selected Pod"))
		return model, nil
	}
	model.panel = &localPanel{title: "EXEC", content: "connecting to /bin/sh…\n", loading: true}
	command := &localExecCommand{ctx: model.ctx, client: model.local, target: target}
	return model, tea.Exec(command, func(err error) tea.Msg { return localExecDoneMsg{err: err} })
}

func (model *Model) startEdit() (tea.Model, tea.Cmd) {
	target, err := model.selectedLocalTarget()
	if err != nil {
		model.setLocalError(err)
		return model, nil
	}
	model.panel = &localPanel{title: "YAML EDIT", content: "opening editor…\n", loading: true}
	command := &localEditCommand{ctx: model.ctx, client: model.local, target: target}
	return model, tea.Exec(command, func(err error) tea.Msg { return localEditDoneMsg{err: err} })
}

func (model *Model) startForwardPrompt() (tea.Model, tea.Cmd) {
	target, err := model.selectedLocalTarget()
	if err != nil {
		model.setLocalError(err)
		return model, nil
	}
	if target.Kind != "Pod" && target.Kind != "Service" {
		model.setLocalError(fmt.Errorf("port-forward requires a selected Pod or Service"))
		return model, nil
	}
	model.pending = &target
	model.mode, model.input = modePortForward, ""
	return model, nil
}

func (model *Model) startForward() (tea.Model, tea.Cmd) {
	ports := strings.Fields(model.input)
	target := model.pending
	model.mode, model.input, model.pending = modeNormal, "", nil
	if target == nil || len(ports) == 0 {
		model.setLocalError(fmt.Errorf("enter at least one local:remote port mapping"))
		return model, nil
	}
	model.panel = &localPanel{title: "PORT-FORWARD", content: "connecting…\n", loading: true}
	targetCopy := *target
	return model, func() tea.Msg {
		session, err := model.local.PortForward(model.ctx, localops.ForwardRequest{Target: targetCopy, Ports: ports})
		if err != nil {
			return localForwardMsg{err: err}
		}
		select {
		case <-session.Ready():
			forwarded, err := session.Ports()
			if err != nil {
				_ = session.Close()
				return localForwardMsg{err: err}
			}
			return localForwardMsg{entry: &forwardEntry{target: targetCopy, ports: forwarded, session: session}}
		case err := <-session.Done():
			_ = session.Close()
			if err == nil {
				err = fmt.Errorf("port-forward ended before becoming ready")
			}
			return localForwardMsg{err: err}
		case <-model.ctx.Done():
			_ = session.Close()
			return localForwardMsg{err: model.ctx.Err()}
		}
	}
}

func waitForward(entry *forwardEntry) tea.Cmd {
	return func() tea.Msg { return localForwardDoneMsg{entry: entry, err: <-entry.session.Done()} }
}

func (model *Model) forwardPanel() string {
	if len(model.forwards) == 0 {
		return "no active port-forwards\n"
	}
	var content strings.Builder
	for index, entry := range model.forwards {
		status := "active"
		if entry.done {
			status = "ended"
			if entry.err != nil {
				status += ": " + entry.err.Error()
			}
		}
		fmt.Fprintf(&content, "%d. %s/%s %s  %s\n", index+1, entry.target.Context, entry.target.Name, formatForwardedPorts(entry.ports), status)
	}
	content.WriteString("\n[x] close newest active forward\n")
	return content.String()
}

func formatForwardedPorts(ports []localops.ForwardedPort) string {
	values := make([]string, 0, len(ports))
	for _, port := range ports {
		values = append(values, fmt.Sprintf("%d→%d", port.Local, port.Remote))
	}
	return strings.Join(values, ",")
}

func (model *Model) panelView() tea.View {
	panel := model.panel
	lines := strings.Split(panel.content, "\n")
	pageSize := max(model.height-5, 1)
	maxOffset := max(len(lines)-pageSize, 0)
	panel.offset = min(max(panel.offset, 0), maxOffset)
	end := min(panel.offset+pageSize, len(lines))
	var content strings.Builder
	fmt.Fprintf(&content, "sith  %s", panel.title)
	if panel.loading {
		content.WriteString("  ⟳")
	}
	content.WriteString("\n")
	content.WriteString(strings.Repeat("─", min(model.width, 80)))
	content.WriteString("\n")
	content.WriteString(strings.Join(lines[panel.offset:end], "\n"))
	content.WriteString("\n\n[↑/↓] scroll  [esc] return to exact fleet scope")
	if model.lastError != "" {
		content.WriteString("\nerror: ")
		content.WriteString(model.lastError)
	}
	view := tea.NewView(limitWidth(content.String(), model.width))
	view.AltScreen = true
	view.WindowTitle = "sith " + strings.ToLower(panel.title)
	return view
}

func (model *Model) handlePanelKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q":
		if model.panel.stream != nil {
			_ = model.panel.stream.Close()
		}
		model.panel = nil
	case "up", "k":
		model.panel.offset = max(model.panel.offset-1, 0)
	case "down", "j":
		model.panel.offset++
	case "x":
		if model.panel.title == "PORT-FORWARDS" {
			for index := len(model.forwards) - 1; index >= 0; index-- {
				if !model.forwards[index].done {
					_ = model.forwards[index].session.Close()
					break
				}
			}
		}
	}
	return model, nil
}

func (model *Model) appendPanel(chunk string) {
	if chunk == "" || model.panel == nil {
		return
	}
	model.panel.content += chunk
	if len(model.panel.content) > maxPanelBytes {
		model.panel.content = "… earlier output truncated …\n" + model.panel.content[len(model.panel.content)-maxPanelBytes:]
	}
	lines := strings.Count(model.panel.content, "\n") + 1
	model.panel.offset = max(lines-max(model.height-5, 1), 0)
}

func (model *Model) setLocalError(err error) {
	if err == nil {
		model.lastError = ""
		return
	}
	model.lastError = err.Error()
}

func (model *Model) closeLocalSessions() {
	if model.panel != nil && model.panel.stream != nil {
		_ = model.panel.stream.Close()
	}
	for _, entry := range model.forwards {
		_ = entry.session.Close()
	}
}

func pointer[T any](value T) *T { return &value }

type localExecCommand struct {
	ctx            context.Context
	client         localops.Client
	target         localops.Target
	stdin          io.Reader
	stdout, stderr io.Writer
}

func (command *localExecCommand) SetStdin(input io.Reader)   { command.stdin = input }
func (command *localExecCommand) SetStdout(output io.Writer) { command.stdout = output }
func (command *localExecCommand) SetStderr(output io.Writer) { command.stderr = output }

func (command *localExecCommand) Run() error {
	terminal, err := newExecTerminal(command.ctx, command.stdin, command.stdout)
	if err != nil {
		return err
	}
	defer terminal.Close()
	return command.client.Exec(command.ctx, command.target, localops.ExecOptions{
		Command: []string{"/bin/sh"}, Stdin: true, TTY: true,
	}, localops.Streams{Stdin: command.stdin, Stdout: command.stdout, Stderr: command.stderr, Sizes: terminal})
}

type execTerminal struct {
	ctx       context.Context
	input     *os.File
	output    *os.File
	state     *term.State
	last      localops.TerminalSize
	first     bool
	ticker    *time.Ticker
	closeOnce sync.Once
	done      chan struct{}
}

func newExecTerminal(ctx context.Context, input io.Reader, output io.Writer) (*execTerminal, error) {
	inputFile, inputOK := input.(*os.File)
	outputFile, outputOK := output.(*os.File)
	if !inputOK || !outputOK || !term.IsTerminal(int(inputFile.Fd())) || !term.IsTerminal(int(outputFile.Fd())) {
		return nil, fmt.Errorf("interactive TUI exec requires terminal stdin and stdout")
	}
	width, height, err := term.GetSize(int(outputFile.Fd()))
	if err != nil {
		return nil, err
	}
	state, err := term.MakeRaw(int(inputFile.Fd()))
	if err != nil {
		return nil, err
	}
	return &execTerminal{
		ctx: ctx, input: inputFile, output: outputFile, state: state,
		last:  localops.TerminalSize{Width: boundedTerminalSize(width), Height: boundedTerminalSize(height)},
		first: true, ticker: time.NewTicker(250 * time.Millisecond), done: make(chan struct{}),
	}, nil
}

func (terminal *execTerminal) Next() *localops.TerminalSize {
	if terminal.first {
		terminal.first = false
		size := terminal.last
		return &size
	}
	for {
		select {
		case <-terminal.ctx.Done():
			return nil
		case <-terminal.done:
			return nil
		case <-terminal.ticker.C:
			width, height, err := term.GetSize(int(terminal.output.Fd()))
			if err != nil {
				continue
			}
			size := localops.TerminalSize{Width: boundedTerminalSize(width), Height: boundedTerminalSize(height)}
			if size == terminal.last {
				continue
			}
			terminal.last = size
			return &size
		}
	}
}

func (terminal *execTerminal) Close() {
	terminal.closeOnce.Do(func() {
		terminal.ticker.Stop()
		close(terminal.done)
		_ = term.Restore(int(terminal.input.Fd()), terminal.state)
	})
}

func boundedTerminalSize(value int) uint16 {
	if value <= 0 {
		return 1
	}
	if value > int(^uint16(0)) {
		return ^uint16(0)
	}
	return uint16(value) // #nosec G115 -- range checked immediately above.
}

type localEditCommand struct {
	ctx            context.Context
	client         localops.Client
	target         localops.Target
	stdin          io.Reader
	stdout, stderr io.Writer
}

func (command *localEditCommand) SetStdin(input io.Reader)   { command.stdin = input }
func (command *localEditCommand) SetStdout(output io.Writer) { command.stdout = output }
func (command *localEditCommand) SetStderr(output io.Writer) { command.stderr = output }

func (command *localEditCommand) Run() error {
	if strings.EqualFold(command.target.Kind, "Secret") || strings.EqualFold(command.target.Kind, "Secrets") {
		return fmt.Errorf("interactive Secret edit is refused because it would persist plaintext in a temporary file")
	}
	view, err := command.client.View(command.ctx, command.target, true)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp("", "sith-tui-edit-*.yaml")
	if err != nil {
		return fmt.Errorf("create secure edit file: %w", err)
	}
	filename := file.Name()
	defer func() { _ = os.Remove(filename) }()
	if err := file.Chmod(0o600); err != nil {
		return errors.Join(err, file.Close())
	}
	if _, err := file.Write(view.YAML); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := command.edit(filename); err != nil {
		return err
	}
	manifest, err := readTUIEdit(filename)
	if err != nil {
		return err
	}
	preview, err := command.client.PreviewApply(command.ctx, command.target, manifest)
	if err != nil {
		return err
	}
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: difflib.SplitLines(string(preview.CurrentYAML)), B: difflib.SplitLines(string(preview.DryRunYAML)),
		FromFile: command.target.Kind + "/" + command.target.Name + " (current)",
		ToFile:   command.target.Kind + "/" + command.target.Name + " (server dry-run)", Context: 3,
	})
	if err != nil {
		return err
	}
	if diff == "" {
		_, err := fmt.Fprintln(command.stdout, "no changes")
		return err
	}
	if _, err := io.WriteString(command.stdout, diff); err != nil {
		return err
	}
	if _, err := io.WriteString(command.stderr, "Apply this server-validated edit? [y/N] "); err != nil {
		return err
	}
	answer, err := bufio.NewReader(command.stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if answer = strings.ToLower(strings.TrimSpace(answer)); answer != "y" && answer != "yes" {
		_, err := fmt.Fprintln(command.stderr, "edit canceled")
		return err
	}
	evidence, err := command.client.Apply(command.ctx, command.target, manifest)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(command.stdout, "%s/%s edited in context %s\n", evidence.Ref.Kind, evidence.Ref.Name, evidence.Ref.Scope)
	return err
}

func (command *localEditCommand) edit(filename string) error {
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
	// #nosec G204,G702 -- this is the user's editor, invoked directly without a shell.
	process := exec.CommandContext(command.ctx, arguments[0], append(arguments[1:], filename)...)
	process.Stdin, process.Stdout, process.Stderr = command.stdin, command.stdout, command.stderr
	return process.Run()
}

func readTUIEdit(filename string) ([]byte, error) {
	// #nosec G304 -- filename is the private temporary file created by this process.
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, maxTUIEditFileSize+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if len(payload) > maxTUIEditFileSize {
		return nil, fmt.Errorf("edited YAML exceeds %d bytes", maxTUIEditFileSize)
	}
	return payload, nil
}
