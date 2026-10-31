package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
)

const (
	toolClip     = 120
	summaryLimit = 240
	replyTail    = 256 * 1024
	peerPrefix   = "Another Claude session sent a message:"
	peerSuffix   = "This came from another Claude session"
)

var toolFields = []string{"command", "file_path", "pattern", "query", "sql", "url", "prompt", "description"}

func (d *Driver) transcriptPath(sessionID string) string {
	matches, _ := filepath.Glob(filepath.Join(d.home, ".claude", "projects", "*", sessionID+".jsonl"))
	var best string
	var newest time.Time
	for _, m := range matches {
		info, err := os.Stat(m)
		if err == nil && info.ModTime().After(newest) {
			best, newest = m, info.ModTime()
		}
	}
	return best
}

func (d *Driver) scanFrom(sessionID string, offset int64, fn func([]byte)) (int64, error) {
	f, err := os.Open(d.transcriptPath(sessionID))
	if errors.Is(err, fs.ErrNotExist) {
		return offset, nil
	}
	if err != nil {
		return offset, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return offset, err
	}
	offset = min(offset, info.Size())
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}
	read := bufio.NewReaderSize(f, 1024*1024)
	for {
		line, err := read.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return offset, err
		}
		if err == nil || json.Valid(line) {
			offset += int64(len(line))
			if row := bytes.TrimSpace(line); len(row) > 0 {
				fn(row)
			}
		}
		if err != nil {
			return offset, nil
		}
	}
}

func (d *Driver) scan(sessionID string, fn func([]byte)) error {
	_, err := d.scanFrom(sessionID, 0, fn)
	return err
}

func (d *Driver) TranscriptHas(sessionID, token string) (bool, error) {
	found := false
	err := d.scan(sessionID, func(line []byte) {
		found = found || bytes.Contains(line, []byte(token))
	})
	return found, err
}

func (d *Driver) TranscriptFrom(sessionID string, offset int64) ([]core.LogLine, int64, error) {
	var out []core.LogLine
	next, err := d.scanFrom(sessionID, offset, func(line []byte) {
		out = append(out, parseEntries(line)...)
	})
	return out, next, err
}

func (d *Driver) LastReply(sessionID string) string {
	f, err := os.Open(d.transcriptPath(sessionID))
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	start := max(info.Size()-replyTail, 0)
	tail := make([]byte, info.Size()-start)
	if _, err := f.ReadAt(tail, start); err != nil {
		return ""
	}
	lines := bytes.Split(tail, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		entries := parseEntries(lines[i])
		for j := len(entries) - 1; j >= 0; j-- {
			switch entries[j].Role {
			case "assistant":
				return summary(entries[j].Text)
			case "user":
				return ""
			}
		}
	}
	return ""
}

func summary(reply string) string {
	heading := ""
	for _, raw := range strings.Split(reply, "\n") {
		line := core.OneLine(strings.ReplaceAll(raw, "**", ""))
		text := strings.TrimSpace(strings.TrimLeft(line, "#>"))
		text = strings.TrimPrefix(strings.TrimPrefix(text, "- "), "* ")
		if text == "" || strings.HasPrefix(text, "```") || strings.Trim(text, "-=_*") == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			return clip(text, summaryLimit)
		}
		if heading == "" {
			heading = text
		}
	}
	return clip(heading, summaryLimit)
}

func (d *Driver) TranscriptSince(sessionID string, tokenTag *regexp.Regexp) ([]core.LogLine, error) {
	var out []core.LogLine
	err := d.scan(sessionID, func(line []byte) {
		if deliversInput(line, tokenTag) {
			out = nil
			return
		}
		out = append(out, parseEntries(line)...)
	})
	return out, err
}

func (d *Driver) TurnEnded(sessionID, token string) (bool, error) {
	seen := token == ""
	ended := false
	err := d.scan(sessionID, func(line []byte) {
		var row struct {
			Type    string `json:"type"`
			Message struct {
				StopReason string `json:"stop_reason"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &row) != nil {
			return
		}
		switch {
		case row.Type == "assistant":
			ended = seen && row.Message.StopReason == "end_turn"
		case token != "" && bytes.Contains(line, []byte(token)):
			seen, ended = true, false
		case row.Type == "user":
			ended = false
		}
	})
	return ended, err
}

func deliversInput(line []byte, tokenTag *regexp.Regexp) bool {
	if !tokenTag.Match(line) {
		return false
	}
	var row struct {
		Type       string `json:"type"`
		Attachment struct {
			Type string `json:"type"`
		} `json:"attachment"`
	}
	if json.Unmarshal(line, &row) != nil {
		return false
	}
	switch row.Type {
	case "queue-operation":
		return true
	case "attachment":
		return row.Attachment.Type == "queued_command"
	}
	for _, e := range parseEntries(line) {
		if e.Role == "user" && tokenTag.MatchString(e.Text) {
			return true
		}
	}
	return false
}

func parseEntries(line []byte) []core.LogLine {
	var row struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		Attachment struct {
			Type        string `json:"type"`
			CommandMode string `json:"commandMode"`
			Prompt      string `json:"prompt"`
		} `json:"attachment"`
	}
	if json.Unmarshal(line, &row) != nil {
		return nil
	}
	if a := row.Attachment; row.Type == "attachment" && a.Type == "queued_command" && a.CommandMode == "prompt" {
		return []core.LogLine{{Role: "user", Text: a.Prompt}}
	}
	if row.Type != "user" && row.Type != "assistant" {
		return nil
	}
	var text string
	if json.Unmarshal(row.Message.Content, &text) == nil {
		if hiddenText(text) {
			return nil
		}
		return []core.LogLine{{Role: row.Type, Text: text}}
	}
	var blocks []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(row.Message.Content, &blocks) != nil {
		return nil
	}
	var out []core.LogLine
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if !hiddenText(b.Text) {
				out = append(out, core.LogLine{Role: row.Type, Text: b.Text})
			}
		case "tool_use":
			out = append(out, core.LogLine{Role: "tool", Text: toolLine(b.Name, b.Input)})
		}
	}
	return out
}

func UnwrapPeer(text string) string {
	body, ok := strings.CutPrefix(strings.TrimSpace(text), peerPrefix)
	if !ok {
		return text
	}
	if end := strings.LastIndex(body, peerSuffix); end >= 0 {
		body = body[:end]
	}
	return strings.TrimSpace(body)
}

func hiddenText(text string) bool {
	text = strings.TrimSpace(text)
	return text == "" || strings.HasPrefix(text, "<task-notification>")
}

func toolLine(name string, input json.RawMessage) string {
	if parts := strings.SplitN(name, "__", 3); len(parts) == 3 && parts[0] == "mcp" {
		name = parts[1] + " " + parts[2]
	}
	var fields map[string]any
	json.Unmarshal(input, &fields)
	for _, key := range toolFields {
		if value, ok := fields[key].(string); ok && value != "" {
			return name + "  " + clip(value, toolClip)
		}
	}
	return name + "  " + clip(string(input), toolClip)
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return strings.ToValidUTF8(s[:n], "") + "…"
	}
	return s
}
