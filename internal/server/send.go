package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/driver/claude"
	"github.com/anjay-goel/gofer/internal/store"
)

var confirmWindow = 15 * time.Second

var tokenTag = regexp.MustCompile(`\[gofer-[0-9a-f]{10}\]`)

func newToken() string {
	b := make([]byte, 5)
	rand.Read(b)
	return "gofer-" + hex.EncodeToString(b)
}

func tagged(message, token string) string {
	return fmt.Sprintf("%s\n\n[%s]", message, token)
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

func (s *Server) post(t target, name, text string) error {
	var last error
	var candidates []string
	if t.agent.Socket != "" {
		candidates = append(candidates, t.agent.Socket)
	}
	if rec, err := s.Driver.Record(t.sess.PID); err == nil && rec.Socket != "" {
		candidates = append(candidates, rec.Socket)
	}
	for _, path := range candidates {
		if !isSocket(path) {
			continue
		}
		if last = claude.Post(path, text); last == nil {
			return nil
		}
	}
	if last == nil {
		last = fmt.Errorf("%s has no inbox socket yet, so send the message again in a moment", name)
	}
	return last
}

func (s *Server) send(p core.SendParams) (core.SendResult, error) {
	t, unlock, err := s.locateLocked(p.Name)
	if err != nil {
		return core.SendResult{}, err
	}
	defer unlock()
	if err := t.requireSession(p.Name); err != nil {
		return core.SendResult{}, err
	}
	return s.deliver(t, p)
}

func (s *Server) deliver(t target, p core.SendParams) (core.SendResult, error) {
	var result core.SendResult
	var kind store.EventKind
	token := newToken()
	switch state := t.state(); state {
	case core.StateRunning, core.StateIdle:
		result.Action, kind = "posted", store.EventMessage
		if err := s.post(t, p.Name, tagged(p.Message, token)); err != nil {
			return result, err
		}
	case core.StateStopped, core.StateFailed:
		result.Action, kind = "resumed", store.EventWoken
		if err := s.resume(t, p.Message, token); err != nil && !errors.Is(err, claude.ErrUnconfirmed) {
			return result, err
		}
	default:
		return result, refusal(p.Name, state, t.sess)
	}
	s.record(t, kind, token)
	result.Delivered, result.Note = s.confirm(t.agent.SessionID, token)
	return result, nil
}

func refusal(name string, state core.State, sess claude.Session) error {
	if state != core.StateBlocked {
		return fmt.Errorf("%s is in a state gofer doesn't recognise, so the message wasn't sent. Try again, or run gofer attach %s", name, name)
	}
	reason := "blocked"
	if sess.WaitingFor != "" {
		reason += " (" + sess.WaitingFor + ")"
	}
	return fmt.Errorf("%s is %s, and a message can't clear that. Run gofer attach %s", name, reason, name)
}

func (s *Server) resume(t target, message, token string) error {
	if _, err := os.Stat(t.agent.Dir); err != nil {
		return fmt.Errorf("%s is gone, so the agent can't be woken and the message wasn't sent", t.agent.Dir)
	}
	arg, err := s.promptArg(t.agent.Name, message)
	if err != nil {
		return err
	}
	text := tagged(arg, token)
	if t.listed {
		return s.Driver.Resume(t.agent.SessionID, t.agent.Dir, text)
	}
	return s.Driver.Respawn(t.agent.SessionID, t.agent.Dir, text, s.launch(t.agent))
}

func (s *Server) record(t target, kind store.EventKind, token string) {
	if !t.managed() {
		return
	}
	s.Store.Update(t.agent.Name, func(a *store.Agent) {
		a.LastInput = time.Now().UTC()
		a.LastToken = token
		a.Failure = ""
		a.Summary = ""
	})
	s.Store.Append(t.agent.Name, store.Event{Kind: kind})
}

func (s *Server) confirm(sessionID, token string) (bool, string) {
	deadline := time.Now().Add(confirmWindow)
	for time.Now().Before(deadline) {
		found, err := s.Driver.TranscriptHas(sessionID, token)
		if err != nil {
			return false, "couldn't read the transcript: " + err.Error()
		}
		if found {
			return true, ""
		}
		time.Sleep(500 * time.Millisecond)
	}
	snap, err := s.snapshot()
	if err == nil {
		if sess, ok := snap.byID[sessionID]; ok && sess.Phase() == core.StateBlocked {
			return false, "the agent is holding this message for approval, so attach to it. Agents started with --bypass take messages directly"
		}
	}
	return false, "not in the transcript yet. A busy agent reads messages between tool calls, so check gofer logs before resending"
}
