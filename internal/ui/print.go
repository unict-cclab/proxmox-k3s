package ui

import (
	"fmt"
	"io"
	"os"
)

const reset = "\033[0m"

const (
	bold   = "\033[1m"
	dim    = "\033[2m"
	blue   = "\033[34m"
	cyan   = "\033[36m"
	green  = "\033[32m"
	yellow = "\033[33m"
	red    = "\033[31m"
)

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return os.Getenv("TERM") != "dumb"
}

func style(s, color string) string {
	if !colorEnabled() {
		return s
	}
	return color + s + reset
}

func Section(w io.Writer, title string) {
	fmt.Fprintf(w, "%s %s\n", style("◼", blue), style(title, bold))
}

func Step(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, "  %s %s\n", style("→", cyan), fmt.Sprintf(format, args...))
}

func Info(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, "  %s %s\n", style("•", dim), fmt.Sprintf(format, args...))
}

func Success(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, "  %s %s\n", style("✓", green), fmt.Sprintf(format, args...))
}

func Warn(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, "  %s %s\n", style("▲", yellow), fmt.Sprintf(format, args...))
}

func Error(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, "  %s %s\n", style("✗", red), fmt.Sprintf(format, args...))
}

func PromptPrefix(kind string) string {
	switch kind {
	case "warn":
		return style("▲", yellow)
	case "info":
		return style("•", dim)
	default:
		return style("→", cyan)
	}
}
