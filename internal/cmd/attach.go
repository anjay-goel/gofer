package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/terminal"
	"github.com/anjay-goel/gofer/internal/transport"
)

func (a *app) attach(args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	rex := fs.Bool("rex", false, "")
	ssh := fs.Bool("ssh", false, "")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: gofer attach <name>")
	}
	r, err := transport.Call(a.client, core.Resolve, core.ResolveParams{Name: pos[0]})
	if err != nil {
		return err
	}
	if r.Job == "" {
		return errors.New(r.Note)
	}
	useRex := *rex || (a.hostCfg.Terminal == "rex" && !*ssh)
	if useRex {
		note, err := terminal.AttachRex(a.host, pos[0], r.Job)
		if err == nil {
			fmt.Println(note)
			return nil
		}
		fmt.Fprintln(os.Stderr, "Falling back to SSH:", err)
	}
	return terminal.AttachSSH(a.host, r.Job)
}
