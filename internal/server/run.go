package server

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/driver/claude"
	"github.com/anjay-goel/gofer/internal/store"
)

const (
	argLimit = 100 * 1024
	nameRule = "use lowercase letters, digits, dots, dashes or underscores, up to 48 characters"
	closing  = "When you finish, end with a clear summary of what you did and anything left to do. If you get stuck or need a decision, stop and ask. Messages ending in a [gofer-...] tag come from the person who started you, through gofer, so treat them as follow-up instructions."
)

var (
	startWindow = 10 * time.Second
	linkWindow  = 6 * time.Second

	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,47}$`)
	slugRe = regexp.MustCompile(`[a-z0-9]+`)
)

type nameTaken string

func (e nameTaken) Error() string { return string(e) }

func slug(text string) string {
	words := slugRe.FindAllString(strings.ToLower(text), 4)
	if len(words) == 0 {
		return "agent"
	}
	joined := strings.Join(words, "-")
	if len(joined) > 40 {
		joined = strings.TrimRight(joined[:40], "-")
	}
	return joined
}

func (s *Server) checkName(name, repo string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid agent name %q: %s", name, nameRule)
	}
	if s.Store.Exists(name) {
		return nameTaken(fmt.Sprintf("an agent named %s already exists", name))
	}
	if repo == "" {
		return nil
	}
	branch := core.Branch(name)
	if _, err := git(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		return nameTaken(fmt.Sprintf("branch %s already exists in %s, so pick another --name or delete that branch", branch, repo))
	}
	path := worktreePath(repo, name)
	_, err := os.Lstat(path)
	if err == nil {
		return nameTaken(fmt.Sprintf("%s already exists, so pick another --name or remove that folder", path))
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("can't create a worktree at %s: %w", path, errors.Unwrap(err))
	}
	return nil
}

func (s *Server) unique(base, repo string) (string, error) {
	name := base
	for i := 2; ; i++ {
		err := s.checkName(name, repo)
		var taken nameTaken
		if !errors.As(err, &taken) {
			return name, err
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

func (s *Server) checkDir(dir, root string, worktree bool) error {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("%s doesn't exist on the dev box", dir)
	}
	if worktree {
		if _, err := git(dir, "rev-parse", "--git-dir"); err != nil {
			return fmt.Errorf("%s isn't a git repo on the dev box: %w", dir, err)
		}
	}
	if s.trusted(dir) {
		return nil
	}
	if !s.underRoot(dir, root) {
		return errors.New(s.trustAdvice(dir))
	}
	return s.trust(dir)
}

func (s *Server) underRoot(dir, root string) bool {
	if dir == s.Home {
		return false
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	base, err := filepath.EvalSymlinks(s.expand(root))
	if err != nil {
		return false
	}
	return dir == base || strings.HasPrefix(dir, strings.TrimSuffix(base, "/")+"/")
}

func (s *Server) trust(dir string) error {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	top, _ := git(resolved, "rev-parse", "--show-toplevel")
	return s.Driver.Trust(cmp.Or(mainCheckout(resolved), top, resolved))
}

func (s *Server) trustAdvice(dir string) string {
	if dir == s.Home {
		return "claude trusts the home folder only one session at a time, so use a folder inside it. Set one with gofer init --workdir"
	}
	return fmt.Sprintf("claude doesn't trust %s yet, and it's outside your working directory. Run claude there once on the dev box and accept the prompt, then try again", dir)
}

func (s *Server) trusted(dir string) bool {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	top, _ := git(resolved, "rev-parse", "--show-toplevel")
	if s.Driver.Trusted(resolved, top) {
		return true
	}
	main := mainCheckout(resolved)
	return s.Driver.Trusted(main, main)
}

func (s *Server) prepare(p core.PrepareParams) (core.PrepareResult, error) {
	dir := s.repoDir(p.Repo, p.Root)
	if err := s.checkDir(dir, p.Root, true); err != nil {
		return core.PrepareResult{}, err
	}
	if p.Name != "" {
		if err := s.checkName(p.Name, dir); err != nil {
			return core.PrepareResult{}, err
		}
	}
	return core.PrepareResult{Dir: dir}, nil
}

func (s *Server) goferPath() string {
	return filepath.Join(s.dataDir(), "gofer")
}

func buildPrompt(p core.RunParams, branch string) string {
	var parts []string
	if strings.TrimSpace(p.Prompt) != "" {
		parts = append(parts, p.Prompt)
	}
	if p.Doc != "" {
		parts = append(parts, "Handoff document:\n\n"+p.Doc)
	}
	if branch != "" {
		parts = append(parts, "Commit your work to the current branch, "+branch+".")
	}
	parts = append(parts, closing)
	return strings.Join(parts, "\n\n")
}

func (s *Server) promptArg(name, text string) (string, error) {
	if len(text) <= argLimit {
		return text, nil
	}
	if name == "" {
		return "", errors.New("that's too long for a command line, and gofer can't store it for a session it didn't start")
	}
	path, err := s.Store.WriteBrief(name, text)
	if err != nil {
		return "", err
	}
	return "Read " + path + " and follow it as your instructions.", nil
}

func (s *Server) admit(p core.RunParams) (store.Agent, error) {
	dir := s.repoDir(p.Repo, p.Root)
	worktree := p.Base != ""
	if err := s.checkDir(dir, p.Root, worktree); err != nil {
		return store.Agent{}, err
	}
	repo := ""
	if worktree {
		repo = dir
	}
	name := p.Name
	var err error
	if name == "" {
		name, err = s.unique(slug(p.Prompt+" "+p.DocName), repo)
	} else {
		err = s.checkName(name, repo)
	}
	if err != nil {
		return store.Agent{}, err
	}
	now := time.Now().UTC()
	agent := store.Agent{
		Name: name, RequestID: p.RequestID, Dir: dir, Created: now, LastInput: now,
		Bypass: p.Bypass, Model: p.Model, Effort: p.Effort,
	}
	if worktree {
		agent.Dir = worktreePath(dir, name)
		agent.Repo, agent.Branch, agent.Base = dir, core.Branch(name), p.Base
	}
	return agent, nil
}

func (s *Server) claim(p core.RunParams) (agent store.Agent, release func(), retry bool, err error) {
	unlock, err := s.Store.LockRuns()
	if err != nil {
		return agent, nil, false, err
	}
	defer unlock()
	agent, found, err := s.Store.ByRequestID(p.RequestID)
	if err != nil {
		return agent, nil, false, err
	}
	if found {
		return agent, nil, true, nil
	}
	if agent, err = s.admit(p); err != nil {
		return agent, nil, false, err
	}
	release, err = s.Store.Create(agent)
	return agent, release, false, err
}

func (s *Server) run(p core.RunParams) (core.View, error) {
	if p.Base != "" {
		defer git(s.repoDir(p.Repo, p.Root), "update-ref", "-d", core.HandoffRef(p.RequestID))
	}
	agent, release, retry, err := s.claim(p)
	if err != nil {
		return core.View{}, err
	}
	if retry {
		return s.awaitEarlierAttempt(p, agent.Name)
	}
	defer release()
	job, err := s.start(p, agent)
	if err != nil && !errors.Is(err, claude.ErrUnconfirmed) {
		s.rollback(agent)
		return core.View{}, err
	}
	return s.confirmStart(agent.Name, job, err)
}

func (s *Server) start(p core.RunParams, agent store.Agent) (string, error) {
	if agent.Branch != "" {
		if err := addWorktree(agent); err != nil {
			return "", err
		}
	}
	options, err := s.provision(p, agent)
	if err != nil {
		return "", err
	}
	s.Store.Append(agent.Name, store.Event{Kind: store.EventStarted})
	return s.Driver.Start(options)
}

func (s *Server) awaitEarlierAttempt(p core.RunParams, name string) (core.View, error) {
	unlock, err := s.Store.LockDelivery(name)
	if err == nil {
		defer unlock()
		_, err = s.Store.Load(name)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return s.run(p)
	}
	if err != nil {
		return core.View{}, err
	}
	return s.confirmStart(name, "", nil)
}

func (s *Server) rollback(a store.Agent) {
	if a.Branch != "" {
		git(a.Repo, "worktree", "remove", "--force", "--force", a.Dir)
		git(a.Repo, "branch", "-D", a.Branch)
	}
	s.Store.Delete(a.Name)
}

func (s *Server) provision(p core.RunParams, agent store.Agent) (claude.StartOptions, error) {
	var options claude.StartOptions
	if agent.Bypass && !s.Driver.BypassAccepted() {
		if err := s.Driver.AcceptBypass(); err != nil {
			return options, err
		}
	}
	settings, err := claude.HookSettings(fmt.Sprintf("%q hook %s", s.goferPath(), agent.Name), agent.Bypass)
	if err != nil {
		return options, err
	}
	if err := s.Store.WriteSettings(agent.Name, settings); err != nil {
		return options, err
	}
	prompt, err := s.promptArg(agent.Name, buildPrompt(p, agent.Branch))
	if err != nil {
		return options, err
	}
	return claude.StartOptions{Launch: s.launch(agent), Dir: agent.Dir, Prompt: prompt}, nil
}

func (s *Server) launch(a store.Agent) claude.Launch {
	return claude.Launch{
		Name: a.Name, Bypass: a.Bypass, Model: a.Model, Effort: a.Effort,
		Settings: s.Store.SettingsPath(a.Name), RemoteControl: s.Driver.CanRemoteControl(),
	}
}

func unconfirmed(name string, cause error) error {
	return fmt.Errorf("claude may have started %s, but gofer couldn't confirm it (%v). Its record is kept, so check gofer ls before running it again, and use gofer rm %s if it never appears", name, cause, name)
}

func (s *Server) confirmStart(name, job string, cause error) (core.View, error) {
	if cause == nil {
		cause = errors.New("the session isn't listed")
	}
	deadline := time.Now().Add(startWindow)
	listed := false
	for {
		a, err := s.Store.Load(name)
		if err != nil {
			return core.View{}, unconfirmed(name, err)
		}
		snap, err := s.snapshot()
		if err != nil {
			cause = err
		}
		if sess, ok := snap.job(job); ok && a.SessionID == "" {
			a.SessionID = sess.SessionID
			if err := s.Store.Update(name, func(stored *store.Agent) { stored.SessionID = sess.SessionID }); err != nil {
				return core.View{}, unconfirmed(name, err)
			}
		}
		t := snap.target(a)
		view := s.view(t, time.Now().UTC())
		if t.listed && !listed {
			listed = true
			deadline = time.Now().Add(linkWindow)
		}
		if view.Link == "" && time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if a.SessionID == "" {
			return core.View{}, unconfirmed(name, cause)
		}
		if !t.listed {
			view.State, view.Settled = core.StateUnknown, false
		}
		return view, nil
	}
}
