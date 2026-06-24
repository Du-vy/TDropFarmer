package logging

import (
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

	writer := io.Writer(os.Stdout)
	closeFn := func() error { return nil }
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
		writer = io.MultiWriter(os.Stdout, file)
		closeFn = file.Close
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch cfg.Format {
	case "", "text":
		handler = slog.NewTextHandler(writer, opts)
	case "json":
		handler = slog.NewJSONHandler(writer, opts)
	default:
		return SetupResult{}, fmt.Errorf("unsupported log format %q", cfg.Format)
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
