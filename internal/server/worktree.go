package server

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/anjay-goel/gofer/internal/driver/claude"
	"github.com/anjay-goel/gofer/internal/proc"
	"github.com/anjay-goel/gofer/internal/store"
)

const gitLimit = 2 * time.Minute

func git(dir string, args ...string) (string, error) {
	return proc.Cmd{Name: "git", Args: args, Dir: dir, Limit: gitLimit}.Output()
}

func worktreePath(repo, name string) string {
	return filepath.Join(repo, filepath.FromSlash(claude.WorktreesDir), name)
}

func addWorktree(a store.Agent) error {
	if err := excludeWorktrees(a.Repo); err != nil {
		return fmt.Errorf("couldn't add %s to the repo's git exclude file: %w", claude.WorktreesDir, err)
	}
	if _, err := git(a.Repo, "worktree", "add", "-b", a.Branch, a.Dir, a.Base); err != nil {
		return fmt.Errorf("couldn't create the worktree: %w", err)
	}
	return nil
}

func excludeWorktrees(repo string) error {
	exclude, err := git(repo, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(repo, exclude)
	}
	data, err := os.ReadFile(exclude)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if slices.Contains(strings.Split(string(data), "\n"), claude.WorktreesDir) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(exclude, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	entry := claude.WorktreesDir + "\n"
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		entry = "\n" + entry
	}
	_, err = f.WriteString(entry)
	return err
}

func mainCheckout(dir string) string {
	common, err := git(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	if filepath.Base(common) != ".git" {
		return ""
	}
	return filepath.Dir(common)
}
