package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Setup configures the default slog logger from a level string and optional
// file path. When file is empty, output goes to stderr. When a file path is
// provided, output goes to that file (appended, created if missing).
func Setup(level, file string) error {
	w, err := openWriter(file)
	if err != nil {
		return err
	}
	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, opts)))
	return nil
}

func openWriter(file string) (io.Writer, error) {
	if file == "" {
		return os.Stderr, nil
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		return nil, err
	}
	return f, nil
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
