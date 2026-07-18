// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/spf13/cobra"
	strictjson "sigs.k8s.io/json"

	"github.com/ArdurAI/sith/internal/auditrecord"
)

const auditIntegrityInternal = "internally-consistent"

type auditVerifyResult struct {
	Integrity          string `json:"integrity"`
	ExternallyAnchored bool   `json:"externally_anchored"`
	Schema             string `json:"schema"`
	WorkspaceID        string `json:"workspace_id"`
	Entries            int    `json:"entries"`
	HeadSequence       int64  `json:"head_sequence"`
	HeadHash           string `json:"head_hash"`
}

func newAuditCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "audit",
		Short: "Inspect portable audit records",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	command.AddCommand(newAuditVerifyCommand(options))
	return command
}

func newAuditVerifyCommand(options *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "verify <export.json>",
		Short: "Verify the internal integrity of one portable audit export",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			exported, err := readAuditExportFile(args[0])
			if err != nil {
				return err
			}
			result := auditVerifyResult{
				Integrity: auditIntegrityInternal, ExternallyAnchored: false,
				Schema: exported.Schema, WorkspaceID: exported.WorkspaceID, Entries: len(exported.Entries),
				HeadSequence: exported.Chain.HeadSequence, HeadHash: exported.Chain.HeadHash,
			}
			return writeAuditVerifyResult(command, options.output, result)
		},
	}
}

func readAuditExportFile(path string) (auditrecord.Export, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return auditrecord.Export{}, fmt.Errorf("audit export file is unavailable")
	}
	if !before.Mode().IsRegular() {
		return auditrecord.Export{}, fmt.Errorf("audit export must be a regular file")
	}
	if before.Size() < 0 || before.Size() > auditrecord.MaxDocumentBytes {
		return auditrecord.Export{}, fmt.Errorf("audit export exceeds the %d-byte limit", auditrecord.MaxDocumentBytes)
	}

	// #nosec G304 -- the path is the command's explicit local argument; Lstat, regular-file,
	// same-file, and bounded-read checks constrain it before any document content is trusted.
	file, err := os.Open(path)
	if err != nil {
		return auditrecord.Export{}, fmt.Errorf("audit export file is unavailable")
	}
	after, statErr := file.Stat()
	if statErr != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = file.Close()
		return auditrecord.Export{}, fmt.Errorf("audit export must be a stable regular file")
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, auditrecord.MaxDocumentBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return auditrecord.Export{}, fmt.Errorf("audit export file could not be read")
	}
	if len(payload) > auditrecord.MaxDocumentBytes {
		return auditrecord.Export{}, fmt.Errorf("audit export exceeds the %d-byte limit", auditrecord.MaxDocumentBytes)
	}
	if !utf8.Valid(payload) {
		return auditrecord.Export{}, fmt.Errorf("audit export JSON is invalid")
	}

	var exported auditrecord.Export
	strictErrors, decodeErr := strictjson.UnmarshalStrict(payload, &exported)
	if decodeErr != nil || len(strictErrors) != 0 {
		return auditrecord.Export{}, fmt.Errorf("audit export JSON is invalid")
	}
	if err := exported.Verify(); err != nil {
		return auditrecord.Export{}, fmt.Errorf("audit export integrity is invalid: %w", err)
	}
	return exported, nil
}

func writeAuditVerifyResult(command *cobra.Command, format string, result auditVerifyResult) error {
	switch format {
	case "json":
		if err := json.NewEncoder(command.OutOrStdout()).Encode(result); err != nil {
			return fmt.Errorf("write audit verification JSON: %w", err)
		}
		return nil
	case "yaml":
		return writeYAML(command.OutOrStdout(), result, "audit verification")
	default:
		_, err := fmt.Fprintf(command.OutOrStdout(),
			"Audit export integrity: %s (not externally anchored)\nSchema: %s\nWorkspace: %s\nEntries: %d\nHead: %d %s\n",
			result.Integrity, result.Schema, result.WorkspaceID, result.Entries, result.HeadSequence, result.HeadHash,
		)
		if err != nil {
			return fmt.Errorf("write audit verification output: %w", err)
		}
		return nil
	}
}
