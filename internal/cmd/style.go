package cmd

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/anjay-goel/gofer/internal/core"
)

const (
	red    = "31"
	green  = "32"
	yellow = "33"
	cyan   = "36"
	bold   = "1"
	dim    = "2"

	columnGap = 2
)

var (
	styled    = styleable(os.Stdout)
	errStyled = styleable(os.Stderr)

	stateLooks = map[core.State]cell{
		core.StateRunning: {"●", cyan},
		core.StateIdle:    {"✓", green},
		core.StateBlocked: {"!", yellow},
		core.StateFailed:  {"✗", red},
		core.StateStopped: {"○", dim},
		core.StateUnknown: {"?", dim},
	}
	checkLooks = map[string]cell{
		"ok":   {"✓", green},
		"note": {"●", yellow},
		"FAIL": {"✗", red},
	}
	roleLooks = map[string]cell{
		"user":      {"you", cyan},
		"assistant": {"claude", green},
		"tool":      {"tool", dim},
		"gofer":     {"gofer", yellow},
	}
)

type cell struct {
	text string
	code string
}

func styleable(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0 &&
		os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

func ansi(code, text string) string {
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func paint(code, text string) string {
	if !styled || code == "" {
		return text
	}
	return ansi(code, text)
}

func (c cell) String() string { return paint(c.code, c.text) }

func styledUsage() string {
	lines := strings.Split(usage, "\n")
	for i, line := range lines {
		switch {
		case strings.HasSuffix(line, ":") && !strings.HasPrefix(line, " "):
			lines[i] = paint(bold, line)
		case strings.HasPrefix(line, "  gofer "):
			fields := strings.SplitN(line, " ", 5)
			command := strings.Join(fields[:4], " ")
			lines[i] = paint(cyan, command) + strings.TrimPrefix(line, command)
		}
	}
	return strings.Join(lines, "\n")
}

func errorPrefix() string {
	if errStyled {
		return ansi(red, "gofer:")
	}
	return "gofer:"
}

func done(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if styled {
		line = paint(green, "✓") + " " + line
	}
	fmt.Println(line)
}

func checkMark(kind string) string {
	if !styled {
		return fmt.Sprintf("%-4s", kind)
	}
	return checkLooks[kind].String()
}

func stateCell(v core.View) cell {
	look := stateLooks[v.State]
	label := stateLabel(v)
	if styled {
		label = look.text + " " + label
	}
	return cell{label, look.code}
}

func table(rows [][]cell) string {
	widths := map[int]int{}
	for _, row := range rows {
		for i, c := range row {
			widths[i] = max(widths[i], utf8.RuneCountInString(c.text))
		}
	}
	var out strings.Builder
	for _, row := range rows {
		for i, c := range row {
			out.WriteString(c.String())
			if i < len(row)-1 {
				gap := widths[i] - utf8.RuneCountInString(c.text) + columnGap
				out.WriteString(strings.Repeat(" ", gap))
			}
		}
		out.WriteByte('\n')
	}
	return out.String()
}
