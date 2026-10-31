package claude

import (
	"reflect"
	"testing"

	"github.com/anjay-goel/gofer/internal/core"
)

func TestPhase(t *testing.T) {
	cases := []struct {
		sess Session
		want core.State
	}{
		{Session{Status: "busy", State: "working"}, core.StateRunning},
		{Session{Status: "idle", State: "done"}, core.StateIdle},
		{Session{Status: "waiting", State: "blocked"}, core.StateBlocked},
		{Session{State: "working"}, core.StateRunning},
		{Session{State: "blocked", PID: 42}, core.StateBlocked},
		{Session{State: "blocked"}, core.StateStopped},
		{Session{State: "done"}, core.StateStopped},
		{Session{State: "stopped"}, core.StateStopped},
		{Session{State: "failed"}, core.StateFailed},
		{Session{State: "paused"}, core.StateUnknown},
		{Session{Status: "rebooting"}, core.StateUnknown},
		{Session{}, core.StateUnknown},
	}
	for _, tc := range cases {
		if got := tc.sess.Phase(); got != tc.want {
			t.Errorf("%+v: got %s, want %s", tc.sess, got, tc.want)
		}
	}
}

func TestParseEntriesQueuedInput(t *testing.T) {
	cases := []struct {
		line string
		want int
	}{
		{`{"type":"attachment","attachment":{"type":"queued_command","commandMode":"prompt","isMeta":true,"prompt":"hello"}}`, 1},
		{`{"type":"attachment","attachment":{"type":"queued_command","commandMode":"task-notification","prompt":"done"}}`, 0},
	}
	for _, tc := range cases {
		if got := parseEntries([]byte(tc.line)); len(got) != tc.want {
			t.Errorf("%s: got %+v", tc.line, got)
		}
	}
}

func TestParseEntriesCleanText(t *testing.T) {
	cases := []struct {
		line string
		want []core.LogLine
	}{
		{`{"type":"user","message":{"content":"<task-notification>x</task-notification>"}}`, nil},
		{`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls","description":"list"}}]}}`, []core.LogLine{{Role: "tool", Text: "Bash  ls"}}},
		{`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__metabase__execute_sql","input":{"database_id":3,"sql":"SELECT 1"}}]}}`, []core.LogLine{{Role: "tool", Text: "metabase execute_sql  SELECT 1"}}},
		{`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Odd","input":{"n":1}}]}}`, []core.LogLine{{Role: "tool", Text: `Odd  {"n":1}`}}},
	}
	for _, tc := range cases {
		if got := parseEntries([]byte(tc.line)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %+v", tc.line, got)
		}
	}
}

func TestClipKeepsWholeCharacters(t *testing.T) {
	if got := clip("aé", 2); got != "a…" {
		t.Fatalf("got %q", got)
	}
}
