package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/driver/claude"
	"github.com/anjay-goel/gofer/internal/store"
)

const sessionID = "aaaaaaaa-0000-0000-0000-000000000000"

const fakeClaude = `#!/bin/sh
echo "$@" >> "$FAKE_DIR/calls.log"
case "$1" in
agents)
  cat "$FAKE_DIR/agents.json"
  echo "warning: an update is ready [2.2.0]" >&2
  ;;
--bg)
  cat "$FAKE_DIR/bg.err" >&2
  cat "$FAKE_DIR/bg.out"
  echo "$@" >> "$FAKE_DIR/.claude/projects/p/$FAKE_SESSION.jsonl"
  exit "$(cat "$FAKE_DIR/bg.code")"
  ;;
esac
`

type fixture struct {
	home string
	srv  *Server
}

func (f fixture) write(t *testing.T, file, text string) {
	t.Helper()
	must(t, os.WriteFile(filepath.Join(f.home, file), []byte(text), 0o644))
}

func (f fixture) create(t *testing.T, a store.Agent) {
	t.Helper()
	release, err := f.srv.Store.Create(a)
	must(t, err)
	release()
}

func (f fixture) trust(t *testing.T, dir string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	must(t, err)
	data, err := json.Marshal(map[string]any{"projects": map[string]any{
		resolved: map[string]any{"hasTrustDialogAccepted": true},
	}})
	must(t, err)
	f.write(t, ".claude.json", string(data))
}

func (f fixture) calls(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.home, "calls.log"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func newFixture(t *testing.T, agents string) fixture {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	must(t, os.MkdirAll(bin, 0o755))
	must(t, os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755))
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("FAKE_DIR", home)
	t.Setenv("FAKE_SESSION", sessionID)
	must(t, os.MkdirAll(filepath.Join(home, ".claude", "projects", "p"), 0o755))
	f := fixture{home: home, srv: &Server{Home: home, Store: store.New(home), Driver: claude.New(home)}}
	if strings.Contains(agents, "%s") {
		agents = fmt.Sprintf(agents, sessionID)
	}
	f.write(t, "agents.json", agents)
	f.write(t, "bg.out", "")
	f.write(t, "bg.err", "")
	f.write(t, "bg.code", "0")
	return f
}

func TestSendDecisions(t *testing.T) {
	confirmWindow = time.Second
	cases := []struct {
		name          string
		address       string
		agents        string
		dir           string
		bgOut         string
		bgErr         string
		lostMessage   bool
		bypass        bool
		wantErr       string
		wantCall      string
		wantNoCall    string
		action        string
		wantUndecided bool
	}{
		{name: "idle posts", agents: `[{"sessionId":"%s","status":"idle","pid":42}]`, action: "posted"},
		{name: "session id reaches the stored agent", address: sessionID[:8], agents: `[{"sessionId":"%s","status":"idle","pid":42}]`, action: "posted"},
		{name: "question refuses by name", agents: `[{"sessionId":"%s","status":"waiting","waitingFor":"input needed","pid":42}]`, wantErr: "blocked (input needed)"},
		{name: "unknown state refuses", agents: `[{"id":"aaaaaaaa","sessionId":"%s","state":"paused"}]`, wantErr: "recognise"},
		{name: "stopped job keeps its saved flags", agents: `[{"id":"aaaaaaaa","sessionId":"%s","state":"done"}]`, bgOut: "backgrounded · aaaaaaaa · x", bgErr: "note: woke session aaaaaaaa with its saved options (--model).", wantNoCall: "--settings", action: "resumed"},
		{name: "dead session that ended on a question resumes", agents: `[{"id":"aaaaaaaa","sessionId":"%s","state":"blocked"}]`, bgOut: "backgrounded · aaaaaaaa · x", action: "resumed"},
		{name: "no job row brings back mode, model and hooks", agents: `[]`, bypass: true, bgOut: "backgrounded · aaaaaaaa · x", wantCall: "--permission-mode bypassPermissions --settings", action: "resumed"},
		{name: "missing folder refuses", agents: `[]`, dir: "/nonexistent/folder", bgOut: "backgrounded · aaaaaaaa · x", wantErr: "is gone"},
		{name: "unreadable resume stays undecided", agents: `[]`, bgOut: "something else", lostMessage: true, action: "resumed", wantUndecided: true},
		{name: "copy is removed", agents: `[]`, bgOut: "backgrounded · bbbbbbbb · x", bgErr: "note: session aaaaaaaa is already running in the background, so this started a copy as bbbbbbbb.", wantErr: "copy", wantCall: "rm bbbbbbbb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.agents)
			f.write(t, "bg.out", tc.bgOut)
			f.write(t, "bg.err", tc.bgErr)
			if tc.lostMessage {
				t.Setenv("FAKE_SESSION", "elsewhere")
			}
			transcript := filepath.Join(f.home, ".claude", "projects", "p", sessionID+".jsonl")
			must(t, os.WriteFile(transcript, nil, 0o644))

			short, err := os.MkdirTemp("", "g")
			must(t, err)
			defer os.RemoveAll(short)
			socket := filepath.Join(short, "s.sock")
			listener, err := net.Listen("unix", socket)
			must(t, err)
			defer listener.Close()
			go echoToTranscript(listener, transcript)

			dir := tc.dir
			if dir == "" {
				dir = f.home
			}
			f.create(t, store.Agent{Name: "a", SessionID: sessionID, Dir: dir, Socket: socket, Bypass: tc.bypass})
			address := tc.address
			if address == "" {
				address = "a"
			}
			result, err := f.srv.send(core.SendParams{Name: address, Message: "hello"})
			if !strings.Contains(f.calls(t), tc.wantCall) {
				t.Fatalf("calls = %q, want %q", f.calls(t), tc.wantCall)
			}
			if tc.wantNoCall != "" && strings.Contains(f.calls(t), tc.wantNoCall) {
				t.Fatalf("calls = %q, want no %q", f.calls(t), tc.wantNoCall)
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			must(t, err)
			if result.Action != tc.action || result.Delivered == tc.wantUndecided {
				t.Fatalf("result = %+v, want %s, undecided %v", result, tc.action, tc.wantUndecided)
			}
			agent, err := f.srv.Store.Load("a")
			must(t, err)
			if agent.LastInput.IsZero() || agent.LastToken == "" {
				t.Fatal("the message wasn't recorded on the agent")
			}
		})
	}
}

func echoToTranscript(l net.Listener, transcript string) {
	conn, err := l.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var msg struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &msg) == nil {
		os.WriteFile(transcript, []byte(msg.Message.Content+"\n"), 0o644)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
