// Package observability provides structured logging and Prometheus metrics shared
// across the service.
package observability

import (
	"log/slog"
	"os"
)

// NewLogger returns a JSON structured logger at the given level ("debug", "info",
// "warn", "error"). JSON output is the right default for production log pipelines.
func NewLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h)
}
