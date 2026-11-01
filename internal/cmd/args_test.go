package cmd

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anjay-goel/gofer/internal/config"
	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/driver/claude"
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
	path, err := claude.SkillPath("gofer")
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
		path, err := claude.SkillPath(s.Name)
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
	four := 4
	views := []core.View{
		{Name: "auth", State: core.StateFailed, Detail: strings.Repeat("api_error ", 20), Ago: "1m ago", Source: "gofer", New: true, Next: `gofer send auth "continue"`},
		{Name: "web-session", State: core.StateIdle, Source: "remote", Summary: "Done"},
		{Name: "cut", State: core.StateStopped, CutShort: true, Source: "gofer", Branch: "agent/cut", Commits: &four, Ago: "2h ago", Summary: strings.Repeat("日本語 words ", 40)},
		{Name: "bad", State: core.StateUnknown, Source: "gofer", Next: "gofer rm bad --force"},
	}
	plain := agentTable(views, 0)
	lines := strings.Split(strings.TrimSpace(plain), "\n")
	if want := regexp.MustCompile(`^NAME +STATE +CHANGED +COMMITS +BRANCH +SOURCE +SUMMARY$`); !want.MatchString(lines[0]) {
		t.Fatalf("plain header: %q", lines[0])
	}
	if !strings.Contains(lines[1], `Run gofer send auth "continue"`) || !strings.Contains(lines[3], "stopped (cut short)") || !strings.Contains(lines[3], "4 commits") || !strings.Contains(lines[4], "Run gofer rm bad --force") {
		t.Fatalf("plain table:\n%s", plain)
	}
	if strings.Contains(plain, strings.Repeat("api_error ", 10)) || strings.HasSuffix(lines[2], " ") {
		t.Fatalf("plain table isn't clipped:\n%s", plain)
	}
	if same := agentTable(views[:1], 0); !strings.Contains(same, "BRANCH") {
		t.Fatalf("plain columns changed with the data:\n%s", same)
	}
	styled = true
	defer func() { styled = false }()
	escapes := regexp.MustCompile("\\x1b\\[[0-9]+m")
	for _, width := range []int{200, 80, 50, 30, 12} {
		for _, line := range strings.Split(strings.TrimSpace(escapes.ReplaceAllString(agentTable(views, width), "")), "\n") {
			if got := displayWidth(line); got > width {
				t.Errorf("width %d: %d columns: %q", width, got, line)
			}
		}
	}
}

func TestWaitHintsNameTheNextCommand(t *testing.T) {
	cases := []struct {
		view core.View
		want string
	}{
		{core.View{Name: "a", State: core.StateBlocked, Detail: "permission prompt", Source: "gofer"}, "blocked (permission prompt). Run gofer attach a"},
		{core.View{Name: "a", State: core.StateFailed, Detail: "rate_limit", Source: "gofer", Settled: true}, `failed (rate_limit). Run gofer send a "continue"`},
		{core.View{Name: "a", State: core.StateStopped, CutShort: true, Source: "gofer", Settled: true}, `stopped (cut short). Run gofer send a "continue"`},
		{core.View{Name: "a", State: core.StateIdle, Source: "gofer", Unconfirmed: true}, "idle, but its turn can't be confirmed finished, so this work may be stale. Run gofer logs a"},
		{core.View{Name: "a", State: core.StateIdle, Source: "gofer", Settled: true}, "idle"},
		{core.View{Name: "a", State: core.StateBlocked, Source: "remote"}, "blocked. Run gofer logs a"},
	}
	for _, tc := range cases {
		tc.view.Next = tc.view.NextStep()
		if got := waitState(tc.view).text; got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestAlertsCarryOneCleanLine(t *testing.T) {
	v := core.View{Name: "a\x1b]evil\x07", State: core.StateBlocked, Detail: "needs\nyou\x07", Source: "gofer"}
	v.Next = v.NextStep()
	title, body := alertText(v)
	if strings.ContainsAny(title+body, "\x1b\x07\n") || !strings.Contains(body, "blocked (needs you). Run gofer attach") {
		t.Fatalf("title %q, body %q", title, body)
	}
	cases := []struct {
		goos string
		name string
		last string
	}{
		{"darwin", "osascript", "with \"quotes\""},
		{"linux", "notify-send", "-dash"},
		{"plan9", "", ""},
	}
	for _, tc := range cases {
		name, args := alertCommand(tc.goos, "title", tc.last)
		if name != tc.name || (name != "" && args[len(args)-1] != tc.last) {
			t.Errorf("%s: %s %q", tc.goos, name, args)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	stdout := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	defer func() { os.Stdout = stdout }()
	fn()
	write.Close()
	printed, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	return string(printed)
}

func TestAlertTakesOneRoute(t *testing.T) {
	settled := core.WaitResult{Views: []core.View{{Name: "a", State: core.StateIdle, Settled: true}}}
	cases := []struct {
		name         string
		command      string
		inTerminal   bool
		wantTerminal bool
	}{
		{"the OS command wins", "exit 0", true, false},
		{"a failing OS command falls back", "exit 1", true, true},
		{"no OS command falls back", "", true, true},
		{"JSON output stays clean", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PATH", dir)
			if tc.command != "" {
				name, _ := alertCommand(runtime.GOOS, "", "")
				if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+tc.command+"\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			printed := captureStdout(t, func() { raiseAlerts(settled, tc.inTerminal) })
			if strings.Contains(printed, osc+"9;") != tc.wantTerminal {
				t.Fatalf("printed %q", printed)
			}
		})
	}
}

func TestCompletion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("GOFER_HOST", "")
	saveNames("box", []core.View{{Name: "auth"}, {Name: "api"}, {Name: "has space"}})
	saveNames("user@other:22/x", []core.View{{Name: "web"}})
	cfg := &config.Config{DefaultHost: "box", Hosts: map[string]config.Host{"box": {}, "user@other:22/x": {}}}
	cases := []struct {
		words []string
		want  []string
	}{
		{[]string{"lo"}, []string{"logs"}},
		{[]string{"wait", "--"}, []string{"--help", "--host", "--any", "--notify", "--timeout", "--json"}},
		{[]string{"rm", "--f"}, []string{"--force", "--finished"}},
		{[]string{"logs", ""}, []string{"auth", "api"}},
		{[]string{"logs", "auth", ""}, nil},
		{[]string{"logs", "-n", "5", "a"}, []string{"auth", "api"}},
		{[]string{"logs", "-n", ""}, nil},
		{[]string{"wait", "auth", "a"}, []string{"auth", "api"}},
		{[]string{"send", "auth", "fi"}, nil},
		{[]string{"diff", "--"}, []string{"--help", "--host", "--stat"}},
		{[]string{"diff", "a"}, []string{"auth", "api"}},
		{[]string{"fetch", "a"}, []string{"auth", "api"}},
		{[]string{"cp", "au"}, []string{"auth:"}},
		{[]string{"cp", ""}, nil},
		{[]string{"--host", ""}, []string{"box", "user@other:22/x"}},
		{[]string{"--host", "user@other:22/x", "stop", ""}, []string{"web"}},
		{[]string{"run", "api", "--name", ""}, nil},
		{[]string{"completion", "z"}, []string{"zsh"}},
	}
	for _, tc := range cases {
		if got := candidates(cfg, tc.words); !slices.Equal(got, tc.want) {
			t.Errorf("%q: got %q, want %q", tc.words, got, tc.want)
		}
	}
}

func TestSendOnlyDoubtsDeliveryWhenItMayHaveLanded(t *testing.T) {
	cases := []struct {
		name    string
		failure string
		want    int
	}{
		{"dropped connection", "connection reset", exitUnconfirmed},
		{"refused login", "Permission denied (publickey)", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\necho '" + tc.failure + "' >&2\nexit 255\n"
			if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
			a := &app{host: "box", client: &transport.Client{Host: "box", Version: "v"}}
			err := a.send([]string{"auth", "hi"})
			got := 1
			var code exitCode
			if errors.As(err, &code) {
				got = code.code
			}
			if got != tc.want {
				t.Fatalf("exit %d: %v", got, err)
			}
		})
	}
}

func TestRemoveFinishedSkipsWhatItMustKeep(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
req=$(cat)
case "$req" in
*'"method":"ls"'*)
  echo 'GOFER-RPC {"result":[
' | tr -d '\n'
  echo '{"name":"clean","state":"idle","source":"gofer","settled":true},{"name":"dirty","state":"idle","source":"gofer","settled":true},{"name":"bad","state":"failed","source":"gofer","settled":true},{"name":"cut","state":"stopped","source":"gofer","settled":true,"cut_short":true},{"name":"busy","state":"running","source":"gofer"},{"name":"other","state":"idle","source":"bg","settled":true}]}'
  ;;
*'"name":"dirty"'*)
  echo 'GOFER-RPC {"error":"dirty has uncommitted changes in /w. Commit them, or use --force"}'
  ;;
*)
  echo "$req" >> "$FAKE_DIR/removed"
  echo 'GOFER-RPC {"result":{}}'
  ;;
esac
`
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755))
	t.Setenv("FAKE_DIR", dir)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	a := &app{host: "box", cfg: &config.Config{}, client: &transport.Client{Host: "box", Version: "v"}}
	printed := captureStdout(t, func() { must(a.rm([]string{"--finished"})) })
	for _, want := range []string{"Removed clean", "Skipped: dirty has uncommitted", "Skipped bad: failed", "Skipped cut: stopped (cut short)"} {
		if !strings.Contains(printed, want) {
			t.Errorf("missing %q in:\n%s", want, printed)
		}
	}
	removed, err := os.ReadFile(filepath.Join(dir, "removed"))
	must(err)
	if strings.Count(string(removed), `"method"`) != 1 || !strings.Contains(string(removed), `"name":"clean"`) {
		t.Fatalf("removed = %s", removed)
	}
	if err := a.rm([]string{"--finished", "--force"}); err == nil {
		t.Fatal("--finished took --force")
	}
}

type fakeBox struct {
	dir string
	a   *app
}

func newFakeBox(t *testing.T) fakeBox {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
for last; do :; done
case "$last" in
*' rpc'*) cat >/dev/null; cat "$FAKE_DIR/reply" ;;
*) exec sh -c "$last" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_DIR", dir)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	return fakeBox{dir, &app{host: "box", cfg: &config.Config{}, client: &transport.Client{Host: "box", Version: "v"}}}
}

func (b fakeBox) reply(t *testing.T, r core.ResolveResult) {
	t.Helper()
	data, err := json.Marshal(core.Response{Result: mustJSON(t, r)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.dir, "reply"), append([]byte(core.RPCPrefix), append(data, '\n')...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func gitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestDiff(t *testing.T) {
	box := newFakeBox(t)
	repo := filepath.Join(box.dir, "it's a repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitAt(t, repo, "init", "-q")
	write(".gitignore", "ignored\n")
	write("kept.txt", "one\n")
	gitAt(t, repo, "add", ".")
	gitAt(t, repo, "commit", "-q", "-m", "start")
	base := gitAt(t, repo, "rev-parse", "HEAD")
	write("committed.txt", "two\n")
	gitAt(t, repo, "add", ".")
	gitAt(t, repo, "commit", "-q", "-m", "work")
	write("kept.txt", "changed\n")
	write("fresh.txt", "x\n")
	write("ignored", "x\n")
	box.reply(t, core.ResolveResult{Dir: repo, Base: base})
	printed := captureStdout(t, func() {
		if err := box.a.diff([]string{"auth"}); err != nil {
			t.Error(err)
		}
	})
	for _, want := range []string{"+two", "+changed", "Untracked:\n  fresh.txt\n"} {
		if !strings.Contains(printed, want) {
			t.Errorf("missing %q in:\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "\x1b") || strings.Contains(printed, "  ignored") {
		t.Errorf("colour or ignored files in:\n%s", printed)
	}
	stat := captureStdout(t, func() {
		if err := box.a.diff([]string{"auth", "--stat"}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(stat, "2 files changed") || !strings.Contains(stat, "fresh.txt") {
		t.Errorf("stat:\n%s", stat)
	}
	paged := filepath.Join(box.dir, "paged")
	t.Setenv("PAGER", "cat > "+transport.Quote(paged))
	if err := box.a.page(diffScript(repo, base, false, true)); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(paged); !strings.Contains(string(data), "\x1b[") || !strings.Contains(string(data), "fresh.txt") {
		t.Errorf("paged:\n%s", data)
	}
	t.Setenv("PAGER", "true")
	if err := box.a.page(diffScript(repo, base, false, true)); err != nil {
		t.Errorf("quitting the pager early: %v", err)
	}
	if err := box.a.client.Stream(diffScript("/nonexistent", base, false, false), io.Discard); err == nil {
		t.Error("a failed diff reported nothing")
	}
	box.reply(t, core.ResolveResult{Dir: "/w", GitError: "/w isn't a git repo"})
	if err := box.a.diff([]string{"auth"}); err == nil || err.Error() != "/w isn't a git repo" {
		t.Errorf("error = %v", err)
	}
}

func TestFetch(t *testing.T) {
	box := newFakeBox(t)
	remote := filepath.Join(box.dir, "box")
	gitAt(t, box.dir, "init", "-q", "-b", "main", remote)
	gitAt(t, remote, "commit", "-q", "--allow-empty", "-m", "start")
	gitAt(t, remote, "switch", "-q", "-c", "agent/auth")
	local := filepath.Join(box.dir, "laptop")
	gitAt(t, box.dir, "clone", "-q", remote, local)
	gitAt(t, local, "switch", "-q", "main")
	gitAt(t, local, "remote", "remove", "origin")
	gitAt(t, local, "branch", "-q", "-D", "agent/auth")
	commit := func(dir string) string {
		t.Helper()
		gitAt(t, dir, "commit", "-q", "--allow-empty", "-m", "work in "+dir)
		return gitAt(t, dir, "rev-parse", "HEAD")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(box.dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := box.a.fetch([]string{"auth"}); err == nil || !strings.Contains(err.Error(), "git repo") {
		t.Fatalf("outside a repo: %v", err)
	}
	if err := os.Chdir(local); err != nil {
		t.Fatal(err)
	}
	box.reply(t, core.ResolveResult{Dir: remote, Name: "auth", Branch: "agent/auth", Dirty: true})
	fetch := func() (string, error) {
		var err error
		printed := captureStdout(t, func() { err = box.a.fetch([]string{"auth"}) })
		return printed, err
	}
	wantPrinted := func(want ...string) {
		t.Helper()
		printed, err := fetch()
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range want {
			if !strings.Contains(printed, text) {
				t.Errorf("missing %q in:\n%s", text, printed)
			}
		}
	}
	wantPrinted("agent/auth is up to date at", "uncommitted work")
	first := commit(remote)
	wantPrinted("agent/auth at "+first[:6]+", 1 commit\n", "uncommitted work")
	if !reachable(first) {
		t.Error("a fetched commit isn't reachable")
	}
	wantPrinted("agent/auth is up to date at " + first[:6])
	gitAt(t, local, "switch", "-q", "agent/auth")
	commit(remote)
	head := commit(remote)
	wantPrinted("agent/auth at " + head[:6] + ", 2 commits")
	if got := gitAt(t, local, "rev-parse", "HEAD"); got != head {
		t.Fatalf("checked out branch at %s, want %s", got, head)
	}
	gitAt(t, local, "switch", "-q", "main")
	mine := commit(gitAt(t, local, "rev-parse", "--show-toplevel"))
	gitAt(t, local, "update-ref", "refs/heads/agent/auth", mine)
	theirs := commit(remote)
	if _, err := fetch(); err == nil || !strings.Contains(err.Error(), "diverged") {
		t.Fatalf("diverged: %v", err)
	}
	if got := gitAt(t, local, "rev-parse", "agent/auth"); got != mine {
		t.Fatalf("diverged branch moved to %s", got)
	}
	if reachable(theirs) {
		t.Error("a commit only in FETCH_HEAD counts as fetched")
	}
	gitAt(t, remote, "switch", "-q", "--orphan", "other")
	commit(remote)
	box.reply(t, core.ResolveResult{Dir: remote, Name: "auth", Branch: "other"})
	if _, err := fetch(); err == nil || !strings.Contains(err.Error(), "no history") {
		t.Fatalf("unrelated: %v", err)
	}
	box.reply(t, core.ResolveResult{Dir: remote, Name: "auth"})
	if _, err := fetch(); err == nil || !strings.Contains(err.Error(), "isn't on a branch") {
		t.Fatalf("detached: %v", err)
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

func TestWorkFooter(t *testing.T) {
	four := 4
	cases := []struct {
		view core.View
		want string
	}{
		{core.View{Name: "a"}, ""},
		{core.View{Name: "a", ToolCalls: 1}, "1 tool call. gofer logs a has the rest."},
		{core.View{Name: "a", ToolCalls: 23, Commits: &four}, "4 commits, 23 tool calls. gofer logs a has the rest."},
	}
	for _, tc := range cases {
		if got := workFooter(tc.view); got != tc.want {
			t.Errorf("%+v: got %q", tc.view, got)
		}
	}
}
