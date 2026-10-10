package logging

import (
	"io"
	"os"
	"regexp"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// StripANSI removes all ANSI escape sequences from a string.
func StripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// SupportsColor returns true if the standard output terminal supports color rendering.
func SupportsColor() bool {
	return checkColorSupport(os.Stdout)
}

// checkColorSupport checks whether writing to w can support ANSI colors.
func checkColorSupport(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	if f, ok := w.(*os.File); ok {
		return isTerminalColorSupported(f)
	}
	return false
}

// ColorWriter wraps an io.Writer and strips ANSI escape codes if color is disabled.
type ColorWriter struct {
	writer       io.Writer
	colorEnabled bool
}

// NewColorWriter returns a ColorWriter that automatically detects if w supports color.
func NewColorWriter(w io.Writer) *ColorWriter {
	return &ColorWriter{
		writer:       w,
		colorEnabled: checkColorSupport(w),
	}
}

// NewColorWriterWithEnabled returns a ColorWriter with explicit color enable flag.
func NewColorWriterWithEnabled(w io.Writer, colorEnabled bool) *ColorWriter {
	return &ColorWriter{
		writer:       w,
		colorEnabled: colorEnabled,
	}
}

func (cw *ColorWriter) Write(p []byte) (n int, err error) {
	if cw.colorEnabled {
		return cw.writer.Write(p)
	}
	clean := StripANSI(string(p))
	_, err = cw.writer.Write([]byte(clean))
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (cw *ColorWriter) ColorEnabled() bool {
	return cw.colorEnabled
}
