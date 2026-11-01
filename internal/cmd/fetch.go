package cmd

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"strconv"

	"github.com/anjay-goel/gofer/internal/core"
)

func isAncestor(commit, of string) bool {
	_, err := localGit("merge-base", "--is-ancestor", commit, of)
	return err == nil
}

func (a *app) fetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: gofer fetch <name>")
	}
	if _, err := localGit("rev-parse", "--show-toplevel"); err != nil {
		return errors.New("gofer fetch needs a git repo in the current folder")
	}
	r, err := spinCall(a.client, "Reading "+pos[0], core.Resolve, core.ResolveParams{Name: pos[0], Git: true})
	if err != nil {
		return err
	}
	if r.GitError != "" {
		return errors.New(r.GitError)
	}
	if r.Branch == "" {
		return fmt.Errorf("%s isn't on a branch, so there's nothing to fetch", r.Dir)
	}
	stop := spinning("Fetching " + r.Name)
	note, err := fetchBranch(a.client.Location(r.Dir), r.Branch, core.Branch(r.Name))
	stop()
	if err != nil {
		return err
	}
	done("%s", note)
	if r.Dirty {
		fmt.Println("The dev box has uncommitted work, which a fetch doesn't include")
	}
	return nil
}

func fetchBranch(remote, branch, local string) (string, error) {
	if _, err := localGit("fetch", remote, "refs/heads/"+branch); err != nil {
		return "", fmt.Errorf("couldn't fetch %s: %w", branch, err)
	}
	head, err := localGit("rev-parse", "FETCH_HEAD")
	if err != nil {
		return "", err
	}
	if _, err := localGit("merge-base", "HEAD", head); err != nil {
		return "", fmt.Errorf("%s shares no history with this repo", branch)
	}
	ref := "refs/heads/" + local
	old, _ := localGit("rev-parse", "--verify", "-q", ref)
	from := cmp.Or(old, "HEAD")
	if isAncestor(head, from) {
		return fmt.Sprintf("%s is up to date at %s", local, head[:6]), nil
	}
	if old != "" && !isAncestor(old, head) {
		return "", fmt.Errorf("%s has diverged from the agent's branch, so it wasn't changed", local)
	}
	out, err := localGit("rev-list", "--count", from+".."+head)
	if err != nil {
		return "", err
	}
	count, err := strconv.Atoi(out)
	if err != nil {
		return "", err
	}
	if err := moveBranch(local, ref, old, head); err != nil {
		return "", fmt.Errorf("couldn't update %s: %w", local, err)
	}
	return fmt.Sprintf("%s at %s, %s", local, head[:6], commitWord(&count)), nil
}

func moveBranch(local, ref, old, head string) error {
	worktree, err := localGit("branch", "--list", "--format=%(worktreepath)", local)
	if err != nil {
		return err
	}
	if worktree != "" {
		_, err = localGit("-C", worktree, "merge", "--ff-only", "-q", head)
		return err
	}
	_, err = localGit("update-ref", "-m", "gofer fetch", ref, head, old)
	return err
}
