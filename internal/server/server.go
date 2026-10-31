package server

import (
	"fmt"
	"os"
	"path/filepath"
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
)

type Server struct {
	Home    string
	Store   *store.Store
	Driver  *claude.Driver
	Version string
}

func New(version string) (*Server, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Server{Home: home, Store: store.New(home), Driver: claude.New(home), Version: version}, nil
}

func isHomeRelative(path string) bool {
	return path == "~" || strings.HasPrefix(path, "~/")
}

func (s *Server) expand(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if isHomeRelative(path) {
		path = strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/")
	}
	return filepath.Join(s.Home, path)
}

func (s *Server) repoDir(repo, root string) string {
	if filepath.IsAbs(repo) || isHomeRelative(repo) {
		return s.expand(repo)
	}
	return filepath.Join(s.expand(root), repo)
}

func ago(t, now time.Time) string {
	d := now.Sub(t).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

var eventLabels = map[store.EventKind]string{
	store.EventStarted:  "started",
	store.EventFinished: "turn finished",
	store.EventErrored:  "turn failed",
	store.EventBlocked:  "needs attention",
	store.EventMessage:  "message sent",
	store.EventWoken:    "woken",
	store.EventStopped:  "stopped",
	store.EventEnded:    "session ended",
}

func describe(e store.Event, now time.Time) string {
	return eventLabels[e.Kind] + ", " + ago(e.Time, now)
}

func (s *Server) list(p core.ListParams) ([]core.View, error) {
	snap, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	agents, unreadable, err := s.Store.List()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	known := map[string]bool{}
	views := []core.View{}
	for _, a := range agents {
		known[a.SessionID] = true
		v := s.view(snap.target(a), now)
		events, _ := s.Store.Events(a.Name)
		if n := len(events); n > 0 {
			v.Changed = describe(events[n-1], now)
			if p.Client != "" {
				v.New = n > s.Store.Seen(a.Name, p.Client)
				if !p.Peek {
					s.Store.MarkSeen(a.Name, p.Client, n)
				}
			}
		}
		views = append(views, v)
	}
	for _, u := range unreadable {
		views = append(views, core.View{Name: u.Name, State: core.StateUnknown, Detail: u.Err.Error(), Source: "gofer"})
	}
	for _, sess := range snap.list {
		if known[sess.SessionID] || sess.SessionID == "" || sess.Phase().Ended() {
			continue
		}
		views = append(views, s.view(foreignTarget(sess), now))
	}
	return views, nil
}

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

func (s *Server) waitPass(names []string) (core.WaitResult, error) {
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
			result.Blocked = true
			v.Detail = s.blockReason(t, v.Detail)
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
	return result, nil
}

func (s *Server) wait(p core.WaitParams) (core.WaitResult, error) {
	deadline := time.Now().Add(time.Duration(p.Timeout) * time.Second)
	for {
		result, err := s.waitPass(p.Names)
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

func (s *Server) logs(p core.LogsParams) ([]core.LogLine, error) {
	t, err := s.locate(p.Name)
	if err != nil {
		return nil, err
	}
	if err := t.requireSession(p.Name); err != nil {
		return nil, err
	}
	return s.Driver.Transcript(t.agent.SessionID, p.Limit)
}

func (s *Server) stop(p core.NameParams) (core.StopResult, error) {
	t, err := s.locate(p.Name)
	if err != nil {
		return core.StopResult{}, err
	}
	if t.ended() {
		return core.StopResult{AlreadyStopped: true}, nil
	}
	if t.sess.Job == "" {
		return core.StopResult{}, fmt.Errorf("%s wasn't started with claude --bg, so stop it where it runs", p.Name)
	}
	if err := s.Driver.Stop(t.sess.Job); err != nil {
		return core.StopResult{}, err
	}
	if t.managed() {
		s.Store.Append(t.agent.Name, store.Event{Kind: store.EventStopped})
	}
	return core.StopResult{}, nil
}

func (s *Server) resolve(p core.NameParams) (core.ResolveResult, error) {
	t, err := s.locate(p.Name)
	if err != nil {
		return core.ResolveResult{}, err
	}
	if err := t.requireSession(p.Name); err != nil {
		return core.ResolveResult{}, err
	}
	r := core.ResolveResult{Job: t.sess.Job, Dir: t.agent.Dir}
	switch {
	case !t.listed:
		r.Note = "claude no longer lists this session, so there's nothing to attach to. Wake it with gofer send"
	case t.sess.Job == "":
		r.Note = "this session was not started with claude --bg, so it can't be attached. Follow it on claude.ai/code, or message it with gofer send"
	}
	if t.listed && t.sess.Cwd != "" {
		if _, err := os.Stat(t.sess.Cwd); err == nil {
			r.Dir = t.sess.Cwd
		}
	}
	return r, nil
}
