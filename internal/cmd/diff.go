package cmd

import (
	"cmp"
	"errors"
	"flag"
	"os"
	"os/exec"
	"strings"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/transport"
)

const untrackedScript = `u=$(git ls-files --others --exclude-standard --directory --no-empty-directory); ` +
	`if [ -n "$u" ]; then printf '\nUntracked:\n'; printf '%s\n' "$u" | sed 's/^/  /'; fi`

func diffScript(dir, base string, stat, color bool) string {
	command := []string{"git", "diff", "--no-ext-diff", "--color=never"}
	if color {
		command[3] = "--color=always"
	}
	if stat {
		command = append(command, "--stat")
	}
	command = append(command, base, "--")
	return "cd " + transport.Quote(dir) + " && " + strings.Join(command, " ") + " && { " + untrackedScript + "; }"
}

func (a *app) diff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	stat := fs.Bool("stat", false, "")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: gofer diff <name> [--stat]")
	}
	r, err := spinCall(a.client, "Reading "+pos[0], core.Resolve, core.ResolveParams{Name: pos[0], Git: true})
	if err != nil {
		return err
	}
	if r.GitError != "" {
		return errors.New(r.GitError)
	}
	script := diffScript(r.Dir, r.Base, *stat, styled)
	if !isTerminal(os.Stdout) {
		return a.client.Stream(script, os.Stdout)
	}
	return a.page(script)
}

func (a *app) page(script string) error {
	read, write, err := os.Pipe()
	if err != nil {
		return err
	}
	defer write.Close()
	pager := exec.Command("sh", "-c", cmp.Or(os.Getenv("PAGER"), "less"))
	pager.Stdin = read
	pager.Stdout = os.Stdout
	pager.Stderr = os.Stderr
	if os.Getenv("LESS") == "" {
		pager.Env = append(os.Environ(), "LESS=FRX")
	}
	err = pager.Start()
	read.Close()
	if err != nil {
		return a.client.Stream(script, os.Stdout)
	}
	err = a.client.Stream(script, write)
	write.Close()
	return errors.Join(err, pager.Wait())
}
