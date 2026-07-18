// SPDX-License-Identifier: Apache-2.0

package pep

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ArdurAI/sith/internal/tenancy"
)

// NewSlogAuditor constructs a structured hub policy-audit sink. A nil logger is rejected rather
// than silently discarding governance events.
func NewSlogAuditor(logger *slog.Logger) (Auditor, error) {
	if logger == nil {
		return nil, fmt.Errorf("construct policy slog auditor: logger is required")
	}
	return slogAuditor{logger: logger}, nil
}

type slogAuditor struct {
	logger *slog.Logger
}

func (auditor slogAuditor) Record(ctx context.Context, event AuditEvent) error {
	if auditor.logger == nil || ctx == nil {
		return fmt.Errorf("record policy audit: logger and context are required")
	}
	if err := event.Validate(); err != nil {
		return fmt.Errorf("record policy audit: %w", err)
	}
	attributes := []any{
		"audit", true,
		"surface", "hub-pep",
		slog.Time("audit_at", event.At.UTC()),
		"trace_id", event.TraceID,
		"workspace", event.WorkspaceID,
		"actor", event.Actor,
		"role", event.Role,
		"action", event.Action,
		"verb", event.Verb,
		"verdict", event.Verdict,
		"reason_code", event.ReasonCode,
	}
	level := slog.LevelWarn
	if event.Verdict == VerdictAllow {
		level = slog.LevelInfo
	}
	handler := auditor.logger.Handler()
	if !handler.Enabled(ctx, level) {
		return fmt.Errorf("record policy audit: structured event level is disabled")
	}
	record := slog.NewRecord(time.Now(), level, "policy decision", 0)
	record.Add(attributes...)
	if err := handler.Handle(ctx, record); err != nil {
		return fmt.Errorf("record policy audit: emit structured event: %w", err)
	}
	return nil
}

// Validate rejects malformed events before a logger can serialize them.
func (event AuditEvent) Validate() error {
	if event.At.IsZero() || event.At.After(time.Now().Add(time.Minute)) {
		return fmt.Errorf("policy audit time is invalid")
	}
	if !event.TraceID.Valid() {
		return fmt.Errorf("policy audit trace identifier is invalid")
	}
	if err := tenancy.ValidateWorkspaceID(event.WorkspaceID); err != nil {
		return fmt.Errorf("policy audit workspace: %w", err)
	}
	if err := validateSafeText("policy audit actor", event.Actor, maxActorBytes); err != nil {
		return err
	}
	if !event.Role.Valid() || (event.Action != tenancy.ActionRead && event.Action != tenancy.ActionExportAudit && event.Action != tenancy.ActionProposeIntent) {
		return fmt.Errorf("policy audit has unsupported role, action, or verb")
	}
	if !event.Role.Allows(event.Action) && (event.Verdict != VerdictDeny || (event.ReasonCode != "role-denied" && event.ReasonCode != "invalid-request")) {
		return fmt.Errorf("policy audit has impossible role and action outcome")
	}
	if event.Verb == invalidVerb {
		if event.Verdict != VerdictDeny || event.ReasonCode != "invalid-request" {
			return fmt.Errorf("policy audit has unsafe invalid-request sentinel")
		}
	} else if !event.Verb.validForAction(event.Action) {
		return fmt.Errorf("policy audit has unsupported role, action, or verb")
	}
	return (Decision{Verdict: event.Verdict, ReasonCode: event.ReasonCode}).Validate()
}
