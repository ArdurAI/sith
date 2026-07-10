// SPDX-License-Identifier: Apache-2.0

// Package logging constructs structured loggers for Sith commands.
package logging

import (
	"fmt"
	"io"
	"log/slog"
)

// New builds a structured logger at the requested level and output format.
func New(w io.Writer, level, format string) (*slog.Logger, error) {
	resolvedLevel, err := parseLevel(level)
	if err != nil {
		return nil, err
	}

	options := &slog.HandlerOptions{Level: resolvedLevel}
	var handler slog.Handler
	switch format {
	case "text":
		handler = slog.NewTextHandler(w, options)
	case "json":
		handler = slog.NewJSONHandler(w, options)
	default:
		return nil, fmt.Errorf("invalid log format %q: expected text or json", format)
	}

	return slog.New(handler), nil
}

func parseLevel(level string) (slog.Level, error) {
	switch level {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q: expected debug, info, warn, or error", level)
	}
}
