package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Setup configures the default slog logger. When file is non-empty the log
// file is rotated on each call: the existing file is renamed to <file>.1,
// previous .1 becomes .2, and so on up to maxBackups. Numbered files beyond
// maxBackups are deleted. Pass maxBackups=0 to disable rotation entirely.
func Setup(level, file string, maxBackups int) error {
	if file != "" {
		rotateLog(file, maxBackups)
	}
	w, err := openWriter(file)
	if err != nil {
		return err
	}
	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, opts)))
	return nil
}

// rotateLog shifts existing numbered backups up by one and renames file to
// file.1. Files at positions >= maxBackups are deleted first to make room.
func rotateLog(file string, maxBackups int) {
	if maxBackups <= 0 {
		return
	}
	// Delete the oldest backups (maxBackups and beyond) so the shift below
	// doesn't push the count over the limit.
	for n := maxBackups; ; n++ {
		name := fmt.Sprintf("%s.%d", file, n)
		if _, err := os.Stat(name); os.IsNotExist(err) {
			break
		}
		os.Remove(name) //nolint:errcheck — best-effort cleanup
	}
	// Shift .N-1 → .N from the top down to avoid clobbering.
	for n := maxBackups - 1; n >= 1; n-- {
		src := fmt.Sprintf("%s.%d", file, n)
		dst := fmt.Sprintf("%s.%d", file, n+1)
		os.Rename(src, dst) //nolint:errcheck — file may not exist; that's fine
	}
	// Rotate the current log to .1.
	os.Rename(file, file+".1") //nolint:errcheck — file may not exist on first run
}

func openWriter(file string) (io.Writer, error) {
	if file == "" {
		return os.Stderr, nil
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0640)
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
