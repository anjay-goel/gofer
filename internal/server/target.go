package server

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/driver/claude"
	"github.com/anjay-goel/gofer/internal/store"
)

const turnGrace = 60 * time.Second

type snapshot struct {
	byID map[string]claude.Session
	list []claude.Session
}

func (s *Server) snapshot() (snapshot, error) {
	list, err := s.Driver.List()
	if err != nil {
		return snapshot{}, err
	}
	snap := snapshot{byID: map[string]claude.Session{}, list: list}
	for _, sess := range list {
		if sess.SessionID != "" {
			snap.byID[sess.SessionID] = sess
		}
	}
	return snap, nil
}

func (sn snapshot) job(id string) (claude.Session, bool) {
	if id == "" {
		return claude.Session{}, false
	}
	for _, sess := range sn.list {
		if sess.Job == id {
			return sess, true
		}
	}
	return claude.Session{}, false
}

type target struct {
	agent  store.Agent
	sess   claude.Session
	listed bool
}

func (sn snapshot) target(a store.Agent) target {
	sess, ok := sn.byID[a.SessionID]
	return target{agent: a, sess: sess, listed: ok}
}

func foreignTarget(sess claude.Session) target {
	return target{agent: store.Agent{SessionID: sess.SessionID, Dir: sess.Cwd}, sess: sess, listed: true}
}

func (t target) managed() bool { return t.agent.Name != "" }

func (t target) state() core.State {
	switch {
	case t.listed:
		return t.sess.Phase()
	case t.agent.SessionID == "":
		return core.StateUnknown
	}
	return core.StateStopped
}

func (t target) ended() bool { return !t.listed || t.state().Ended() }

func (t target) requireSession(name string) error {
	if t.agent.SessionID == "" {
		return fmt.Errorf("%s has no session yet", name)
	}
	return nil
}

func (s *Server) view(t target, now time.Time) core.View {
	if !t.managed() {
		return s.foreignView(t.sess)
	}
	a := t.agent
	state := t.state()
	detail := t.sess.WaitingFor
	if a.Failure != "" && (state == core.StateIdle || state == core.StateStopped) {
		state = core.StateFailed
		detail = a.Failure
	}
	settled, unconfirmed := s.turnFinished(a, state, now)
	v := core.View{
		Name: a.Name, ID: a.SessionID, State: state, Detail: detail,
		Source: "gofer", Dir: a.Dir, Branch: a.Branch,
		Settled: settled, Unconfirmed: unconfirmed,
		Cut: state == core.StateStopped && !s.turnEnded(a),
	}
	if t.listed && t.sess.Cwd != "" {
		v.Dir = t.sess.Cwd
	}
	if t.listed && t.sess.PID > 0 {
		if rec, err := s.Driver.Record(t.sess.PID); err == nil {
			v.Link = rec.Link()
		}
	}
	return v
}

func (s *Server) foreignView(sess claude.Session) core.View {
	rec, _ := s.Driver.Record(sess.PID)
	name := sess.Name
	if name == "" {
		name = sess.SessionID
	}
	state := sess.Phase()
	return core.View{
		Name: name, ID: sess.SessionID, State: state, Detail: sess.WaitingFor,
		Source: sess.Source(rec), Dir: sess.Cwd, Link: rec.Link(),
		Settled: state == core.StateIdle || state.Ended(),
	}
}

func (s *Server) turnFinished(a store.Agent, state core.State, now time.Time) (settled, unconfirmed bool) {
	switch {
	case state.Ended():
		return true, false
	case state != core.StateIdle:
		return false, false
	case s.turnEnded(a):
		return true, false
	}
	return false, now.Sub(a.LastInput) > turnGrace
}

func (s *Server) turnEnded(a store.Agent) bool {
	if !a.LastStop.Before(a.LastInput) {
		return true
	}
	ended, err := s.Driver.TurnEnded(a.SessionID, a.LastToken)
	return err == nil && ended
}

func (s *Server) find(name string, snap snapshot) (target, error) {
	if name == "" {
		return target{}, errors.New("no agent name given")
	}
	a, err := s.Store.Load(name)
	if err == nil {
		return snap.target(a), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return target{}, err
	}
	agents, _, err := s.Store.List()
	if err != nil {
		return target{}, err
	}
	isID := func(id string) bool { return len(name) >= 8 && strings.HasPrefix(id, name) }
	var match []claude.Session
	for _, sess := range snap.list {
		if sess.Name == name || sess.Job == name || isID(sess.SessionID) {
			match = append(match, sess)
		}
	}
	switch len(match) {
	case 0:
		for _, a := range agents {
			if isID(a.SessionID) {
				return snap.target(a), nil
			}
		}
		return target{}, fmt.Errorf("no agent named %q", name)
	case 1:
		for _, a := range agents {
			if a.SessionID == match[0].SessionID {
				return snap.target(a), nil
			}
		}
		return foreignTarget(match[0]), nil
	}
	return target{}, fmt.Errorf("%d sessions match %q, use a session ID", len(match), name)
}

func (s *Server) locate(name string) (target, error) {
	snap, err := s.snapshot()
	if err != nil {
		return target{}, err
	}
	return s.find(name, snap)
}

func (s *Server) locateLocked(name string) (target, func(), error) {
	t, err := s.locate(name)
	if err != nil || !t.managed() {
		return t, func() {}, err
	}
	unlock, err := s.Store.LockDelivery(t.agent.Name)
	if err != nil {
		return t, nil, err
	}
	if t, err = s.locate(name); err != nil {
		unlock()
		return t, nil, err
	}
	return t, unlock, nil
}
