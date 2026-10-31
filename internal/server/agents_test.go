package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/store"
)

func TestWaitReportsUnfinishedTurns(t *testing.T) {
	idle := `[{"sessionId":"%s","status":"idle"}]`
	endTurn := `{"type":"assistant","message":{"stop_reason":"end_turn"}}`
	old := -2 * turnGrace
	cases := []struct {
		name            string
		agents          string
		hooks           []string
		inputAge        time.Duration
		token           string
		transcript      string
		wantState       core.State
		wantDetail      string
		wantFailed      bool
		wantUnfinished  bool
		wantUnconfirmed bool
	}{
		{name: "finished", agents: idle, hooks: []string{`{"hook_event_name":"Stop"}`}, wantState: core.StateIdle},
		{name: "api error", agents: idle, hooks: []string{`{"hook_event_name":"StopFailure","error":"rate_limit"}`}, wantState: core.StateFailed, wantDetail: "rate_limit", wantFailed: true},
		{name: "later turn clears the error", agents: idle, hooks: []string{`{"hook_event_name":"StopFailure","error":"rate_limit"}`, `{"hook_event_name":"Stop"}`}, wantState: core.StateIdle},
		{name: "job failed", agents: `[{"id":"aaaaaaaa","sessionId":"%s","state":"failed"}]`, wantState: core.StateFailed, wantFailed: true},
		{name: "killed mid turn", agents: `[]`, wantState: core.StateStopped, wantFailed: true},
		{name: "stopped after a turn whose stop hook was lost", agents: `[]`, inputAge: old, transcript: endTurn, wantState: core.StateStopped},
		{name: "blocked shows the notification", agents: `[{"sessionId":"%s","status":"waiting","waitingFor":"permission prompt"}]`, hooks: []string{`{"hook_event_name":"Notification","message":"Claude needs your permission to use Bash"}`}, wantState: core.StateBlocked, wantDetail: "Claude needs your permission to use Bash", wantUnfinished: true},
		{name: "idle before the turn starts", agents: idle, wantState: core.StateIdle, wantUnfinished: true},
		{name: "lost stop hook after the turn ended", agents: idle, inputAge: old, transcript: endTurn, wantState: core.StateIdle},
		{name: "lost stop hook with no sign of the turn ending", agents: idle, inputAge: old, wantState: core.StateIdle, wantUnconfirmed: true},
		{name: "only an earlier turn ended", agents: idle, inputAge: old, token: "gofer-0123456789", transcript: endTurn, wantState: core.StateIdle, wantUnconfirmed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.agents)
			inputAge := tc.inputAge
			if inputAge == 0 {
				inputAge = -time.Second
			}
			f.create(t, store.Agent{Name: "a", SessionID: sessionID, Dir: f.home, LastInput: time.Now().UTC().Add(inputAge), LastToken: tc.token})
			f.write(t, ".claude/projects/p/"+sessionID+".jsonl", tc.transcript)
			for _, hook := range tc.hooks {
				f.srv.Hook("a", strings.NewReader(hook))
			}
			r, err := f.srv.wait(core.WaitParams{Names: []string{"a"}, Timeout: 1})
			must(t, err)
			v := r.Views[0]
			if r.Finished == tc.wantUnfinished || r.Failed != tc.wantFailed || r.Unconfirmed != tc.wantUnconfirmed || v.State != tc.wantState || v.Detail != tc.wantDetail {
				t.Fatalf("result = %+v", r)
			}
		})
	}
}

func TestWaitOutlastsAFailedAgentList(t *testing.T) {
	f := newFixture(t, `not json`)
	f.create(t, store.Agent{Name: "a", SessionID: sessionID, Dir: f.home})
	r, err := f.srv.wait(core.WaitParams{Names: []string{"a"}, Timeout: 1})
	must(t, err)
	if r.Done() || !strings.Contains(r.Trouble, "unexpected claude agents output") {
		t.Fatalf("result = %+v", r)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
	return strings.TrimSpace(string(out))
}

func gitRepo(t *testing.T, dir string) string {
	t.Helper()
	must(t, os.MkdirAll(dir, 0o755))
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	return gitIn(t, dir, "rev-parse", "HEAD")
}

func TestRunOutcomes(t *testing.T) {
	startWindow, linkWindow = time.Second, time.Second
	cases := []struct {
		name         string
		agents       string
		bgOut        string
		bgCode       string
		wantErr      string
		wantRecord   bool
		wantWorktree bool
		wantSession  string
	}{
		{name: "claude refused", agents: `[]`, bgCode: "1", wantErr: "exited"},
		{name: "unreadable output with no session", agents: `[]`, bgOut: "something else", bgCode: "0", wantErr: "record is kept", wantRecord: true, wantWorktree: true},
		{name: "never listed", agents: `not json`, bgOut: "backgrounded · cccccccc · x", bgCode: "0", wantErr: "record is kept", wantRecord: true, wantWorktree: true},
		{name: "listed under its job id", agents: `[{"id":"cccccccc","sessionId":"%s","state":"working"}]`, bgOut: "backgrounded · cccccccc · x", bgCode: "0", wantRecord: true, wantWorktree: true, wantSession: sessionID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.agents)
			f.write(t, "bg.out", tc.bgOut)
			f.write(t, "bg.code", tc.bgCode)
			repo := filepath.Join(f.home, "code", "api")
			head := gitRepo(t, repo)
			f.trust(t, repo)
			gitIn(t, repo, "update-ref", core.HandoffRef("r"), head)
			_, err := f.srv.run(core.RunParams{RequestID: "r", Name: "job", Repo: "api", Root: "code", Prompt: "p", Base: head})
			if tc.wantErr == "" {
				must(t, err)
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
			agent, loadErr := f.srv.Store.Load("job")
			if (loadErr == nil) != tc.wantRecord {
				t.Fatalf("record: %v, want %v", loadErr, tc.wantRecord)
			}
			if agent.SessionID != tc.wantSession {
				t.Fatalf("session = %q", agent.SessionID)
			}
			_, statErr := os.Stat(worktreePath(repo, "job"))
			if (statErr == nil) != tc.wantWorktree {
				t.Fatalf("worktree: %v, want %v", statErr, tc.wantWorktree)
			}
			if got := gitIn(t, repo, "branch", "--list", "agent/job"); (got != "") != tc.wantWorktree {
				t.Fatalf("branch = %q, want it only with the worktree", got)
			}
			if got := gitIn(t, repo, "for-each-ref", "refs/gofer"); got != "" {
				t.Fatalf("handoff ref left behind: %s", got)
			}
		})
	}
}

func TestFailedWorktreeAddLeavesNothing(t *testing.T) {
	cases := []struct {
		name string
		hook string
	}{
		{"a hook failed after git created the worktree", "exit 1"},
		{"git was killed with the worktree still locked", `touch "$(git rev-parse --git-dir)/locked"; exit 1`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, `[]`)
			repo := filepath.Join(f.home, "api")
			head := gitRepo(t, repo)
			f.trust(t, repo)
			hook := filepath.Join(gitIn(t, repo, "rev-parse", "--absolute-git-dir"), "hooks", "post-checkout")
			must(t, os.MkdirAll(filepath.Dir(hook), 0o755))
			must(t, os.WriteFile(hook, []byte("#!/bin/sh\n"+tc.hook+"\n"), 0o755))
			if _, err := f.srv.run(core.RunParams{RequestID: "r", Name: "job", Repo: repo, Prompt: "p", Base: head}); err == nil {
				t.Fatal("the run succeeded")
			}
			if f.srv.Store.Exists("job") || pathExists(worktreePath(repo, "job")) || gitIn(t, repo, "branch", "--list", "agent/job") != "" {
				t.Fatal("the failed run left something behind")
			}
		})
	}
}

func TestRefusedBeforeProvisioning(t *testing.T) {
	cases := []struct {
		name      string
		untrusted bool
		branch    string
		agent     string
		folder    bool
		wantErr   string
	}{
		{name: "untrusted repo", untrusted: true, wantErr: "Run claude there"},
		{name: "existing branch", branch: "agent/job", wantErr: "branch agent/job already exists"},
		{name: "existing agent", agent: "job", wantErr: "agent named job"},
		{name: "existing worktree folder", folder: true, wantErr: "pick another --name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, `[]`)
			repo := filepath.Join(f.home, "code", "api")
			head := gitRepo(t, repo)
			if !tc.untrusted {
				f.trust(t, repo)
			}
			if tc.branch != "" {
				gitIn(t, repo, "branch", tc.branch)
			}
			if tc.agent != "" {
				f.create(t, store.Agent{Name: tc.agent, Dir: f.home})
			}
			if tc.folder {
				must(t, os.MkdirAll(worktreePath(repo, "job"), 0o755))
			}
			check := func(err error) {
				t.Helper()
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				if tc.untrusted && !strings.Contains(err.Error(), repo) {
					t.Fatalf("error %q doesn't name %s", err, repo)
				}
			}
			_, err := f.srv.prepare(core.PrepareParams{Name: "job", Repo: repo, Root: "/elsewhere"})
			check(err)
			_, err = f.srv.run(core.RunParams{RequestID: "r", Name: "job", Repo: repo, Root: "/elsewhere", Prompt: "p", Base: head})
			check(err)
			if strings.Contains(f.calls(t), "--bg") {
				t.Fatalf("claude was started: %s", f.calls(t))
			}
			if tc.branch != "" && gitIn(t, repo, "rev-parse", tc.branch) != head {
				t.Fatal("the existing branch moved")
			}
			if tc.branch == "" && gitIn(t, repo, "branch", "--list", "agent/job") != "" {
				t.Fatal("a branch was created")
			}
			if pathExists(worktreePath(repo, "job")) != tc.folder {
				t.Fatal("the worktree folder changed")
			}
		})
	}
}

func TestTrustsFoldersUnderTheRoot(t *testing.T) {
	f := newFixture(t, `[]`)
	root := filepath.Join(f.home, "code")
	repo := filepath.Join(root, "api")
	gitRepo(t, repo)
	linked := filepath.Join(root, "api-linked")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "other", linked)
	outside := filepath.Join(f.home, "other")
	must(t, os.MkdirAll(outside, 0o755))
	cases := []struct {
		dir  string
		want bool
	}{
		{outside, false},
		{f.home, false},
		{linked, true},
		{repo, true},
	}
	for _, tc := range cases {
		err := f.srv.checkDir(tc.dir, root, false)
		if (err == nil) != tc.want || f.srv.trusted(tc.dir) != tc.want {
			t.Errorf("%s: err = %v", tc.dir, err)
		}
	}
}

func TestTrusted(t *testing.T) {
	f := newFixture(t, `[]`)
	repo := filepath.Join(f.home, "repo")
	gitRepo(t, repo)
	inside := filepath.Join(repo, "sub")
	must(t, os.Mkdir(inside, 0o755))
	plain := filepath.Join(f.home, "plain", "deep")
	must(t, os.MkdirAll(plain, 0o755))
	link := filepath.Join(f.home, "link")
	must(t, os.Symlink(repo, link))
	linked := filepath.Join(f.home, "linked")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "linked", linked)
	cases := []struct {
		name    string
		granted string
		dir     string
		want    bool
	}{
		{"the repo itself", repo, repo, true},
		{"a folder inside the repo", repo, inside, true},
		{"a parent of a repo", f.home, repo, false},
		{"a parent of a plain folder", f.home, plain, true},
		{"through a symlink", repo, link, true},
		{"a linked worktree of the repo", repo, linked, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.trust(t, tc.granted)
			if got := f.srv.trusted(tc.dir); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}

func TestRepeatedRunReturnsTheSameAgent(t *testing.T) {
	startWindow, linkWindow = time.Second, time.Millisecond
	f := newFixture(t, `[{"id":"cccccccc","sessionId":"%s","state":"working"}]`)
	f.write(t, "bg.out", "backgrounded · cccccccc · x")
	dir := filepath.Join(f.home, "work")
	must(t, os.Mkdir(dir, 0o755))
	f.trust(t, dir)
	params := core.RunParams{RequestID: "r", Repo: dir, Prompt: "fix the tests"}
	views := make([]core.View, 3)
	var group sync.WaitGroup
	for i := range views {
		group.Add(1)
		go func() {
			defer group.Done()
			view, err := f.srv.run(params)
			if err != nil {
				t.Error(err)
			}
			views[i] = view
		}()
	}
	group.Wait()
	for _, view := range views {
		if view.Name != "fix-the-tests" || view.ID != sessionID {
			t.Fatalf("views = %+v", views)
		}
	}
	if got := strings.Count(f.calls(t), "--bg"); got != 1 {
		t.Fatalf("claude started %d times", got)
	}
	events, err := f.srv.Store.Events("fix-the-tests")
	must(t, err)
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
}

func TestLargePromptReachesTheAgentThroughAFile(t *testing.T) {
	startWindow, linkWindow = time.Second, time.Millisecond
	f := newFixture(t, `[{"id":"cccccccc","sessionId":"%s","state":"working"}]`)
	f.write(t, "bg.out", "backgrounded · cccccccc · x")
	dir := filepath.Join(f.home, "work")
	must(t, os.Mkdir(dir, 0o755))
	f.trust(t, dir)
	doc := strings.Repeat("x", argLimit)
	_, err := f.srv.run(core.RunParams{RequestID: "r", Name: "job", Repo: dir, Prompt: "fix it", Doc: doc})
	must(t, err)
	if len(f.calls(t)) > argLimit {
		t.Fatalf("claude was passed %d bytes", len(f.calls(t)))
	}
	pattern := f.srv.Store.Path("job", "brief-*.md")
	briefs, err := filepath.Glob(pattern)
	must(t, err)
	if len(briefs) != 1 || !strings.Contains(f.calls(t), briefs[0]) {
		t.Fatalf("briefs = %v, calls = %q", briefs, f.calls(t))
	}
	brief, err := os.ReadFile(briefs[0])
	must(t, err)
	if !strings.Contains(string(brief), "fix it") || !strings.Contains(string(brief), doc) {
		t.Fatal("the brief is missing the prompt or the document")
	}
	_, err = f.srv.remove(core.RemoveParams{Name: "job"})
	must(t, err)
	if left, _ := filepath.Glob(pattern); len(left) != 0 {
		t.Fatalf("briefs left behind: %v", left)
	}
}

func TestUnreadableAgentIsReported(t *testing.T) {
	f := newFixture(t, `[]`)
	f.create(t, store.Agent{Name: "bad", Dir: f.home})
	must(t, os.WriteFile(f.srv.Store.Path("bad", "agent.json"), []byte("{"), 0o600))
	views, err := f.srv.list(core.ListParams{})
	must(t, err)
	if len(views) != 1 || views[0].Name != "bad" || views[0].State != core.StateUnknown || !strings.Contains(views[0].Detail, "agent.json") {
		t.Fatalf("views = %+v", views)
	}
	dir := filepath.Join(f.home, "work")
	must(t, os.Mkdir(dir, 0o755))
	f.trust(t, dir)
	if _, err := f.srv.run(core.RunParams{RequestID: "r", Name: "job", Repo: dir, Prompt: "p"}); err == nil || !strings.Contains(err.Error(), "gofer rm bad --force") {
		t.Fatalf("error = %v", err)
	}
	if _, err := f.srv.remove(core.RemoveParams{Name: "bad"}); err == nil || !strings.Contains(err.Error(), "agent.json") {
		t.Fatalf("error = %v", err)
	}
	r, err := f.srv.remove(core.RemoveParams{Name: "bad", Force: true})
	must(t, err)
	if r.Note == "" || f.srv.Store.Exists("bad") {
		t.Fatalf("result = %+v", r)
	}
}

func TestRunReportsALinkThatAppearsLate(t *testing.T) {
	startWindow, linkWindow = 5*time.Second, 5*time.Second
	f := newFixture(t, `[{"id":"cccccccc","sessionId":"%s","state":"working"}]`)
	f.write(t, "bg.out", "backgrounded · cccccccc · x")
	dir := filepath.Join(f.home, "work")
	must(t, os.Mkdir(dir, 0o755))
	f.trust(t, dir)
	must(t, os.MkdirAll(filepath.Join(f.home, ".claude", "sessions"), 0o755))
	time.AfterFunc(700*time.Millisecond, func() {
		f.write(t, ".claude/sessions/42.json", `{"bridgeSessionId":"session_x"}`)
		f.write(t, "agents.json", fmt.Sprintf(`[{"id":"cccccccc","sessionId":"%s","status":"busy","pid":42}]`, sessionID))
	})
	view, err := f.srv.run(core.RunParams{RequestID: "r", Name: "job", Repo: dir, Prompt: "p"})
	must(t, err)
	if view.Link != "https://claude.ai/code/session_x" {
		t.Fatalf("link = %q", view.Link)
	}
}

func TestStop(t *testing.T) {
	cases := []struct {
		name        string
		agents      string
		wantAlready bool
		wantCall    string
	}{
		{name: "running", agents: `[{"id":"cccccccc","sessionId":"%s","status":"busy"}]`, wantCall: "stop cccccccc"},
		{name: "no longer listed", agents: `[]`, wantAlready: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.agents)
			f.create(t, store.Agent{Name: "a", SessionID: sessionID, Dir: f.home})
			r, err := f.srv.stop(core.NameParams{Name: "a"})
			must(t, err)
			if r.AlreadyStopped != tc.wantAlready {
				t.Fatalf("result = %+v", r)
			}
			stopped := strings.Contains(f.calls(t), "stop ")
			if stopped != !tc.wantAlready || !strings.Contains(f.calls(t), tc.wantCall) {
				t.Fatalf("calls = %q", f.calls(t))
			}
			events, err := f.srv.Store.Events("a")
			must(t, err)
			if (len(events) == 1) == tc.wantAlready {
				t.Fatalf("events = %+v", events)
			}
		})
	}
}

func TestWaitReturnsWorkSinceTheLastInput(t *testing.T) {
	tag := "[gofer-0123456789]"
	assistant := func(text string) string {
		return fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"text","text":%q}]}}`, text)
	}
	tool := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`
	notice := `{"type":"user","message":{"role":"user","content":"the tools reconnected"}}`
	echoed := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"old %s"}]}}`, tag)
	quoted := assistant("noted " + tag)
	want := []core.LogLine{
		{Role: "assistant", Text: "the real reply"},
		{Role: "tool", Text: "Bash  ls"},
		{Role: "assistant", Text: "noted " + tag},
		{Role: "assistant", Text: "tools are back"},
	}
	cases := []struct {
		name  string
		input string
	}{
		{"resumed as a user row", fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"look again\n\n%s"}}`, tag)},
		{"queued mid turn", fmt.Sprintf(`{"type":"attachment","attachment":{"type":"queued_command","prompt":"look again\n\n%s"}}`, tag)},
		{"enqueued", fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue","content":"look again\n\n%s"}`, tag)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, `[{"sessionId":"%s","status":"idle"}]`)
			f.create(t, store.Agent{Name: "a", SessionID: sessionID, Dir: f.home})
			f.srv.Hook("a", strings.NewReader(`{"hook_event_name":"Stop"}`))
			rows := []string{assistant("the first turn"), tc.input, assistant("the real reply"), tool, echoed, quoted, notice, assistant("tools are back")}
			f.write(t, ".claude/projects/p/"+sessionID+".jsonl", strings.Join(rows, "\n"))
			r, err := f.srv.wait(core.WaitParams{Names: []string{"a"}, Timeout: 1})
			must(t, err)
			if !reflect.DeepEqual(r.Views[0].Work, want) {
				t.Fatalf("work = %+v", r.Views[0].Work)
			}
		})
	}
}

func TestTrimWork(t *testing.T) {
	big := strings.Repeat("x", workLimit/2)
	tool := core.LogLine{Role: "tool", Text: big}
	reply := core.LogLine{Role: "assistant", Text: "the reply"}
	final := core.LogLine{Role: "assistant", Text: big + big + big}
	note := func(n int) core.LogLine {
		return core.LogLine{Role: "gofer", Text: fmt.Sprintf("%d earlier entries left out, see gofer logs", n)}
	}
	cases := []struct {
		name string
		in   []core.LogLine
		want []core.LogLine
	}{
		{"within the limit", []core.LogLine{tool, reply}, []core.LogLine{tool, reply}},
		{"tools go first, oldest first", []core.LogLine{tool, reply, tool, reply}, []core.LogLine{note(1), reply, tool, reply}},
		{"the newest entry always stays", []core.LogLine{tool, reply, final}, []core.LogLine{note(2), final}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := trimWork(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %d lines, want %d", len(got), len(tc.want))
			}
		})
	}
}

func TestRemove(t *testing.T) {
	cases := []struct {
		name       string
		address    string
		agents     string
		repo       string
		force      bool
		wantErr    string
		wantRecord bool
		wantCall   string
		wantNoCall string
	}{
		{name: "uses the job id for a session id", address: sessionID[:8], agents: `[{"id":"cccccccc","sessionId":"%s","state":"done"}]`, wantCall: "rm cccccccc"},
		{name: "missing repo refuses before touching the session", agents: `[{"id":"cccccccc","sessionId":"%s","state":"done"}]`, repo: "/nonexistent/repo", wantErr: "--force", wantRecord: true, wantNoCall: "rm "},
		{name: "missing repo goes with force", agents: `[]`, repo: "/nonexistent/repo", force: true},
		{name: "force keeps a good record when claude can't answer", agents: `not json`, force: true, wantErr: "unexpected claude agents output", wantRecord: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.agents)
			agent := store.Agent{Name: "a", SessionID: sessionID, Dir: f.home}
			if tc.repo != "" {
				agent.Repo, agent.Branch, agent.Dir = tc.repo, "agent/a", filepath.Join(tc.repo, "wt")
			}
			f.create(t, agent)
			address := tc.address
			if address == "" {
				address = "a"
			}
			_, err := f.srv.remove(core.RemoveParams{Name: address, Force: tc.force})
			if tc.wantErr == "" {
				must(t, err)
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
			if _, err := f.srv.Store.Load("a"); (err == nil) != tc.wantRecord {
				t.Fatalf("record: %v, want %v", err, tc.wantRecord)
			}
			if !strings.Contains(f.calls(t), tc.wantCall) {
				t.Fatalf("calls = %q, want %q", f.calls(t), tc.wantCall)
			}
			if tc.wantNoCall != "" && strings.Contains(f.calls(t), tc.wantNoCall) {
				t.Fatalf("calls = %q, want no %q", f.calls(t), tc.wantNoCall)
			}
		})
	}
}

func TestRemoveWorktreeGitWouldRefuse(t *testing.T) {
	lock := func(t *testing.T, f fixture, dir string) { gitIn(t, dir, "worktree", "lock", dir) }
	submodule := func(t *testing.T, f fixture, dir string) {
		lib := filepath.Join(f.home, "lib")
		gitRepo(t, lib)
		gitIn(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "lib")
		gitIn(t, dir, "commit", "-q", "-m", "add lib")
	}
	cases := []struct {
		name    string
		setup   func(*testing.T, fixture, string)
		force   bool
		wantErr string
	}{
		{name: "a lock holds, and the session survives", setup: lock, wantErr: "couldn't remove the worktree"},
		{name: "force overrides a lock", setup: lock, force: true},
		{name: "a clean tree with submodules needs no force", setup: submodule},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, `[{"id":"cccccccc","sessionId":"%s","state":"done"}]`)
			repo := filepath.Join(f.home, "api")
			base := gitRepo(t, repo)
			agent := store.Agent{Name: "a", SessionID: sessionID, Repo: repo, Branch: "agent/a", Base: base, Dir: worktreePath(repo, "a")}
			f.create(t, agent)
			must(t, addWorktree(agent))
			tc.setup(t, f, agent.Dir)
			head := gitIn(t, agent.Dir, "rev-parse", "HEAD")
			_, err := f.srv.remove(core.RemoveParams{Name: "a", Force: tc.force, FetchedHead: head})
			if tc.wantErr == "" {
				must(t, err)
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
			failed := tc.wantErr != ""
			if pathExists(agent.Dir) != failed || f.srv.Store.Exists("a") != failed || strings.Contains(f.calls(t), "rm cccccccc") == failed {
				t.Fatalf("calls = %q, worktree or record in the wrong state", f.calls(t))
			}
		})
	}
}

func TestRemoveWaitsForADeliveryInProgress(t *testing.T) {
	f := newFixture(t, `[]`)
	f.create(t, store.Agent{Name: "a", Dir: f.home})
	unlock, err := f.srv.Store.LockDelivery("a")
	must(t, err)
	done := make(chan error)
	go func() {
		_, err := f.srv.remove(core.RemoveParams{Name: "a"})
		done <- err
	}()
	select {
	case <-done:
		t.Fatal("remove didn't wait for the delivery lock")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	must(t, <-done)
}

func TestEmptyNameMatchesNoSession(t *testing.T) {
	f := newFixture(t, `[{"id":"cccccccc","sessionId":"%s","status":"busy"}]`)
	if _, err := f.srv.stop(core.NameParams{}); err == nil || strings.Contains(f.calls(t), "stop ") {
		t.Fatalf("error = %v, calls = %q", err, f.calls(t))
	}
}

func TestExcludeWorktrees(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", worktreesDir + "\n"},
		{"no final newline", "build", "build\n" + worktreesDir + "\n"},
		{"already there", worktreesDir + "\nbuild\n", worktreesDir + "\nbuild\n"},
		{"glued to another pattern", "build" + worktreesDir + "\n", "build" + worktreesDir + "\n" + worktreesDir + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			gitRepo(t, repo)
			exclude := filepath.Join(repo, ".git", "info", "exclude")
			must(t, os.WriteFile(exclude, []byte(tc.in), 0o644))
			must(t, excludeWorktrees(repo))
			data, err := os.ReadFile(exclude)
			must(t, err)
			if string(data) != tc.want {
				t.Fatalf("exclude = %q", data)
			}
		})
	}
}

func TestRCInstall(t *testing.T) {
	f := newFixture(t, `[]`)
	systemdRuntime = f.home
	fake := "#!/bin/sh\necho \"systemctl $*\" >> \"$FAKE_DIR/calls.log\"\n"
	must(t, os.WriteFile(filepath.Join(f.home, ".local", "bin", "systemctl"), []byte(fake), 0o755))
	if _, err := f.srv.rcInstall(core.RCParams{Name: "dev box", Dir: f.home}); err == nil {
		t.Fatal("a name with a space was accepted")
	}
	if _, err := f.srv.rcInstall(core.RCParams{Name: "box", Dir: f.home}); err == nil {
		t.Fatal("the home folder was accepted")
	}
	dir := filepath.Join(f.home, "code")
	must(t, os.MkdirAll(dir, 0o755))
	steps := []struct {
		name string
		want string
	}{
		{"box", "systemctl --user restart claude-rc.service"},
		{"box", "systemctl --user start claude-rc.service"},
		{"box2", "systemctl --user restart claude-rc.service"},
	}
	for _, step := range steps {
		f.write(t, "calls.log", "")
		_, err := f.srv.rcInstall(core.RCParams{Name: step.name, Dir: dir})
		must(t, err)
		if !f.srv.trusted(dir) {
			t.Fatal("the service folder isn't trusted")
		}
		if !strings.Contains(f.calls(t), step.want) {
			t.Fatalf("%s: calls = %q", step.want, f.calls(t))
		}
	}
}

func TestTooOldGit(t *testing.T) {
	cases := map[string]bool{
		"git version 2.16.6":                 true,
		"git version 2.17.0":                 false,
		"git version 2.39.5 (Apple Git-154)": false,
		"something else":                     false,
	}
	for version, want := range cases {
		if got := tooOldGit(version); got != want {
			t.Errorf("%q: got %v", version, got)
		}
	}
}

func TestRemoveProtectsUnfetchedWork(t *testing.T) {
	f := newFixture(t, `[]`)
	repo := filepath.Join(f.home, "api")
	base := gitRepo(t, repo)
	agent := store.Agent{Name: "a", SessionID: sessionID, Repo: repo, Branch: "agent/a", Base: base, Dir: worktreePath(repo, "a")}
	f.create(t, agent)
	must(t, addWorktree(agent))
	must(t, os.WriteFile(filepath.Join(agent.Dir, "new.txt"), []byte("x"), 0o644))
	if _, err := f.srv.remove(core.RemoveParams{Name: "a"}); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("error = %v", err)
	}
	gitIn(t, agent.Dir, "add", ".")
	gitIn(t, agent.Dir, "commit", "-q", "-m", "work")
	head := gitIn(t, agent.Dir, "rev-parse", "HEAD")
	r, err := f.srv.remove(core.RemoveParams{Name: "a"})
	must(t, err)
	if r.Unfetched != head {
		t.Fatalf("result = %+v, want %s", r, head)
	}
	if _, err := f.srv.Store.Load("a"); err != nil {
		t.Fatalf("the record went with the check: %v", err)
	}
	r, err = f.srv.remove(core.RemoveParams{Name: "a", FetchedHead: head})
	must(t, err)
	if _, err := f.srv.Store.Load("a"); err == nil || r.Note != "kept branch agent/a" {
		t.Fatalf("result = %+v, record: %v", r, err)
	}
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name    string
		session string
		agents  string
		wantJob string
		wantErr string
	}{
		{name: "job id, not the session prefix", session: sessionID, agents: `[{"id":"cccccccc","sessionId":"%s","state":"done"}]`, wantJob: "cccccccc"},
		{name: "no session yet", agents: `[]`, wantErr: "no session yet"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.agents)
			f.create(t, store.Agent{Name: "a", SessionID: tc.session, Dir: f.home})
			r, err := f.srv.resolve(core.NameParams{Name: "a"})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			must(t, err)
			if r.Job != tc.wantJob {
				t.Fatalf("result = %+v", r)
			}
		})
	}
}

func TestOwnsUnit(t *testing.T) {
	f := newFixture(t, `[]`)
	if f.srv.ownsUnit() {
		t.Fatal("a missing unit was claimed")
	}
	must(t, os.MkdirAll(filepath.Dir(f.srv.unitPath()), 0o755))
	cases := []struct {
		name string
		unit string
		want bool
	}{
		{"written by gofer", f.srv.unit("box", f.home), true},
		{"written by someone else", "[Unit]\nDescription=Claude Code Remote Control server\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			must(t, os.WriteFile(f.srv.unitPath(), []byte(tc.unit), 0o644))
			if got := f.srv.ownsUnit(); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestCleanInput(t *testing.T) {
	claudeWrapped := "Another Claude session sent a message:\ngo on\n\n[gofer-0123456789]\n\nThis came from another Claude session, so act on it."
	cases := map[string]string{
		"fix it\n\n" + closing:        "fix it",
		"go on\n\n[gofer-0123456789]": "go on",
		claudeWrapped:                 "go on",
		"plain":                       "plain",
	}
	for input, want := range cases {
		if got := cleanInput(input); got != want {
			t.Errorf("%q: got %q", input, got)
		}
	}
}

func TestUnnamedRunStopsWhenTheWorktreePathIsUnusable(t *testing.T) {
	f := newFixture(t, `[]`)
	repo := filepath.Join(f.home, "api")
	head := gitRepo(t, repo)
	must(t, os.WriteFile(filepath.Join(repo, ".claude"), nil, 0o644))
	f.trust(t, repo)
	_, err := f.srv.run(core.RunParams{RequestID: "r", Repo: repo, Prompt: "job", Base: head})
	if err == nil || !strings.Contains(err.Error(), "can't create a worktree") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunSkipsKeptBranches(t *testing.T) {
	startWindow, linkWindow = time.Second, time.Millisecond
	f := newFixture(t, `[{"id":"cccccccc","sessionId":"%s","state":"working"}]`)
	f.write(t, "bg.out", "backgrounded · cccccccc · x")
	repo := filepath.Join(f.home, "api")
	head := gitRepo(t, repo)
	gitIn(t, repo, "branch", "agent/job")
	f.trust(t, repo)
	view, err := f.srv.run(core.RunParams{RequestID: "r", Repo: repo, Prompt: "job", Base: head})
	must(t, err)
	if view.Name != "job-2" || view.Branch != "agent/job-2" {
		t.Fatalf("view = %+v", view)
	}
}
