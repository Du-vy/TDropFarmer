package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Du-vy/TDropFarmer/internal/config"
)

type SetupResult struct {
	Logger *slog.Logger
	close  func() error
}

func Setup(cfg config.LoggingConfig) (SetupResult, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return SetupResult{}, err
	}

	opts := &slog.HandlerOptions{Level: level}

	// Create console handler (with colors)
	consoleHandler := NewConsoleHandler(os.Stdout, level)

	var closeFn func() error = func() error { return nil }
	var handlers []slog.Handler = []slog.Handler{consoleHandler}

	if cfg.File != "" {
		if dir := filepath.Dir(cfg.File); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return SetupResult{}, fmt.Errorf("create log directory: %w", err)
			}
		}
		file, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return SetupResult{}, fmt.Errorf("open log file: %w", err)
		}
		closeFn = file.Close

		// Create file handler (standard plain text slog)
		var fileHandler slog.Handler
		switch cfg.Format {
		case "json":
			fileHandler = slog.NewJSONHandler(file, opts)
		default:
			fileHandler = slog.NewTextHandler(file, opts)
		}
		handlers = append(handlers, fileHandler)
	}

	var handler slog.Handler
	if len(handlers) == 1 {
		handler = handlers[0]
	} else {
		handler = &MultiHandler{handlers: handlers}
	}

	return SetupResult{Logger: slog.New(handler), close: closeFn}, nil
}

func (r SetupResult) Close() error {
	if r.close == nil {
		return nil
	}
	return r.close()
}

func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(value) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unsupported log level %q", value)
	}
}

// MultiHandler forwards log records to multiple handlers
type MultiHandler struct {
	handlers []slog.Handler
}

func (m *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return &MultiHandler{handlers: next}
}

func (m *MultiHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithGroup(name)
	}
	return &MultiHandler{handlers: next}
}

// ConsoleHandler is a custom slog handler for pretty, colored console output
type ConsoleHandler struct {
	level  slog.Level
	writer io.Writer
	attrs  []slog.Attr
	group  string
}

func NewConsoleHandler(w io.Writer, level slog.Level) *ConsoleHandler {
	return &ConsoleHandler{
		level:  level,
		writer: w,
	}
}

func (c *ConsoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= c.level
}

func (c *ConsoleHandler) Handle(_ context.Context, r slog.Record) error {
	var levelStr string
	switch r.Level {
	case slog.LevelDebug:
		levelStr = "\033[90m[DEBUG]\033[0m"
	case slog.LevelInfo:
		levelStr = "\033[32m\033[1m[INFO]\033[0m"
	case slog.LevelWarn:
		levelStr = "\033[33m\033[1m[WARN]\033[0m"
	case slog.LevelError:
		levelStr = "\033[31m\033[1m[ERROR]\033[0m"
	default:
		levelStr = fmt.Sprintf("[%s]", r.Level)
	}

	timeStr := r.Time.Format("15:04:05")
	msg := r.Message

	var attrsBuilder strings.Builder
	for _, attr := range c.attrs {
		c.writeAttr(&attrsBuilder, attr)
	}
	r.Attrs(func(attr slog.Attr) bool {
		c.writeAttr(&attrsBuilder, attr)
		return true
	})

	attrStr := strings.TrimSpace(attrsBuilder.String())
	if attrStr != "" {
		attrStr = " " + attrStr
	}

	fmt.Fprintf(c.writer, "\033[90m%s\033[0m %s %s%s\n", timeStr, levelStr, msg, attrStr)
	return nil
}

func (c *ConsoleHandler) writeAttr(sb *strings.Builder, attr slog.Attr) {
	key := attr.Key
	val := attr.Value.Any()
	if key == "streamer" || key == "login" {
		sb.WriteString(fmt.Sprintf("\033[90m%s=\033[0m\033[36m%v\033[0m ", key, val))
	} else if key == "error" {
		sb.WriteString(fmt.Sprintf("\033[31m%s=\033[0m\033[31m%v\033[0m ", key, val))
	} else if key == "balance" || key == "points" || key == "gained" || key == "total" {
		sb.WriteString(fmt.Sprintf("\033[90m%s=\033[0m\033[33m%v\033[0m ", key, val))
	} else {
		sb.WriteString(fmt.Sprintf("\033[90m%s=\033[0m%v ", key, val))
	}
}

func (c *ConsoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ConsoleHandler{
		level:  c.level,
		writer: c.writer,
		attrs:  append(c.attrs, attrs...),
		group:  c.group,
	}
}

func (c *ConsoleHandler) WithGroup(name string) slog.Handler {
	return &ConsoleHandler{
		level:  c.level,
		writer: c.writer,
		attrs:  c.attrs,
		group:  c.group + "/" + name,
	}
}
