package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/transport"
)

func (a *app) stop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: gofer stop <name>")
	}
	r, err := spinCall(a.client, "Stopping "+pos[0], core.Stop, core.NameParams{Name: pos[0]})
	if err != nil {
		return err
	}
	if r.AlreadyStopped {
		done("%s was already stopped", pos[0])
		return nil
	}
	done("Stopped %s", pos[0])
	return nil
}

func (a *app) rm(args []string) error {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	force := fs.Bool("force", false, "")
	finished := fs.Bool("finished", false, "")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	switch {
	case *finished && *force:
		return errors.New("--finished skips unsaved work, so it can't be combined with --force")
	case *finished && len(pos) > 0:
		return errors.New("--finished takes no names")
	case *finished:
		return a.rmFinished()
	case len(pos) == 0:
		return errors.New("usage: gofer rm <name>...")
	}
	failed := false
	for _, name := range pos {
		err := a.removeAgent(name, *force)
		var network transport.NetworkError
		if errors.As(err, &network) {
			return err
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, errorPrefix(), err)
			failed = true
		}
	}
	if failed {
		return exitCode{1, ""}
	}
	return nil
}

func (a *app) rmFinished() error {
	views, err := spinCall(a.client, "Reading "+a.host, core.List, core.ListParams{Client: a.cfg.Client, Peek: true})
	if err != nil {
		return err
	}
	settled := slices.DeleteFunc(views, func(v core.View) bool { return v.Source != "gofer" || !v.Settled })
	if len(settled) == 0 {
		fmt.Println("No finished agents on", a.host)
		return nil
	}
	for _, v := range settled {
		if v.Stuck() {
			fmt.Printf("Skipped %s: %s, so its work may be unfinished\n", v.Name, stateLabel(v, detailClip))
			continue
		}
		err := a.removeAgent(v.Name, false)
		var network transport.NetworkError
		if errors.As(err, &network) {
			return err
		}
		if err != nil {
			fmt.Println("Skipped:", err)
		}
	}
	return nil
}

func (a *app) removeAgent(name string, force bool) error {
	params := core.RemoveParams{Name: name, Force: force}
	for {
		r, err := spinCall(a.client, "Removing "+name, core.Remove, params)
		if err != nil {
			return err
		}
		if r.Unfetched == "" {
			done("Removed %s", name)
			if r.Note != "" {
				fmt.Println(r.Note)
			}
			return nil
		}
		if _, err := localGit("cat-file", "-e", r.Unfetched+"^{commit}"); err != nil {
			return fmt.Errorf("%s has commits you haven't fetched. Fetch branch %s here, or use --force. The branch is kept either way", name, core.Branch(name))
		}
		params.FetchedHead = r.Unfetched
	}
}
