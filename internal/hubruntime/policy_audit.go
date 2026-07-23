// SPDX-License-Identifier: Apache-2.0

package hubruntime

import (
	"context"
	"fmt"

	"github.com/ArdurAI/sith/internal/pep"
)

type orderedPolicyAuditor struct {
	durable pep.Auditor
	process pep.Auditor
}

func newOrderedPolicyAuditor(durable, process pep.Auditor) (pep.Auditor, error) {
	if durable == nil || process == nil {
		return nil, fmt.Errorf("construct ordered policy auditor: durable and process sinks are required")
	}
	return orderedPolicyAuditor{durable: durable, process: process}, nil
}

func (auditor orderedPolicyAuditor) Record(ctx context.Context, event pep.AuditEvent) error {
	if auditor.durable == nil || auditor.process == nil {
		return fmt.Errorf("record ordered policy audit: sinks are required")
	}
	if err := auditor.durable.Record(ctx, event); err != nil {
		return fmt.Errorf("record durable policy audit: %w", err)
	}
	if err := auditor.process.Record(ctx, event); err != nil {
		return fmt.Errorf("record process policy audit: %w", err)
	}
	return nil
}
