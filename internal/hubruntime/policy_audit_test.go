// SPDX-License-Identifier: Apache-2.0

package hubruntime

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ArdurAI/sith/internal/pep"
)

func TestOrderedPolicyAuditorRequiresDurabilityBeforeProcessLog(t *testing.T) {
	t.Parallel()

	var calls []string
	durableFailure := errors.New("database unavailable")
	auditor, err := newOrderedPolicyAuditor(
		pep.AuditFunc(func(context.Context, pep.AuditEvent) error {
			calls = append(calls, "durable")
			return durableFailure
		}),
		pep.AuditFunc(func(context.Context, pep.AuditEvent) error {
			calls = append(calls, "process")
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := auditor.Record(context.Background(), pep.AuditEvent{}); !errors.Is(err, durableFailure) {
		t.Fatalf("Record() error = %v, want durable failure", err)
	}
	if !reflect.DeepEqual(calls, []string{"durable"}) {
		t.Fatalf("sink calls = %v, want durable sink only", calls)
	}
}

func TestOrderedPolicyAuditorReportsProcessFailureAfterDurableAppend(t *testing.T) {
	t.Parallel()

	var calls []string
	processFailure := errors.New("process sink unavailable")
	auditor, err := newOrderedPolicyAuditor(
		pep.AuditFunc(func(context.Context, pep.AuditEvent) error {
			calls = append(calls, "durable")
			return nil
		}),
		pep.AuditFunc(func(context.Context, pep.AuditEvent) error {
			calls = append(calls, "process")
			return processFailure
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := auditor.Record(context.Background(), pep.AuditEvent{}); !errors.Is(err, processFailure) {
		t.Fatalf("Record() error = %v, want process failure", err)
	}
	if !reflect.DeepEqual(calls, []string{"durable", "process"}) {
		t.Fatalf("sink calls = %v, want durable then process", calls)
	}
}

func TestOrderedPolicyAuditorRejectsMissingSinks(t *testing.T) {
	t.Parallel()

	if _, err := newOrderedPolicyAuditor(nil, nil); err == nil {
		t.Fatal("missing policy audit sinks accepted")
	}
	if err := (orderedPolicyAuditor{}).Record(context.Background(), pep.AuditEvent{}); err == nil {
		t.Fatal("zero-value ordered auditor accepted an event")
	}
}
