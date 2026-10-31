package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/terminal"
	"github.com/anjay-goel/gofer/internal/transport"
)

func (a *app) doctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	live := fs.Bool("live", false, "")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	fmt.Printf("%s %s\n", paint(dim, "Dev box:"), paint(bold, a.host))
	stop := spinning("Checking " + a.host)
	defer stop()
	if err := a.client.Reachable(); err != nil {
		return err
	}
	checks, err := transport.Call(a.client, core.Doctor, core.DoctorParams{Root: a.hostCfg.Root})
	stop()
	if err != nil {
		return err
	}
	allOK := printChecks(checks)
	if _, err := exec.LookPath("rex"); err == nil {
		if terminal.RexReaches(a.host) {
			fmt.Println(checkMark("ok"), "rex: reaches", a.host)
		} else {
			fmt.Printf("%s rex: can't reach a Rex host called %s. Attach will use SSH\n", checkMark("note"), a.host)
		}
	}
	if *live {
		if err := a.smoke(); err != nil {
			return err
		}
	}
	if !allOK {
		return exitCode{1, ""}
	}
	return nil
}

func (a *app) smoke() error {
	name := fmt.Sprintf("gofer-smoke-%d", time.Now().Unix())
	prompt := "Reply with the word READY and nothing else. For any later message, reply with ECHO followed by its first word."
	params := core.RunParams{RequestID: core.NewRequestID(), Name: name, Scratch: true, Root: a.hostCfg.Root, Prompt: prompt, Model: "haiku"}
	defer transport.Call(a.client, core.Remove, core.RemoveParams{Name: name, Force: true})
	if _, err := a.start(params); err != nil {
		return fmt.Errorf("live test couldn't start an agent: %w", err)
	}
	r, err := transport.Call(a.client, core.Wait, core.WaitParams{Names: []string{name}, Timeout: 90})
	if err != nil {
		return err
	}
	if !r.Finished || r.Failed || r.Unconfirmed {
		return errors.New("live test: the agent didn't finish its first turn")
	}
	sent, err := transport.Call(a.client, core.Send, core.SendParams{Name: name, Message: "PONG"})
	if err != nil {
		return err
	}
	if !sent.Delivered {
		return fmt.Errorf("live test: message not delivered: %s", sent.Note)
	}
	reply, err := transport.Call(a.client, core.Wait, core.WaitParams{Names: []string{name}, Timeout: 90})
	if err != nil {
		return err
	}
	echoed := func(l core.LogLine) bool { return strings.Contains(l.Text, "PONG") }
	if len(reply.Views) != 1 || !slices.ContainsFunc(reply.Views[0].Work, echoed) {
		return errors.New("live test: no reply to the message")
	}
	fmt.Println(checkMark("ok"), "live test: start, wait, send and reply")
	return nil
}

func printCheck(c core.Check) {
	kind := "ok"
	if !c.OK {
		kind = "FAIL"
	}
	fmt.Printf("%s %s", checkMark(kind), c.Name)
	if c.Detail != "" {
		fmt.Printf(": %s", c.Detail)
	}
	fmt.Println()
}

func printChecks(checks []core.Check) bool {
	for _, c := range checks {
		printCheck(c)
	}
	return !slices.ContainsFunc(checks, failed)
}

func failed(c core.Check) bool {
	return !c.OK
}

func printCheckSummary(checks []core.Check) bool {
	if !styled {
		return printChecks(checks)
	}
	passed := 0
	for _, c := range checks {
		if c.OK {
			passed++
		} else {
			printCheck(c)
		}
	}
	done("%d checks passed", passed)
	return !slices.ContainsFunc(checks, failed)
}
