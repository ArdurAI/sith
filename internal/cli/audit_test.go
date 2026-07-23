// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestAuditVerifyPagesReportsCompleteBoundedSequence(t *testing.T) {
	pages := validCLIAuditPages(t, auditrecord.MaxEntries+1)
	paths := writeAuditPageFiles(t, pages)
	args := append([]string{"audit", "verify-pages"}, paths...)
	stdout, stderr, exitCode := runCLI(t, args, fleet.StubSource{})
	if exitCode != 0 || stderr != "" {
		t.Fatalf("exit/stderr = %d/%q", exitCode, stderr)
	}
	for _, want := range []string{
		"Audit page sequence integrity: internally-consistent (not externally anchored)",
		"Schema: sith.policy-audit-page/v1", "Workspace: workspace-a", "Pages: 2", "Entries: 513", "Head: 513 sha256:",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestAuditVerifyPagesJSONDoesNotEchoEntries(t *testing.T) {
	paths := writeAuditPageFiles(t, validCLIAuditPages(t, 1))
	stdout, stderr, exitCode := runCLI(t, []string{"audit", "verify-pages", paths[0], "-o", "json"}, fleet.StubSource{})
	if exitCode != 0 || stderr != "" {
		t.Fatalf("exit/stderr = %d/%q", exitCode, stderr)
	}
	var result auditPageVerifyResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if result.Integrity != auditIntegrityInternal || result.ExternallyAnchored || result.Pages != 1 ||
		result.Entries != 1 || result.WorkspaceID != "workspace-a" || strings.Contains(stdout, "user:alice") {
		t.Fatalf("page verification leaked or overstated integrity: %#v / %q", result, stdout)
	}
}

func TestAuditVerifyPagesRejectsMissingReorderedAndTamperedPages(t *testing.T) {
	pages := validCLIAuditPages(t, auditrecord.MaxEntries+1)
	for name, candidate := range map[string][]auditrecord.Page{
		"missing final": {pages[0]},
		"reordered":     {pages[1], pages[0]},
		"replayed":      {pages[0], pages[0]},
		"tampered": func() []auditrecord.Page {
			changed := pages[1]
			changed.Entries = append([]auditrecord.Entry(nil), changed.Entries...)
			changed.Entries[0].Actor = "user:mallory"
			return []auditrecord.Page{pages[0], changed}
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			paths := writeAuditPageFiles(t, candidate)
			args := append([]string{"audit", "verify-pages"}, paths...)
			_, stderr, exitCode := runCLI(t, args, fleet.StubSource{})
			if exitCode == 0 || !strings.Contains(stderr, "integrity is invalid") {
				t.Fatalf("exit/stderr = %d/%q", exitCode, stderr)
			}
		})
	}
}

func TestReadAuditPageFileRejectsStrictJSONAndNonRegularInput(t *testing.T) {
	page := validCLIAuditPages(t, 1)[0]
	payload, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(payload, []byte(`{"schema":`), []byte(`{"unknown":"value","schema":`), 1)
	path := filepath.Join(t.TempDir(), "audit-page.json")
	if err := os.WriteFile(path, unknown, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAuditPageFile(path); err == nil || !strings.Contains(err.Error(), "JSON is invalid") {
		t.Fatalf("strict page error = %v", err)
	}
	target := writeAuditPageFiles(t, []auditrecord.Page{page})[0]
	symlink := filepath.Join(t.TempDir(), "audit-page-link.json")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := readAuditPageFile(symlink); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("page symlink error = %v", err)
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

func validCLIAuditPages(t *testing.T, count int) []auditrecord.Page {
	t.Helper()
	entries := make([]auditrecord.Entry, count)
	previous := "sha256:" + strings.Repeat("0", 64)
	for index := range entries {
		entry := auditrecord.Entry{
			Sequence: int64(index + 1), FormatVersion: 1,
			RecordedAt: time.Date(2026, time.July, 18, 10, 0, 0, index*1000, time.UTC).Truncate(time.Microsecond),
			TraceID:    strings.Repeat("1", 32), Actor: "user:alice", Role: "admin", Action: "export-audit",
			Verb: "audit.export", Verdict: "allow", ReasonCode: "phase-1-audit-export",
			EventKind: "policy-decision", PreviousHash: previous,
		}
		hash, err := auditrecord.RecomputeEntryHash("workspace-a", entry)
		if err != nil {
			t.Fatal(err)
		}
		entry.EntryHash = hash
		entries[index] = entry
		previous = hash
	}
	snapshot := auditrecord.Chain{HashAlgorithm: auditrecord.HashAlgorithm, HeadSequence: int64(count), HeadHash: previous}
	pages := make([]auditrecord.Page, 0, (count+auditrecord.MaxEntries-1)/auditrecord.MaxEntries)
	for start := 0; start < count; start += auditrecord.MaxEntries {
		end := start + auditrecord.MaxEntries
		if end > count {
			end = count
		}
		page := auditrecord.Page{
			Schema: auditrecord.PageSchemaV1, WorkspaceID: "workspace-a", Snapshot: snapshot,
			StartSequence: int64(start + 1), PreviousHash: entries[start].PreviousHash,
			Entries: append([]auditrecord.Entry(nil), entries[start:end]...),
		}
		if end < count {
			cursor, err := auditrecord.EncodePageCursor(
				"workspace-a", int64(count), previous, int64(end+1), entries[end-1].EntryHash,
			)
			if err != nil {
				t.Fatal(err)
			}
			page.NextCursor = cursor
		}
		pages = append(pages, page)
	}
	return pages
}

func writeAuditPageFiles(t *testing.T, pages []auditrecord.Page) []string {
	t.Helper()
	directory := t.TempDir()
	paths := make([]string, len(pages))
	for index, page := range pages {
		payload, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		paths[index] = filepath.Join(directory, fmt.Sprintf("audit-page-%04d.json", index))
		if err := os.WriteFile(paths[index], payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}
