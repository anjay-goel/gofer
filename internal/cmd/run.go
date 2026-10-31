package cmd

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/driver/claude"
	"github.com/anjay-goel/gofer/internal/proc"
	"github.com/anjay-goel/gofer/internal/transport"
)

const (
	runAttempts   = 5
	localGitLimit = time.Hour
)

var runRetryDelay = 5 * time.Second

func localGit(args ...string) (string, error) {
	return proc.Cmd{Name: "git", Args: args, Limit: localGitLimit}.Output()
}

func readDoc(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("%s isn't UTF-8 text, so it can't go to the agent as a document", path)
	}
	return string(data), nil
}

func (a *app) run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	name := fs.String("name", "", "")
	doc := fs.String("doc", "", "")
	model := fs.String("model", "", "")
	effort := fs.String("effort", "", "")
	bypass := fs.Bool("bypass", false, "")
	worktree := fs.Bool("worktree", false, "")
	allowDirty := fs.Bool("allow-dirty", false, "")
	asJSON := fs.Bool("json", false, "")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("usage: gofer run <dir> \"<prompt>\"")
	}
	if err := claude.ValidEffort(*effort); err != nil {
		return err
	}
	repo := pos[0]
	if repo == "." {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		repo = filepath.Base(cwd)
	}
	prompt := strings.Join(pos[1:], " ")
	params := core.RunParams{
		RequestID: core.NewRequestID(), Name: *name, Repo: tildePath(repo), Root: a.hostCfg.Root, Prompt: prompt,
		Model: *model, Effort: *effort, Bypass: *bypass,
	}
	if *doc != "" {
		if params.Doc, err = readDoc(*doc); err != nil {
			return err
		}
		params.DocName = strings.TrimSuffix(filepath.Base(*doc), filepath.Ext(*doc))
	}
	if strings.TrimSpace(prompt) == "" && params.Doc == "" {
		return errors.New("give the agent a prompt or a --doc")
	}
	var remote string
	if *worktree {
		remote, err = a.handoff(&params, *allowDirty)
		if err != nil {
			return err
		}
	}
	stop := spinning("Starting agent on " + a.host)
	view, err := a.start(params)
	stop()
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(view)
	}
	done("Started %s on %s", paint(bold, view.Name), a.host)
	fmt.Printf("  %s  %s\n", paint(dim, "folder"), view.Dir)
	if view.Link != "" {
		fmt.Printf("  %s  %s\n", paint(dim, "follow"), view.Link)
	}
	if remote != "" {
		fmt.Printf("  %s   git fetch %s %s\n", paint(dim, "fetch"), remote, view.Branch)
	}
	return nil
}

func (a *app) start(params core.RunParams) (core.View, error) {
	delay := runRetryDelay
	for attempt := 1; ; attempt++ {
		view, err := transport.Call(a.client, core.Run, params)
		var network transport.NetworkError
		if !errors.As(err, &network) {
			return view, err
		}
		if !network.Retryable() || attempt == runAttempts {
			return view, fmt.Errorf("%w. The agent may have started anyway, so check gofer ls before running it again", err)
		}
		fmt.Fprintln(os.Stderr, "lost the dev box, retrying:", network.Detail)
		time.Sleep(delay)
		delay *= 2
	}
}

func (a *app) handoff(p *core.RunParams, allowDirty bool) (string, error) {
	if _, err := localGit("rev-parse", "--show-toplevel"); err != nil {
		return "", errors.New("--worktree needs a git repo in the current folder")
	}
	status, err := localGit("status", "--porcelain")
	if err != nil {
		return "", err
	}
	if status != "" && !allowDirty {
		return "", errors.New("you have uncommitted changes, and only commits reach the dev box. Commit them, or pass --allow-dirty")
	}
	head, err := localGit("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	prep, err := transport.Call(a.client, core.Prepare, core.PrepareParams{Name: p.Name, Repo: p.Repo, Root: p.Root})
	if err != nil {
		return "", err
	}
	remote := a.client.Location(prep.Dir)
	if _, err := localGit("push", remote, head+":"+core.HandoffRef(p.RequestID)); err != nil {
		return "", fmt.Errorf("couldn't push to the dev box: %w", err)
	}
	p.Repo, p.Base = prep.Dir, head
	return remote, nil
}
