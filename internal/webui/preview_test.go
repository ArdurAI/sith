// SPDX-License-Identifier: Apache-2.0

package webui

import (
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/localops"
)

func TestPreviewGrantIsExactSingleUseAndExpires(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 10, 20, 0, 0, 0, time.UTC)
	manager := newPreviewManager()
	manager.now = func() time.Time { return now }
	target := localops.Target{Context: "alpha", Namespace: "apps", Kind: "ConfigMap", Name: "settings"}
	token, err := manager.issue(target, "mode: new\n")
	if err != nil {
		t.Fatalf("issue() error = %v", err)
	}
	if err := manager.consume(token, target, "mode: changed\n"); err == nil || !strings.Contains(err.Error(), "changed after preview") {
		t.Fatalf("consume(changed) error = %v", err)
	}
	if err := manager.consume(token, target, "mode: new\n"); err == nil || !strings.Contains(err.Error(), "fresh server dry-run") {
		t.Fatalf("consume(reused after mismatch) error = %v", err)
	}
	token, err = manager.issue(target, "mode: new\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.consume(token, target, "mode: new\n"); err != nil {
		t.Fatalf("consume(exact) error = %v", err)
	}
	if err := manager.consume(token, target, "mode: new\n"); err == nil {
		t.Fatal("consume(replay) error = nil")
	}
	token, err = manager.issue(target, "mode: new\n")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(previewLifetime + time.Second)
	if err := manager.consume(token, target, "mode: new\n"); err == nil {
		t.Fatal("consume(expired) error = nil")
	}
}
