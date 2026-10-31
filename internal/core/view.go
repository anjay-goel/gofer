package core

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
	New         bool      `json:"new,omitempty"`
	Settled     bool      `json:"settled"`
	Unconfirmed bool      `json:"unconfirmed,omitempty"`
	Cut         bool      `json:"cut_short,omitempty"`
}

type LogLine struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func Branch(name string) string { return "agent/" + name }

func HandoffRef(requestID string) string { return "refs/gofer/" + requestID }
