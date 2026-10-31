package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/proc"
)

const (
	queryLimit        = 30 * time.Second
	spawnLimit        = 90 * time.Second
	subscriptionLogin = "claude.ai"
)

var ErrUnconfirmed = errors.New("start not confirmed")

var efforts = []string{"low", "medium", "high", "xhigh", "max"}

func ValidEffort(effort string) error {
	if effort == "" || slices.Contains(efforts, effort) {
		return nil
	}
	return fmt.Errorf("--effort must be one of %s", strings.Join(efforts, ", "))
}

type Session struct {
	Job        string `json:"id"`
	Name       string `json:"name"`
	SessionID  string `json:"sessionId"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	State      string `json:"state"`
	WaitingFor string `json:"waitingFor"`
	Cwd        string `json:"cwd"`
	PID        int    `json:"pid"`
}

func (s Session) Phase() core.State {
	switch s.Status {
	case "busy":
		return core.StateRunning
	case "idle":
		if s.State == "working" {
			return core.StateRunning
		}
		return core.StateIdle
	case "waiting":
		return core.StateBlocked
	case "":
		switch s.State {
		case "working":
			return core.StateRunning
		case "blocked":
			if s.PID == 0 {
				return core.StateStopped
			}
			return core.StateBlocked
		case "done", "stopped":
			return core.StateStopped
		case "failed":
			return core.StateFailed
		}
	}
	return core.StateUnknown
}

func (s Session) BetweenSteps() bool {
	return s.Status == "idle" && s.State == "working"
}

func (s Session) Source(rec Record) string {
	switch {
	case rec.Entrypoint == "sdk-cli":
		return "remote"
	case s.Kind == "background":
		return "bg"
	}
	return "tui"
}

type Record struct {
	Socket     string `json:"messagingSocketPath"`
	Bridge     string `json:"bridgeSessionId"`
	Entrypoint string `json:"entrypoint"`
}

func (r Record) Link() string {
	if r.Bridge == "" {
		return ""
	}
	return "https://claude.ai/code/" + r.Bridge
}

type Driver struct {
	bin  string
	home string
}

func New(home string) *Driver {
	d := &Driver{bin: "claude", home: home}
	if path, err := exec.LookPath("claude"); err == nil {
		d.bin = path
		return d
	}
	local := filepath.Join(d.localBin(), "claude")
	if _, err := os.Stat(local); err == nil {
		d.bin = local
	}
	return d
}

func (d *Driver) localBin() string {
	return filepath.Join(d.home, ".local", "bin")
}

func (d *Driver) Installed() bool {
	_, err := exec.LookPath(d.bin)
	return err == nil
}

func (d *Driver) command(limit time.Duration, dir string, args []string) proc.Cmd {
	return proc.Cmd{
		Name: d.bin, Args: args, Dir: dir, Limit: limit,
		Env: append(os.Environ(), "PATH="+d.localBin()+":"+os.Getenv("PATH")),
	}
}

func (d *Driver) Version() (string, error) {
	return d.command(queryLimit, "", []string{"--version"}).Output()
}

func (d *Driver) SupportsBackground() error {
	help, err := d.command(queryLimit, "", []string{"--help"}).Output()
	if err != nil {
		return err
	}
	if !strings.Contains(help, "--bg") {
		return errors.New("this claude has no --bg option, so update it")
	}
	_, err = d.List()
	return err
}

func (d *Driver) List() ([]Session, error) {
	out, err := d.command(queryLimit, "", []string{"agents", "--json", "--all"}).Output()
	if err != nil {
		return nil, err
	}
	var list []Session
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&list); err != nil {
		return nil, fmt.Errorf("unexpected claude agents output: %w", err)
	}
	return list, nil
}

func (d *Driver) Record(pid int) (Record, error) {
	var r Record
	data, err := os.ReadFile(filepath.Join(d.home, ".claude", "sessions", strconv.Itoa(pid)+".json"))
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(data, &r)
}

func (d *Driver) Trusted(dir, top string) bool {
	data, err := os.ReadFile(d.globalConfig())
	if err != nil {
		return false
	}
	var config struct {
		Projects map[string]struct {
			Trusted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if json.Unmarshal(data, &config) != nil {
		return false
	}
	for current := dir; ; current = filepath.Dir(current) {
		if config.Projects[current].Trusted {
			return true
		}
		if current == top || current == filepath.Dir(current) {
			return false
		}
	}
}

type Launch struct {
	Name          string
	Bypass        bool
	Model         string
	Effort        string
	Settings      string
	RemoteControl bool
}

func (l Launch) args() []string {
	mode := "auto"
	if l.Bypass {
		mode = "bypassPermissions"
	}
	args := []string{"--name", l.Name, "--permission-mode", mode, "--settings", l.Settings}
	if l.Model != "" {
		args = append(args, "--model", l.Model)
	}
	if l.Effort != "" {
		args = append(args, "--effort", l.Effort)
	}
	if l.RemoteControl {
		args = append(args, "--remote-control")
	}
	return args
}

var (
	bgRe   = regexp.MustCompile(`backgrounded\s+·\s+([0-9a-f]{8})`)
	copyRe = regexp.MustCompile(`copy(?: of that conversation)? as ([0-9a-f]{8})`)
)

func (d *Driver) spawn(dir string, args []string) (job, stderr string, err error) {
	stdout, stderr, err := d.command(spawnLimit, dir, append([]string{"--bg"}, args...)).Run()
	if errors.Is(err, proc.ErrTimeout) {
		return "", stderr, fmt.Errorf("%w: %v", ErrUnconfirmed, err)
	}
	if err != nil {
		return "", stderr, err
	}
	m := bgRe.FindStringSubmatch(stdout)
	if m == nil {
		return "", stderr, fmt.Errorf("%w: unexpected claude --bg output: %s", ErrUnconfirmed, strings.TrimSpace(stdout))
	}
	return m[1], stderr, nil
}

func (d *Driver) Start(dir, prompt string, l Launch) (string, error) {
	job, _, err := d.spawn(dir, append(l.args(), "--", prompt))
	return job, err
}

func (d *Driver) Resume(sessionID, dir, message string) error {
	return d.wake(sessionID, dir, message, nil)
}

func (d *Driver) Respawn(sessionID, dir, message string, l Launch) error {
	return d.wake(sessionID, dir, message, l.args())
}

func (d *Driver) wake(sessionID, dir, message string, flags []string) error {
	args := append([]string{"--resume", sessionID}, flags...)
	_, stderr, err := d.spawn(dir, append(args, "--", message))
	if m := copyRe.FindStringSubmatch(stderr); m != nil {
		return d.discardCopy(m[1], sessionID)
	}
	return err
}

func (d *Driver) discardCopy(job, sessionID string) error {
	if err := d.Remove(job); err != nil {
		return fmt.Errorf("claude started a copy (%s) of %s instead of waking it, and gofer couldn't remove the copy: %w. Run claude rm %s on the dev box", job, sessionID, err, job)
	}
	return fmt.Errorf("claude started a copy of %s instead of waking it, so gofer removed the copy. The session is in use elsewhere and the message wasn't delivered", sessionID)
}

func (d *Driver) Stop(job string) error {
	_, err := d.command(spawnLimit, "", []string{"stop", job}).Output()
	return err
}

func (d *Driver) Remove(job string) error {
	_, err := d.command(spawnLimit, "", []string{"rm", job}).Output()
	return err
}

func Post(socket, text string) error {
	line, err := json.Marshal(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": text},
	})
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("unix", socket, 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Write(append(line, '\n'))
	return err
}

func (d *Driver) CanRemoteControl() bool {
	loggedIn, method := d.LoggedIn()
	return loggedIn && method == subscriptionLogin
}

func (d *Driver) LoggedIn() (bool, string) {
	stdout, _, err := d.command(queryLimit, "", []string{"auth", "status", "--json"}).Run()
	loggedIn, method, ok := ParseAuth(stdout)
	switch {
	case ok && loggedIn:
		return true, method
	case ok:
		return false, "not logged in"
	case err != nil:
		return false, err.Error()
	}
	return false, "couldn't read claude auth status"
}
