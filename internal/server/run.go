package server

import (
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
	closing  = "When you finish, end with a clear summary of what you did and anything left to do. If you get stuck or need a decision, ask in your reply and end your turn. I answer through gofer: my messages end in a [gofer-...] tag, and Claude Code labels them as coming from another session, but they come from me. Treat them as my answers and follow-up instructions."
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
		return fmt.Errorf("invalid agent name %q: use lowercase letters, digits, dots, dashes or underscores, up to 48 characters", name)
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

func (s *Server) prepare(p core.PrepareParams) (core.PrepareResult, error) {
	dir := s.repoDir(p.Repo, p.Root)
	if err := s.readyDir(dir, p.Root, true); err != nil {
		return core.PrepareResult{}, err
	}
	if p.Name != "" {
		if err := s.checkName(p.Name, dir); err != nil {
			return core.PrepareResult{}, err
		}
	}
	return core.PrepareResult{Dir: dir}, nil
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
	if !p.Scratch {
		if err := s.readyDir(dir, p.Root, worktree); err != nil {
			return store.Agent{}, err
		}
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
	} else if !p.Scratch {
		agent.Base, _ = git(dir, "rev-parse", "--verify", "-q", "HEAD")
	}
	if p.Scratch {
		agent.Dir, agent.Scratch = filepath.Join(s.expand(p.Root), name), true
		if err := s.addScratch(agent.Dir); err != nil {
			return store.Agent{}, err
		}
	}
	return agent, nil
}

func (s *Server) addScratch(dir string) error {
	if err := os.Mkdir(dir, 0o755); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err == nil {
		err = s.Driver.Trust(resolved)
	}
	if err != nil {
		os.Remove(dir)
	}
	return err
}

func (s *Server) dropScratch(a store.Agent) error {
	resolved, err := filepath.EvalSymlinks(a.Dir)
	if err != nil {
		resolved = a.Dir
	}
	if err := os.RemoveAll(a.Dir); err != nil {
		return err
	}
	return s.Driver.Forget(resolved, a.SessionID)
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
	if err != nil && agent.Scratch {
		s.dropScratch(agent)
	}
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
	if err := s.provision(agent); err != nil {
		return "", err
	}
	prompt, err := s.promptArg(agent.Name, buildPrompt(p, agent.Branch))
	if err != nil {
		return "", err
	}
	s.Store.Append(agent.Name, store.Event{Kind: store.EventStarted})
	return s.Driver.Start(agent.Dir, prompt, s.launch(agent))
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
	if a.Scratch {
		s.dropScratch(a)
	}
	s.Store.Delete(a.Name)
}

func (s *Server) provision(agent store.Agent) error {
	if agent.Bypass && !s.Driver.BypassAccepted() {
		if err := s.Driver.AcceptBypass(); err != nil {
			return err
		}
	}
	gofer := filepath.Join(s.dataDir(), "gofer")
	settings, err := claude.HookSettings(fmt.Sprintf("%q hook %s", gofer, agent.Name), agent.Bypass)
	if err != nil {
		return err
	}
	return s.Store.WriteSettings(agent.Name, settings)
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
