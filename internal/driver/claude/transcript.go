package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
)

const (
	toolClip   = 120
	peerPrefix = "Another Claude session sent a message:"
	peerSuffix = "This came from another Claude session"
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

func (d *Driver) scan(sessionID string, fn func([]byte)) error {
	f, err := os.Open(d.transcriptPath(sessionID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024)
	for scan.Scan() {
		fn(scan.Bytes())
	}
	return scan.Err()
}

func (d *Driver) TranscriptHas(sessionID, token string) (bool, error) {
	found := false
	err := d.scan(sessionID, func(line []byte) {
		found = found || bytes.Contains(line, []byte(token))
	})
	return found, err
}

func (d *Driver) Transcript(sessionID string, limit int) ([]core.LogLine, error) {
	var out []core.LogLine
	err := d.scan(sessionID, func(line []byte) {
		out = append(out, parseEntries(line)...)
	})
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, err
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
