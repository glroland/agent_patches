package tests

import (
	"context"
	"log/slog"
	"testing"

	"agent_patches/server/logger"
)

func TestSetup_InfoLevel(t *testing.T) {
	logger.Setup("info")
	l := slog.Default()

	if l.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug should be disabled at info level")
	}
	if !l.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("info should be enabled at info level")
	}
	if !l.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("warn should be enabled at info level")
	}
}

func TestSetup_DebugLevel(t *testing.T) {
	logger.Setup("debug")
	l := slog.Default()

	if !l.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug should be enabled at debug level")
	}
}

func TestSetup_WarnLevel(t *testing.T) {
	logger.Setup("warn")
	l := slog.Default()

	if l.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("info should be disabled at warn level")
	}
	if !l.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("warn should be enabled at warn level")
	}
}

func TestSetup_ErrorLevel(t *testing.T) {
	logger.Setup("error")
	l := slog.Default()

	if l.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("warn should be disabled at error level")
	}
	if !l.Enabled(context.Background(), slog.LevelError) {
		t.Error("error should be enabled at error level")
	}
}

func TestSetup_UnknownLevel_DefaultsToInfo(t *testing.T) {
	logger.Setup("nonsense")
	l := slog.Default()

	if l.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug should be disabled for unknown level (defaults to info)")
	}
	if !l.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("info should be enabled for unknown level (defaults to info)")
	}
}
