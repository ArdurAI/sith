// SPDX-License-Identifier: Apache-2.0

//go:build unix

package cli

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReadAuditExportFileRejectsNamedPipeBeforeOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.pipe")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAuditExportFile(path); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("named pipe error = %v", err)
	}
}
