package server

import (
	"io"
	"time"

	"github.com/anjay-goel/gofer/internal/driver/claude"
	"github.com/anjay-goel/gofer/internal/store"
)

func (s *Server) Hook(name string, in io.Reader) {
	h, err := claude.ParseHook(in)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	switch h.Kind {
	case claude.HookSessionStart:
		s.Store.Update(name, func(a *store.Agent) {
			if h.SessionID != "" {
				a.SessionID = h.SessionID
			}
			if h.Socket != "" {
				a.Socket = h.Socket
			}
		})
	case claude.HookTurnEnd:
		s.Store.Update(name, func(a *store.Agent) {
			a.LastStop = now
			a.Failure = h.Failure
			a.Summary = h.Summary
			a.Pending = h.Pending
		})
		kind := store.EventFinished
		if h.Failure != "" {
			kind = store.EventErrored
		}
		s.Store.Append(name, store.Event{Time: now, Kind: kind})
	case claude.HookBlocked:
		s.Store.Append(name, store.Event{Time: now, Kind: store.EventBlocked, Detail: h.Message})
	case claude.HookSessionEnd:
		s.Store.Append(name, store.Event{Time: now, Kind: store.EventEnded})
	}
}
