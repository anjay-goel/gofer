package core

import (
	"strings"
	"unicode"
)

const InstallDir = ".local/share/gofer"

type State string

const (
	StateRunning State = "running"
	StateIdle    State = "idle"
	StateBlocked State = "blocked"
	StateStopped State = "stopped"
	StateFailed  State = "failed"
	StateUnknown State = "unknown"
)

func (s State) Ended() bool { return s == StateStopped || s == StateFailed }

type View struct {
	Name        string    `json:"name"`
	ID          string    `json:"id,omitempty"`
	State       State     `json:"state"`
	Detail      string    `json:"detail,omitempty"`
	Source      string    `json:"source"`
	Dir         string    `json:"dir,omitempty"`
	Branch      string    `json:"branch,omitempty"`
	Link        string    `json:"link,omitempty"`
	Work        []LogLine `json:"work,omitempty"`
	Changed     string    `json:"changed,omitempty"`
	Ago         string    `json:"ago,omitempty"`
	Summary     string    `json:"summary,omitempty"`
	Commits     *int      `json:"commits,omitempty"`
	Next        string    `json:"next,omitempty"`
	New         bool      `json:"new,omitempty"`
	Settled     bool      `json:"settled"`
	Unconfirmed bool      `json:"unconfirmed,omitempty"`
	Cut         bool      `json:"cut_short,omitempty"`
}

func (v View) Stuck() bool {
	return v.State == StateBlocked || v.State == StateFailed || v.Cut
}

func (v View) Returned() bool {
	return v.Settled || v.Unconfirmed || v.State == StateBlocked
}

func (v View) NextStep() string {
	switch {
	case v.State == StateBlocked && (v.Source == "gofer" || v.Source == "bg"):
		return "gofer attach " + v.Name
	case v.State == StateFailed, v.Cut:
		return "gofer send " + v.Name + ` "continue"`
	case v.Returned():
		return "gofer logs " + v.Name
	}
	return ""
}

func (v View) Working() bool {
	return v.State == StateRunning || (v.State == StateIdle && !v.Returned())
}

func (v View) Rank() int {
	switch {
	case v.State == StateBlocked:
		return 0
	case v.State == StateFailed, v.Cut:
		return 1
	case v.New && v.Settled:
		return 2
	case v.Working():
		return 3
	}
	return 4
}

func OneLine(text string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == unicode.ReplacementChar:
			return -1
		}
		return r
	}, text)
	return strings.Join(strings.Fields(cleaned), " ")
}

type LogLine struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func Branch(name string) string { return "agent/" + name }

func HandoffRef(requestID string) string { return "refs/gofer/" + requestID }
