package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	Schema       = 1
	deliveryLock = "delivery.lock"
)

type Agent struct {
	Schema    int       `json:"schema"`
	Name      string    `json:"name"`
	RequestID string    `json:"request_id,omitempty"`
	SessionID string    `json:"session_id"`
	Dir       string    `json:"dir"`
	Repo      string    `json:"repo,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	Base      string    `json:"base,omitempty"`
	Bypass    bool      `json:"bypass,omitempty"`
	Model     string    `json:"model,omitempty"`
	Effort    string    `json:"effort,omitempty"`
	Socket    string    `json:"socket,omitempty"`
	Failure   string    `json:"failure,omitempty"`
	Created   time.Time `json:"created"`
	LastInput time.Time `json:"last_input"`
	LastToken string    `json:"last_token,omitempty"`
	LastStop  time.Time `json:"last_stop"`
}

type EventKind string

const (
	EventStarted  EventKind = "started"
	EventFinished EventKind = "finished"
	EventErrored  EventKind = "errored"
	EventBlocked  EventKind = "blocked"
	EventMessage  EventKind = "message"
	EventWoken    EventKind = "woken"
	EventStopped  EventKind = "stopped"
	EventEnded    EventKind = "ended"
)

type Event struct {
	Time   time.Time `json:"time"`
	Kind   EventKind `json:"kind"`
	Detail string    `json:"detail,omitempty"`
}

type Unreadable struct {
	Name string
	Err  error
}

func (u Unreadable) Error() string { return u.Err.Error() }

type Store struct {
	Root string
}

func New(home string) *Store {
	return &Store{Root: filepath.Join(home, ".local", "state", "gofer")}
}

func (s *Store) agentsDir() string { return filepath.Join(s.Root, "agents") }

func (s *Store) dir(name string) string { return filepath.Join(s.agentsDir(), name) }

func (s *Store) Path(name, file string) string { return filepath.Join(s.dir(name), file) }

func (s *Store) Exists(name string) bool {
	_, err := os.Stat(s.dir(name))
	return !os.IsNotExist(err)
}

func (s *Store) Create(a Agent) (release func(), err error) {
	staged := s.dir(".new-" + a.Name)
	if err := os.MkdirAll(staged, 0o700); err != nil {
		return nil, err
	}
	defer os.RemoveAll(staged)
	a.Schema = Schema
	if err := writeRecord(staged, a); err != nil {
		return nil, err
	}
	release, err = lock(filepath.Join(staged, deliveryLock))
	if err != nil {
		return nil, err
	}
	if err := os.Rename(staged, s.dir(a.Name)); err != nil {
		release()
		return nil, err
	}
	syncDir(s.agentsDir())
	return release, nil
}

func (s *Store) Load(name string) (Agent, error) {
	var a Agent
	path := s.Path(name, "agent.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return a, err
	}
	if err != nil {
		return a, Unreadable{name, err}
	}
	if err := json.Unmarshal(data, &a); err != nil {
		return a, Unreadable{name, fmt.Errorf("can't read %s: %w", path, err)}
	}
	if a.Schema > Schema {
		return a, Unreadable{name, fmt.Errorf("can't read %s: it uses state schema %d, newer than this gofer supports", path, a.Schema)}
	}
	return a, nil
}

func writeRecord(dir string, a Agent) error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "agent.json.tmp")
	if err := writeSynced(tmp, data); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "agent.json")); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

func syncDir(path string) {
	if dir, err := os.Open(path); err == nil {
		dir.Sync()
		dir.Close()
	}
}

func lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func (s *Store) LockDelivery(name string) (func(), error) {
	return lock(s.Path(name, deliveryLock))
}

func (s *Store) LockRuns() (func(), error) {
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return nil, err
	}
	return lock(filepath.Join(s.Root, "runs.lock"))
}

func (s *Store) Update(name string, fn func(*Agent)) error {
	unlock, err := lock(s.Path(name, "agent.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	a, err := s.Load(name)
	if err != nil {
		return err
	}
	fn(&a)
	return writeRecord(s.dir(name), a)
}

func (s *Store) List() ([]Agent, []Unreadable, error) {
	entries, err := os.ReadDir(s.agentsDir())
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var agents []Agent
	var unreadable []Unreadable
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		a, err := s.Load(e.Name())
		var bad Unreadable
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case errors.As(err, &bad):
			unreadable = append(unreadable, bad)
		default:
			agents = append(agents, a)
		}
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Created.Before(agents[j].Created) })
	return agents, unreadable, nil
}

func (s *Store) ByRequestID(id string) (Agent, bool, error) {
	agents, unreadable, err := s.List()
	if err != nil {
		return Agent{}, false, err
	}
	for _, a := range agents {
		if a.RequestID == id {
			return a, true, nil
		}
	}
	if len(unreadable) > 0 {
		bad := unreadable[0]
		return Agent{}, false, fmt.Errorf("can't rule out an earlier attempt at this request: %w. Run gofer rm %s --force to drop that record", bad, bad.Name)
	}
	return Agent{}, false, nil
}

func (s *Store) Append(name string, e Event) error {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.Path(name, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func (s *Store) Events(name string) ([]Event, error) {
	f, err := os.Open(s.Path(name, "events.jsonl"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scan.Scan() {
		var e Event
		if json.Unmarshal(scan.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, scan.Err()
}

func (s *Store) Seen(name, client string) int {
	data, err := os.ReadFile(s.seenPath(name, client))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(string(data))
	return n
}

func (s *Store) MarkSeen(name, client string, n int) error {
	return os.WriteFile(s.seenPath(name, client), []byte(strconv.Itoa(n)), 0o600)
}

func (s *Store) seenPath(name, client string) string {
	return s.Path(name, "seen-"+client)
}

func (s *Store) SettingsPath(name string) string {
	return s.Path(name, "settings.json")
}

func (s *Store) WriteSettings(name string, data []byte) error {
	return os.WriteFile(s.SettingsPath(name), data, 0o600)
}

func (s *Store) WriteBrief(name, text string) (string, error) {
	f, err := os.CreateTemp(s.dir(name), "brief-*.md")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func (s *Store) Delete(name string) error {
	doomed := s.dir(".del-" + name)
	if err := os.RemoveAll(doomed); err != nil {
		return err
	}
	err := os.Rename(s.dir(name), doomed)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.RemoveAll(doomed)
}
