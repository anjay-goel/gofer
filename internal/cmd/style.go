package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unsafe"

	"github.com/anjay-goel/gofer/internal/core"
)

const (
	red    = "31"
	green  = "32"
	yellow = "33"
	cyan   = "36"
	bold   = "1"
	dim    = "2"

	ellipsis = "…"
	bell     = "\a"
	osc      = "\x1b]"
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
		"follow":    {"follow", dim},
	}
	wideRunes = [][2]rune{
		{0x1100, 0x115F}, {0x2E80, 0xA4CF}, {0xAC00, 0xD7A3}, {0xF900, 0xFAFF}, {0xFE30, 0xFE6F},
		{0xFF00, 0xFF60}, {0xFFE0, 0xFFE6}, {0x1F300, 0x1F64F}, {0x1F680, 0x1F6FF}, {0x1F900, 0x1F9FF},
		{0x20000, 0x3FFFD},
	}
)

type cell struct {
	text string
	code string
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	null, err := os.Stat(os.DevNull)
	return err != nil || !os.SameFile(info, null)
}

func styleable(f *os.File) bool {
	return isTerminal(f) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

func terminalWidth() int {
	if !isTerminal(os.Stdout) {
		return 0
	}
	var size struct{ rows, cols, x, y uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&size)))
	if errno == 0 && size.cols > 0 {
		return int(size.cols)
	}
	columns, _ := strconv.Atoi(os.Getenv("COLUMNS"))
	return columns
}

func runeWidth(r rune) int {
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 0
	}
	for _, span := range wideRunes {
		if r >= span[0] && r <= span[1] {
			return 2
		}
	}
	return 1
}

func displayWidth(text string) int {
	width := 0
	for _, r := range text {
		width += runeWidth(r)
	}
	return width
}

func clipWidth(text string, limit int) string {
	if displayWidth(text) <= limit {
		return text
	}
	width := 0
	for i, r := range text {
		width += runeWidth(r)
		if width >= limit {
			return strings.TrimRight(text[:i], " ") + ellipsis
		}
	}
	return text
}

func alertable() bool {
	return isTerminal(os.Stdout) && os.Getenv("TERM") != "dumb"
}

func terminalAlert(text string) {
	fmt.Print(osc + "9;" + text + bell + bell)
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

func styledHelp(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		switch {
		case strings.HasSuffix(line, ":") && !strings.HasPrefix(line, " "):
			lines[i] = paint(bold, line)
		case strings.HasPrefix(line, "Usage: "):
			lines[i] = paint(bold, "Usage:") + strings.TrimPrefix(line, "Usage:")
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

func stateLabel(v core.View, limit int) string {
	detail := v.Detail
	if v.Cut {
		detail = "cut short"
	}
	if detail == "" {
		return string(v.State)
	}
	if limit > 0 {
		detail = clipWidth(core.OneLine(detail), limit)
	}
	return fmt.Sprintf("%s (%s)", v.State, detail)
}

func stateCell(v core.View, limit int) cell {
	look := stateLooks[v.State]
	label := stateLabel(v, limit)
	if styled {
		label = look.text + " " + label
	}
	return cell{label, look.code}
}
