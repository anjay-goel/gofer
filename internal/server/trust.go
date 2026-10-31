package server

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func checkDir(dir string, worktree bool) error {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("%s doesn't exist on the dev box", dir)
	}
	if worktree {
		if _, err := git(dir, "rev-parse", "--git-dir"); err != nil {
			return fmt.Errorf("%s isn't a git repo on the dev box: %w", dir, err)
		}
	}
	return nil
}

func (s *Server) readyDir(dir, root string, worktree bool) error {
	if err := checkDir(dir, worktree); err != nil {
		return err
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
