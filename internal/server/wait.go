package server

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/driver/claude"
	"github.com/anjay-goel/gofer/internal/store"
)

const (
	pollInterval = 4 * time.Second
	workLimit    = 32000
	followPoll   = time.Second
	followChecks = 4
)

func (s *Server) work(sessionID string) []core.LogLine {
	entries, err := s.Driver.TranscriptSince(sessionID, tokenTag)
	lines := slices.DeleteFunc(entries, func(l core.LogLine) bool { return l.Role == "user" })
	if err != nil {
		lines = append(lines, core.LogLine{Role: "gofer", Text: "couldn't read the whole transcript: " + err.Error()})
	}
	return trimWork(lines)
}

func trimWork(lines []core.LogLine) []core.LogLine {
	if len(lines) < 2 {
		return lines
	}
	last := lines[len(lines)-1]
	earlier := lines[:len(lines)-1]
	size := len(last.Text)
	for _, l := range earlier {
		size += len(l.Text)
	}
	dropped := 0
	for _, role := range []string{"tool", "assistant"} {
		earlier = slices.DeleteFunc(earlier, func(l core.LogLine) bool {
			if size <= workLimit || l.Role != role {
				return false
			}
			size -= len(l.Text)
			dropped++
			return true
		})
	}
	trimmed := append(earlier, last)
	if dropped > 0 {
		note := core.LogLine{Role: "gofer", Text: fmt.Sprintf("%d earlier entries left out, see gofer logs", dropped)}
		return slices.Insert(trimmed, 0, note)
	}
	return trimmed
}

func (s *Server) blockReason(t target, fallback string) string {
	if !t.managed() {
		return fallback
	}
	events, err := s.Store.Events(t.agent.Name)
	if err != nil || len(events) == 0 {
		return fallback
	}
	last := events[len(events)-1]
	if last.Kind != store.EventBlocked || last.Detail == "" {
		return fallback
	}
	return last.Detail
}

func tally(result *core.WaitResult, v core.View) {
	if v.State == core.StateBlocked {
		result.Blocked = true
	}
	if v.State == core.StateFailed || v.Cut {
		result.Failed = true
	}
	if v.Unconfirmed {
		result.Unconfirmed = true
	} else if !v.Settled {
		result.Finished = false
	}
	result.Views = append(result.Views, v)
}

func (s *Server) waitPass(names []string, first bool) (core.WaitResult, error) {
	snap, err := s.snapshot()
	if err != nil {
		return core.WaitResult{Trouble: "can't read claude's agent list: " + err.Error()}, nil
	}
	now := time.Now().UTC()
	result := core.WaitResult{Finished: true}
	for _, name := range names {
		t, err := s.find(name, snap)
		if err != nil {
			return result, err
		}
		if err := t.requireSession(name); err != nil {
			return result, err
		}
		v := s.view(t, now)
		if v.State == core.StateBlocked {
			v.Detail = s.blockReason(t, v.Detail)
		}
		if first && v.Returned() {
			result = core.WaitResult{Finished: true}
			tally(&result, v)
			return result, nil
		}
		tally(&result, v)
	}
	return result, nil
}

func (s *Server) wait(p core.WaitParams) (core.WaitResult, error) {
	deadline := time.Now().Add(time.Duration(p.Timeout) * time.Second)
	for {
		result, err := s.waitPass(p.Names, p.Any)
		if err != nil {
			return result, err
		}
		if result.Done() {
			for i := range result.Views {
				result.Views[i].Work = s.work(result.Views[i].ID)
			}
			return result, nil
		}
		left := time.Until(deadline)
		if left <= 0 {
			return result, nil
		}
		time.Sleep(min(pollInterval, left))
	}
}

func (s *Server) logs(p core.LogsParams) (core.LogsResult, error) {
	var result core.LogsResult
	t, err := s.locate(p.Name)
	if err != nil {
		return result, err
	}
	if err := t.requireSession(p.Name); err != nil {
		return result, err
	}
	lines, cursor, err := s.Driver.TranscriptFrom(t.agent.SessionID, 0)
	if p.Limit > 0 && len(lines) > p.Limit {
		lines = lines[len(lines)-p.Limit:]
	}
	return core.LogsResult{Lines: cleanLines(lines), Cursor: cursor, Link: s.link(t)}, err
}

func (s *Server) follow(p core.FollowParams) (core.FollowResult, error) {
	deadline := time.Now().Add(time.Duration(p.Timeout) * time.Second)
	result := core.FollowResult{Cursor: p.Cursor}
	for tick := 0; ; tick++ {
		if tick%followChecks == 0 {
			pass, err := s.waitPass([]string{p.Name}, false)
			if err != nil {
				return result, err
			}
			result.Wait = pass
		}
		if len(result.Wait.Views) == 1 {
			lines, next, err := s.Driver.TranscriptFrom(result.Wait.Views[0].ID, p.Cursor)
			if err != nil {
				return result, err
			}
			result.Lines, result.Cursor = cleanLines(lines), next
		}
		if result.Wait.Done() || len(result.Lines) > 0 || !time.Now().Before(deadline) {
			return result, nil
		}
		time.Sleep(followPoll)
	}
}

func cleanLines(lines []core.LogLine) []core.LogLine {
	for i, l := range lines {
		if l.Role == "user" {
			lines[i].Text = cleanInput(l.Text)
		}
	}
	return lines
}

func cleanInput(text string) string {
	text = tokenTag.ReplaceAllString(claude.UnwrapPeer(text), "")
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), closing))
}
