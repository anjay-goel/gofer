package server

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/store"
)

func (s *Server) remove(p core.RemoveParams) (core.RemoveResult, error) {
	var result core.RemoveResult
	t, unlock, err := s.locateLocked(p.Name)
	var bad store.Unreadable
	if p.Force && errors.As(err, &bad) {
		result.Note = "its record couldn't be read, so only the record was dropped. Its worktree and session weren't checked"
		return result, s.Store.Delete(bad.Name)
	}
	if err != nil {
		return result, err
	}
	defer unlock()
	if !t.managed() {
		return result, fmt.Errorf("%s wasn't started by gofer, so remove it with claude rm", p.Name)
	}
	a := t.agent
	if !p.Force {
		v := s.view(t, time.Now().UTC())
		if v.State == core.StateBlocked {
			return result, fmt.Errorf("%s is blocked. Stop it with gofer stop %s, or use --force", a.Name, a.Name)
		}
		if v.Working() {
			return result, fmt.Errorf("%s is running. Stop it with gofer stop %s, or use --force", a.Name, a.Name)
		}
	}
	if a.Branch != "" && !p.Force {
		if result.Unfetched, err = checkWork(a, p.FetchedHead); err != nil || result.Unfetched != "" {
			return result, err
		}
	}
	if a.Branch != "" {
		if result.Note, err = removeWorktree(a, p.Force); err != nil {
			return result, err
		}
	}
	if t.sess.Job != "" {
		if err := s.Driver.Remove(t.sess.Job); err != nil {
			return result, err
		}
	}
	if a.Scratch {
		if err := s.dropScratch(a); err != nil {
			return result, err
		}
	}
	return result, s.Store.Delete(a.Name)
}

func checkWork(a store.Agent, fetchedHead string) (unfetched string, err error) {
	if _, err := os.Stat(a.Repo); err != nil {
		return "", fmt.Errorf("%s is gone, so the worktree can't be removed. Use --force to drop the agent anyway", a.Repo)
	}
	if _, err := os.Stat(a.Dir); err != nil {
		return "", nil
	}
	status, err := git(a.Dir, "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("couldn't check %s: %w", a.Dir, err)
	}
	if status != "" {
		return "", fmt.Errorf("%s has uncommitted changes in %s. Commit them, or use --force", a.Name, a.Dir)
	}
	head, err := git(a.Dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("couldn't check %s: %w", a.Dir, err)
	}
	if head == a.Base || head == fetchedHead {
		return "", nil
	}
	if out, _ := git(a.Dir, "branch", "-r", "--contains", head); out != "" {
		return "", nil
	}
	return head, nil
}

func removeWorktree(a store.Agent, force bool) (string, error) {
	if _, err := os.Stat(a.Repo); err != nil {
		return "", nil
	}
	if _, err := os.Stat(a.Dir); os.IsNotExist(err) {
		_, err := git(a.Repo, "worktree", "prune")
		return "kept branch " + a.Branch, err
	}
	args := []string{"worktree", "remove", "--force", a.Dir}
	if force {
		args = []string{"worktree", "remove", "--force", "--force", a.Dir}
	}
	if _, err := git(a.Repo, args...); err != nil {
		return "", fmt.Errorf("couldn't remove the worktree: %w", err)
	}
	return "kept branch " + a.Branch, nil
}
