// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/auditrecord"
	"github.com/ArdurAI/sith/internal/fleet"
)

func TestAuditVerifyReportsBoundedInternalIntegritySummary(t *testing.T) {
	path := writeAuditExportFile(t, validCLIAuditExport())
	stdout, stderr, exitCode := runCLI(t, []string{"audit", "verify", path}, fleet.StubSource{})
	if exitCode != 0 || stderr != "" {
		t.Fatalf("exit/stderr = %d/%q", exitCode, stderr)
	}
	for _, want := range []string{
		"Audit export integrity: internally-consistent (not externally anchored)",
		"Schema: sith.policy-audit-export/v1", "Workspace: workspace-a", "Entries: 1", "Head: 1 sha256:",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestAuditVerifyJSONDoesNotEchoEntries(t *testing.T) {
	path := writeAuditExportFile(t, validCLIAuditExport())
	stdout, stderr, exitCode := runCLI(t, []string{"audit", "verify", path, "-o", "json"}, fleet.StubSource{})
	if exitCode != 0 || stderr != "" {
		t.Fatalf("exit/stderr = %d/%q", exitCode, stderr)
	}
	var result auditVerifyResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if result.Integrity != auditIntegrityInternal || result.ExternallyAnchored || result.Entries != 1 ||
		result.WorkspaceID != "workspace-a" || strings.Contains(stdout, "user:alice") {
		t.Fatalf("verification result leaked or overstated integrity: %#v / %q", result, stdout)
	}
}

func TestReadAuditExportFileRejectsStrictJSONAndTampering(t *testing.T) {
	validPayload, err := json.Marshal(validCLIAuditExport())
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"unknown field":   bytes.Replace(validPayload, []byte(`{"schema":`), []byte(`{"unknown":"value","schema":`), 1),
		"duplicate field": bytes.Replace(validPayload, []byte(`{"schema":`), []byte(`{"schema":"duplicate","schema":`), 1),
		"case mismatch":   bytes.Replace(validPayload, []byte(`"schema"`), []byte(`"Schema"`), 1),
		"trailing JSON":   append(append([]byte{}, validPayload...), []byte(` {}`)...),
		"malformed":       []byte(`{"schema":`),
		"invalid UTF-8":   bytes.Replace(validPayload, []byte("user:alice"), []byte{'u', 's', 'e', 'r', ':', 0xff}, 1),
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit.json")
			if err := os.WriteFile(path, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readAuditExportFile(path); err == nil || !strings.Contains(err.Error(), "JSON is invalid") {
				t.Fatalf("readAuditExportFile() error = %v", err)
			}
		})
	}

	tampered := validCLIAuditExport()
	tampered.Entries[0].Actor = "user:mallory"
	path := writeAuditExportFile(t, tampered)
	if _, err := readAuditExportFile(path); err == nil || !strings.Contains(err.Error(), "integrity is invalid") {
		t.Fatalf("tampered readAuditExportFile() error = %v", err)
	}
}

func TestReadAuditExportFileRejectsOversizedAndNonRegularInputs(t *testing.T) {
	directory := t.TempDir()
	if _, err := readAuditExportFile(directory); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory error = %v", err)
	}

	target := writeAuditExportFile(t, validCLIAuditExport())
	symlink := filepath.Join(t.TempDir(), "audit-link.json")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := readAuditExportFile(symlink); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink error = %v", err)
	}

	oversized := filepath.Join(t.TempDir(), "oversized.json")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte{' '}, auditrecord.MaxDocumentBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAuditExportFile(oversized); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized error = %v", err)
	}
}

func validCLIAuditExport() auditrecord.Export {
	entry := auditrecord.Entry{
		Sequence: 1, FormatVersion: 1, RecordedAt: time.Date(2026, time.July, 18, 10, 0, 0, 0, time.UTC),
		TraceID: strings.Repeat("1", 32), Actor: "user:alice", Role: "admin", Action: "export-audit",
		Verb: "audit.export", Verdict: "allow", ReasonCode: "phase-1-audit-export",
		EventKind: "policy-decision", PreviousHash: "sha256:" + strings.Repeat("0", 64),
	}
	entryHash, err := auditrecord.RecomputeEntryHash("workspace-a", entry)
	if err != nil {
		panic(err)
	}
	entry.EntryHash = entryHash
	return auditrecord.Export{
		Schema: auditrecord.SchemaV1, WorkspaceID: "workspace-a",
		Chain:   auditrecord.Chain{HashAlgorithm: auditrecord.HashAlgorithm, HeadSequence: 1, HeadHash: entryHash},
		Entries: []auditrecord.Entry{entry},
	}
}

func writeAuditExportFile(t *testing.T, exported auditrecord.Export) string {
	t.Helper()
	payload, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "audit.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
