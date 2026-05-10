package logger

import (
	"log/slog"
	"os"
	"strings"
)

// Setup configures the default slog logger from a level string.
// Valid levels: "debug", "info", "warn", "error" (case-insensitive).
// All output goes to stderr.
func Setup(level string) {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, opts)))
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
