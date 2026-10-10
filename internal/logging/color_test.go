package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain text",
			input:    "Hello, world!",
			expected: "Hello, world!",
		},
		{
			name:     "single color code",
			input:    "\x1b[32mHello\x1b[0m",
			expected: "Hello",
		},
		{
			name:     "octal escape representation",
			input:    "\033[1;36mBold Cyan\033[0m",
			expected: "Bold Cyan",
		},
		{
			name:     "chained styles",
			input:    "\033[32m\033[1m[INFO]\033[0m streamer went \033[32mONLINE\033[0m",
			expected: "[INFO] streamer went ONLINE",
		},
		{
			name: "banner stripping",
			input: "\n\x1b[1;36m============================================================\x1b[0m\n" +
				"\x1b[1;33m              T D R O P   F A R M E R\x1b[0m\n" +
				"    Twitch Drops & Channel Points Farmer Bot in Go\n" +
				"\x1b[1;36m============================================================\x1b[0m\n\n",
			expected: "\n============================================================\n" +
				"              T D R O P   F A R M E R\n" +
				"    Twitch Drops & Channel Points Farmer Bot in Go\n" +
				"============================================================\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripANSI(tt.input)
			if got != tt.expected {
				t.Errorf("StripANSI() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestColorWriter(t *testing.T) {
	t.Run("enabled preserves ansi", func(t *testing.T) {
		var buf bytes.Buffer
		cw := NewColorWriterWithEnabled(&buf, true)
		if !cw.ColorEnabled() {
			t.Fatal("expected ColorEnabled() to be true")
		}

		raw := "\x1b[32mGreen\x1b[0m"
		n, err := cw.Write([]byte(raw))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != len(raw) {
			t.Errorf("expected n=%d, got %d", len(raw), n)
		}
		if buf.String() != raw {
			t.Errorf("expected %q, got %q", raw, buf.String())
		}
	})

	t.Run("disabled strips ansi", func(t *testing.T) {
		var buf bytes.Buffer
		cw := NewColorWriterWithEnabled(&buf, false)
		if cw.ColorEnabled() {
			t.Fatal("expected ColorEnabled() to be false")
		}

		raw := "\x1b[32mGreen\x1b[0m text"
		n, err := cw.Write([]byte(raw))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != len(raw) {
			t.Errorf("expected n=%d (length of original slice), got %d", len(raw), n)
		}
		if buf.String() != "Green text" {
			t.Errorf("expected %q, got %q", "Green text", buf.String())
		}
	})
}

func TestCheckColorSupport_Env(t *testing.T) {
	var buf bytes.Buffer

	t.Run("NO_COLOR disables color", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		if checkColorSupport(&buf) {
			t.Error("expected checkColorSupport to be false when NO_COLOR is set")
		}
	})

	t.Run("TERM=dumb disables color", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		t.Setenv("TERM", "dumb")
		if checkColorSupport(&buf) {
			t.Error("expected checkColorSupport to be false when TERM=dumb")
		}
	})

	t.Run("FORCE_COLOR enables color", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		t.Setenv("FORCE_COLOR", "1")
		if !checkColorSupport(&buf) {
			t.Error("expected checkColorSupport to be true when FORCE_COLOR is set")
		}
	})

	t.Run("CLICOLOR_FORCE enables color", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		t.Setenv("FORCE_COLOR", "")
		t.Setenv("CLICOLOR_FORCE", "1")
		if !checkColorSupport(&buf) {
			t.Error("expected checkColorSupport to be true when CLICOLOR_FORCE is set")
		}
	})
}

func TestConsoleHandler_ColorDisabled(t *testing.T) {
	var buf bytes.Buffer
	cw := NewColorWriterWithEnabled(&buf, false)
	handler := &ConsoleHandler{
		level:  slog.LevelInfo,
		writer: cw,
	}

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "test message", 0)
	record.AddAttrs(slog.String("streamer", "shroud"))

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("expected output to contain no ANSI codes, got %q", out)
	}
	if !strings.Contains(out, "[INFO]") || !strings.Contains(out, "test message") || !strings.Contains(out, "streamer=shroud") {
		t.Errorf("expected output to contain message and attributes, got %q", out)
	}
}

func TestConsoleHandler_CustomEvent(t *testing.T) {
	var buf bytes.Buffer
	cw := NewColorWriterWithEnabled(&buf, false)
	handler := &ConsoleHandler{
		level:  slog.LevelInfo,
		writer: cw,
	}

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "points updated", 0)
	record.AddAttrs(
		slog.String("streamer", "tarik"),
		slog.Int("gained", 50),
		slog.String("reason", "watch streak"),
		slog.Int("total", 1050),
	)

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("expected output to contain no ANSI codes, got %q", out)
	}
	if !strings.Contains(out, "tarik points updated: +50 (watch streak) | total: 1050") {
		t.Errorf("unexpected output formatting: %q", out)
	}
}
