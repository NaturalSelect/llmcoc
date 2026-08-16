package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"":        slog.LevelDebug, // 未设置时回落 Debug
		"bogus":   slog.LevelDebug, // 非法值同样回落 Debug
	}
	for raw, want := range cases {
		if got := parseLevel(raw); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestForAttachesComponent(t *testing.T) {
	var buf bytes.Buffer
	orig := slog.Default()
	defer slog.SetDefault(orig)
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	For("chat").Info("hello", "session", 42)

	out := buf.String()
	if !strings.Contains(out, "component=chat") {
		t.Errorf("output missing component attr: %s", out)
	}
	if !strings.Contains(out, "session=42") {
		t.Errorf("output missing session attr: %s", out)
	}
	if !strings.Contains(out, "msg=hello") {
		t.Errorf("output missing message: %s", out)
	}
}

func TestLevelFiltersDebug(t *testing.T) {
	var buf bytes.Buffer
	lvl := new(slog.LevelVar)
	lvl.Set(slog.LevelInfo)
	orig := slog.Default()
	defer slog.SetDefault(orig)
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: lvl})))

	For("x").Debug("should not appear")
	if buf.Len() != 0 {
		t.Errorf("expected no output at Info level for Debug log, got: %s", buf.String())
	}
	For("x").Info("should appear")
	if buf.Len() == 0 {
		t.Error("expected output for Info log at Info level")
	}
}
