// SPDX-License-Identifier: Apache-2.0

package mcpserver

import (
	"context"
	"log/slog"
	"time"
)

// AuditEvent is one non-secret execution record for an MCP fleet read.
type AuditEvent struct {
	At        time.Time
	Tool      string
	Workspace string
	Actor     string
	Allowed   bool
	Records   int
	Duration  time.Duration
	Err       string
}

// Auditor records MCP read decisions without receiving raw tool arguments or results.
type Auditor interface {
	Record(context.Context, AuditEvent)
}

type slogAuditor struct {
	logger *slog.Logger
}

// NewSlogAuditor constructs a structured, secret-minimizing MCP audit sink.
func NewSlogAuditor(logger *slog.Logger) Auditor {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return slogAuditor{logger: logger}
}

func (auditor slogAuditor) Record(ctx context.Context, event AuditEvent) {
	attributes := []any{
		"audit", true,
		"surface", "mcp",
		"tool", event.Tool,
		"workspace", event.Workspace,
		"actor", event.Actor,
		"allowed", event.Allowed,
		"records", event.Records,
		"duration_ms", event.Duration.Milliseconds(),
	}
	if event.Err != "" {
		attributes = append(attributes, "error", event.Err)
	}
	auditor.logger.InfoContext(ctx, "fleet read", attributes...)
}
