package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/anjay-goel/gofer/internal/core"
)

func TestPhase(t *testing.T) {
	cases := []struct {
		sess Session
		want core.State
	}{
		{Session{Status: "busy", State: "working"}, core.StateRunning},
		{Session{Status: "idle", State: "done"}, core.StateIdle},
		{Session{Status: "idle", State: "blocked"}, core.StateIdle},
		{Session{Status: "idle", State: "working"}, core.StateRunning},
		{Session{Status: "busy", State: "done"}, core.StateRunning},
		{Session{Status: "idle"}, core.StateIdle},
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

func TestParseEntries(t *testing.T) {
	cases := []struct {
		line string
		want []core.LogLine
	}{
		{`{"type":"attachment","attachment":{"type":"queued_command","commandMode":"prompt","isMeta":true,"prompt":"hello"}}`, []core.LogLine{{Role: "user", Text: "hello"}}},
		{`{"type":"attachment","attachment":{"type":"queued_command","commandMode":"task-notification","prompt":"done"}}`, nil},
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

func TestConfigWritesKeepOtherFields(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude.json")
	original := `{"numStartups":12345678901234567890,"note":"a<b","projects":{"/old":{"allowedTools":["x"],"hasTrustDialogAccepted":true},"/repo":{"mcpServers":{"a":1}}}}`
	if err := os.WriteFile(config, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	d := New(home)
	if err := d.Trust("/repo"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"12345678901234567890", "a<b", `"allowedTools"`, `"mcpServers"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("lost %s:\n%s", want, data)
		}
	}
	if !d.Trusted("/repo", "/repo") || !d.Trusted("/old", "/old") {
		t.Fatalf("trust is wrong:\n%s", data)
	}
	if err := d.Forget("/old", ""); err != nil {
		t.Fatal(err)
	}
	if d.Trusted("/old", "/old") || !d.Trusted("/repo", "/repo") {
		t.Fatal("forget changed the wrong folder")
	}
	if info, _ := os.Stat(config); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", info.Mode())
	}
	if d.BypassAccepted() {
		t.Fatal("bypass accepted too early")
	}
	if err := d.AcceptBypass(); err != nil {
		t.Fatal(err)
	}
	if !d.BypassAccepted() {
		t.Fatal("bypass not recorded")
	}
	if entries, _ := os.ReadDir(home); len(entries) != 2 {
		t.Fatalf("left files behind: %v", entries)
	}
}

func TestLaunchArgs(t *testing.T) {
	args := Launch{Name: "a", Settings: "s", RemoteControl: true}.args()
	want := []string{"--name", "a", "--permission-mode", "auto", "--settings", "s", "--remote-control"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v", args)
	}
}

func TestHookSettingsDenyDialogs(t *testing.T) {
	for _, bypass := range []bool{false, true} {
		data, err := HookSettings("hook", bypass)
		if err != nil {
			t.Fatal(err)
		}
		var settings struct {
			Permissions struct {
				Deny []string `json:"deny"`
			} `json:"permissions"`
			InputNeeded bool `json:"inputNeededNotifEnabled"`
			AgentPush   bool `json:"agentPushNotifEnabled"`
		}
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(settings.Permissions.Deny, []string{"AskUserQuestion", "EnterPlanMode"}) {
			t.Fatalf("bypass %v: deny = %v", bypass, settings.Permissions.Deny)
		}
		if !settings.InputNeeded || !settings.AgentPush {
			t.Fatalf("bypass %v: push settings missing: %s", bypass, data)
		}
	}
}

func TestSummaryPicksAUsefulFirstLine(t *testing.T) {
	long := "a" + strings.Repeat("é", summaryLimit)
	cases := []struct {
		reply string
		want  string
	}{
		{"Fixed the race\n\nDetails below", "Fixed the race"},
		{"## Summary\n\n**Done.** One test skipped", "Done. One test skipped"},
		{"# Only a heading", "Only a heading"},
		{"\n---\n- bullet first\n", "bullet first"},
		{"```\ncode\n```", "code"},
		{"bad\x1b[31m text\x07", "bad[31m text"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := summary(tc.reply); got != tc.want {
			t.Errorf("%q: got %q", tc.reply, got)
		}
	}
	if got := summary(long); len(got) > summaryLimit+len("…") || !utf8.ValidString(got) {
		t.Fatalf("not capped: %d bytes", len(got))
	}
}

func TestTranscriptReadsPickUpWhereTheyStopped(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s.jsonl")
	row := func(role, text string) string {
		return fmt.Sprintf(`{"type":%q,"message":{"content":[{"type":"text","text":%q}]}}`, role, text)
	}
	first := row("assistant", "one") + "\n"
	partial := row("assistant", "two")
	if err := os.WriteFile(path, []byte(first+partial[:20]), 0o644); err != nil {
		t.Fatal(err)
	}
	d := New(home)
	lines, cursor, err := d.TranscriptFrom("s", 0)
	if err != nil || len(lines) != 1 || cursor != int64(len(first)) {
		t.Fatalf("lines = %+v, cursor = %d, err = %v", lines, cursor, err)
	}
	if err := os.WriteFile(path, []byte(first+partial+"\n"+row("user", "more")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, next, err := d.TranscriptFrom("s", cursor)
	if err != nil || len(lines) != 2 || lines[0].Text != "two" {
		t.Fatalf("lines = %+v, err = %v", lines, err)
	}
	if again, _, _ := d.TranscriptFrom("s", cursor); !reflect.DeepEqual(again, lines) {
		t.Fatalf("a retry saw %+v", again)
	}
	if lines, same, _ := d.TranscriptFrom("s", next); len(lines) != 0 || same != next {
		t.Fatalf("lines = %+v, cursor = %d", lines, same)
	}
	if got := d.LastReply("s"); got != "" {
		t.Fatalf("a reply older than the last input came back: %q", got)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 2*replyTail)+"\n"+first), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := d.LastReply("s"); got != "one" {
		t.Fatalf("got %q", got)
	}
}
