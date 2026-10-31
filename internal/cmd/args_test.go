package cmd

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/transport"
	"github.com/anjay-goel/gofer/skill"
)

func TestExtractHost(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
		host string
	}{
		{"before the command", []string{"--host", "b", "ls"}, []string{"ls"}, "b"},
		{"after flags", []string{"ls", "--host=b", "--peek"}, []string{"ls", "--peek"}, "b"},
		{"before a message", []string{"send", "--host", "b", "a", "hi"}, []string{"send", "a", "hi"}, "b"},
		{"inside a message", []string{"send", "a", "use", "--host", "box"}, []string{"send", "a", "use", "--host", "box"}, ""},
		{"after a double dash", []string{"run", "api", "--", "--host", "x"}, []string{"run", "api", "--", "--host", "x"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, host := extractHost(tc.args)
			if !reflect.DeepEqual(got, tc.want) || host != tc.host {
				t.Fatalf("got %q, %q", got, host)
			}
		})
	}
}

func TestParseInterspersed(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		want     []string
		wantName string
		wantHelp bool
	}{
		{name: "flags between words", args: []string{"api", "--name", "x", "fix"}, want: []string{"api", "fix"}, wantName: "x"},
		{name: "double dash ends flags", args: []string{"api", "--", "-x", "--name", "y"}, want: []string{"api", "-x", "--name", "y"}},
		{name: "help", args: []string{"--help"}, wantHelp: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			name := fs.String("name", "", "")
			got, err := parseInterspersed(fs, tc.args)
			var code exitCode
			if tc.wantHelp {
				if !errors.As(err, &code) || code.code != 0 {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) || *name != tc.wantName {
				t.Fatalf("got %q, name %q, err %v", got, *name, err)
			}
		})
	}
}

func TestCopyPaths(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"", "/w/a/"},
		{"out.txt", "/w/a/out.txt"},
		{"out/", "/w/a/out/"},
		{"/etc/hosts", "/etc/hosts"},
		{"~/x", "~/x"},
	}
	for _, tc := range cases {
		if got := inAgentDir("/w/a", tc.file); got != tc.want {
			t.Errorf("%q: got %q", tc.file, got)
		}
	}
	if got := localPath("2026-10-31T12:00.log"); got != "./2026-10-31T12:00.log" {
		t.Errorf("relative local path: %q", got)
	}
	if got := localPath("/tmp/a:b"); got != "/tmp/a:b" {
		t.Errorf("absolute local path: %q", got)
	}
}

func TestMessageWords(t *testing.T) {
	cases := []struct {
		args    []string
		want    string
		wantErr bool
	}{
		{args: []string{"fix", "it"}, want: "fix it"},
		{args: []string{"--", "-x", "fails"}, want: "-x fails"},
		{args: []string{"--", "--host", "box"}, want: "--host box"},
		{args: []string{" "}, wantErr: true},
		{args: []string{"--"}, wantErr: true},
	}
	for _, tc := range cases {
		got, err := messageWords(tc.args)
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("%q: got %q, %v", tc.args, got, err)
		}
	}
}

func TestReadDocRefusesWhatCantBeAPrompt(t *testing.T) {
	cases := []struct {
		name    string
		data    string
		wantErr bool
	}{
		{"text", "plan é\n", false},
		{"nul byte", "a\x00b", true},
		{"invalid utf-8", "\xff\xfea", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "doc")
			if err := os.WriteFile(path, []byte(tc.data), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := readDoc(path)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), path) {
					t.Fatalf("err = %v", err)
				}
			} else if err != nil || got != tc.data {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestLocalGitKeepsStderrOutOfValues(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init", "-q")
	if _, err := localGit("-C", dir, "rev-parse", "--verify", "HEAD"); err == nil || !strings.Contains(err.Error(), "fatal") {
		t.Fatalf("err = %v, want git's own message", err)
	}
	run("commit", "-q", "--allow-empty", "-m", "base")
	run("update-ref", "refs/heads/HEAD", "HEAD")
	head, err := localGit("-C", dir, "rev-parse", "HEAD")
	if err != nil || len(head) != 40 {
		t.Fatalf("head = %q, err = %v", head, err)
	}
}

func TestRCName(t *testing.T) {
	cases := map[string]string{
		"box.tail1234.ts.net": "box",
		"you@Box":             "box",
		"10.0.0.5":            "10.0.0.5",
		"fd7a:115c::1":        "fd7a-115c--1",
	}
	for host, want := range cases {
		if got := rcName(host); got != want {
			t.Errorf("%q: got %q", host, got)
		}
	}
}

func TestStartRetriesWithTheSameRequest(t *testing.T) {
	runRetryDelay = time.Millisecond
	dir := t.TempDir()
	script := `#!/bin/sh
cat >> "$FAKE_DIR/requests"
echo >> "$FAKE_DIR/requests"
if [ "$(wc -l < "$FAKE_DIR/requests")" -lt 3 ]; then
  echo "connection reset" >&2
  exit 255
fi
echo 'GOFER-RPC {"result":{"name":"job"}}'
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_DIR", dir)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	a := &app{client: &transport.Client{Host: "box", Version: "v"}}
	view, err := a.start(core.RunParams{RequestID: core.NewRequestID(), Name: "job"})
	if err != nil || view.Name != "job" {
		t.Fatalf("view = %+v, err = %v", view, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "requests"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 || lines[0] != lines[1] || lines[1] != lines[2] {
		t.Fatalf("requests = %q", lines)
	}
}

func TestSyncSkillReplacesAnOldCopy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := skillPath("gofer")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syncSkill(); err != nil {
		t.Fatal(err)
	}
	for _, s := range skill.All {
		path, err := skillPath(s.Name)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != s.Text {
			t.Fatalf("%s = %q, err = %v", s.Name, data, err)
		}
	}
}

func TestAgentTable(t *testing.T) {
	views := []core.View{
		{Name: "auth", State: core.StateFailed, Detail: "api_error", Changed: "turn failed, 1m ago", Source: "gofer", New: true},
		{Name: "web-session", State: core.StateIdle, Source: "remote"},
	}
	plain := "NAME         STATE               CHANGED              SOURCE\n" +
		"auth*        failed (api_error)  turn failed, 1m ago  gofer\n" +
		"web-session  idle                                     remote\n"
	if got := agentTable(views); got != plain {
		t.Fatalf("plain table:\n%s", got)
	}
	styled = true
	defer func() { styled = false }()
	escapes := regexp.MustCompile("\\x1b\\[[0-9]+m")
	lines := strings.Split(escapes.ReplaceAllString(agentTable(views), ""), "\n")
	column := utf8.RuneCountInString(lines[0][:strings.Index(lines[0], "SOURCE")])
	for _, line := range lines[1:3] {
		if got := utf8.RuneCountInString(line[:strings.LastIndex(line, " ")+1]); got != column {
			t.Fatalf("source column at %d, want %d: %q", got, column, line)
		}
	}
}

func TestParseKey(t *testing.T) {
	cases := map[string]string{
		"\x1b[A": "up",
		"\x1bOB": "down",
		"j":      "down",
		"\r":     "enter",
		"\x03":   "cancel",
		"Y":      "y",
	}
	for input, want := range cases {
		if got := parseKey([]byte(input)); got != want {
			t.Errorf("%q: got %q", input, got)
		}
	}
	if step(0, 3, -1) != 2 || step(2, 3, 1) != 0 {
		t.Fatal("the list doesn't wrap")
	}
}

func TestWantsHelp(t *testing.T) {
	cases := []struct {
		command string
		args    []string
		want    bool
	}{
		{"attach", []string{"--help"}, true},
		{"attach", []string{"auth", "-h"}, true},
		{"send", []string{"--help"}, true},
		{"send", []string{"auth", "what", "does", "--help", "do"}, false},
		{"run", []string{"api", "--", "--help"}, false},
	}
	for _, tc := range cases {
		if got := wantsHelp(tc.command, tc.args); got != tc.want {
			t.Errorf("%s %v: got %v", tc.command, tc.args, got)
		}
	}
	for name := range commandHelp {
		if !strings.HasPrefix(commandHelp[name], "Usage: gofer "+name) {
			t.Errorf("%s help doesn't start with its usage", name)
		}
	}
}

func TestDevNullIsNotATerminal(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if isTerminal(null) {
		t.Fatal("/dev/null counted as a terminal")
	}
}
