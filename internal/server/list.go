package server

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/proc"
	"github.com/anjay-goel/gofer/internal/store"
)

const countLimit = 5 * time.Second

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
		v.Commits = commitCount(a)
		if v.Summary == "" && v.Settled && !v.Stuck() {
			v.Summary = s.Driver.LastReply(a.SessionID)
		}
		events, _ := s.Store.Events(a.Name)
		if n := len(events); n > 0 {
			v.Changed = describe(events[n-1], now)
			v.Ago = ago(events[n-1].Time, now)
			if p.ClientID != "" {
				v.New = n > s.Store.Seen(a.Name, p.ClientID)
				if !p.Peek {
					s.Store.MarkSeen(a.Name, p.ClientID, n)
				}
			}
		}
		views = append(views, v)
	}
	for _, u := range unreadable {
		views = append(views, core.View{
			Name: u.Name, State: core.StateUnknown, Detail: u.Err.Error(), Source: "gofer",
			Next: "gofer rm " + u.Name + " --force",
		})
	}
	foreign := []core.View{}
	for _, sess := range snap.list {
		if known[sess.SessionID] || sess.SessionID == "" || sess.Phase().Ended() {
			continue
		}
		foreign = append(foreign, s.view(foreignTarget(sess), now))
	}
	slices.SortStableFunc(foreign, func(a, b core.View) int { return cmp.Compare(a.Name, b.Name) })
	views = append(views, foreign...)
	slices.SortStableFunc(views, func(a, b core.View) int { return cmp.Compare(a.Rank(), b.Rank()) })
	return views, nil
}

func commitCount(a store.Agent) *int {
	if a.Branch == "" || a.Base == "" {
		return nil
	}
	span := a.Base + "..refs/heads/" + a.Branch
	out, err := proc.Cmd{Name: "git", Args: []string{"rev-list", "--count", span}, Dir: a.Repo, Limit: countLimit}.Output()
	if err != nil {
		return nil
	}
	count, err := strconv.Atoi(out)
	if err != nil {
		return nil
	}
	return &count
}
