package cmd

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/transport"
)

const (
	waitSlice  = 50 * time.Second
	waitMargin = 45 * time.Second
	backoffMin = 5 * time.Second
	backoffMax = 30 * time.Second
	logIndent  = 8
)

func (a *app) logs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	limit := fs.Int("n", 30, "")
	follow := fs.Bool("f", false, "")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: gofer logs <name>")
	}
	name := pos[0]
	r, err := spinCall(a.client, "Reading "+name, core.Logs, core.LogsParams{Name: name, Limit: *limit})
	if err != nil {
		return err
	}
	printLines(r.Lines)
	if r.Link != "" {
		printLines([]core.LogLine{{Role: "follow", Text: r.Link}})
	}
	if !*follow {
		return nil
	}
	cursor := r.Cursor
	last, err := a.longPoll("", time.Time{}, func(slice time.Duration) (core.WaitResult, error) {
		params := core.FollowParams{Name: name, Cursor: cursor, Timeout: int(math.Ceil(slice.Seconds()))}
		next, err := transport.CallWithin(a.client, slice+waitMargin, core.Follow, params)
		if err == nil {
			printLines(next.Lines)
			cursor = next.Cursor
		}
		return next.Wait, err
	})
	if err != nil {
		return err
	}
	return finishWait(last, false)
}

func printLines(lines []core.LogLine) {
	for _, l := range lines {
		look := roleLooks[l.Role]
		indent := strings.Repeat(" ", logIndent)
		text := strings.ReplaceAll(strings.TrimSpace(l.Text), "\n", "\n"+indent)
		text = strings.ReplaceAll(text, indent+"\n", "\n")
		if l.Role == "tool" {
			text = paint(dim, text)
		}
		fmt.Printf("%s%s%s\n", look, strings.Repeat(" ", logIndent-len(look.text)), text)
	}
}

func printWait(r core.WaitResult) {
	for i, v := range r.Views {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("%s: %s\n", paint(bold, v.Name), waitState(v))
		printLines(v.Work)
		if footer := workFooter(v); footer != "" {
			fmt.Println(paint(dim, footer))
		}
	}
}

func workFooter(v core.View) string {
	var parts []string
	if word := commitWord(v.Commits); word != "" {
		parts = append(parts, word)
	}
	if v.ToolCalls > 0 {
		parts = append(parts, plural(v.ToolCalls, "tool call"))
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("%s. gofer logs %s has the rest.", strings.Join(parts, ", "), v.Name)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func waitState(v core.View) cell {
	state := stateCell(v, 0)
	switch {
	case v.Stuck() && v.Next != "":
		state.text += ". Run " + v.Next
	case v.Unconfirmed:
		state.text += ", but its turn can't be confirmed finished, so this work may be stale. Run " + v.Next
	}
	return state
}

func finishWait(r core.WaitResult, asJSON bool) error {
	if asJSON {
		if err := printJSON(r); err != nil {
			return err
		}
	} else {
		printWait(r)
	}
	switch {
	case r.Blocked:
		return exitCode{exitBlocked, ""}
	case r.Failed:
		return exitCode{exitFailed, ""}
	case r.Unconfirmed:
		return exitCode{exitUnconfirmed, ""}
	}
	return nil
}

func (a *app) longPoll(label string, deadline time.Time, call func(time.Duration) (core.WaitResult, error)) (core.WaitResult, error) {
	backoff := backoffMin
	trouble := ""
	stop := func() {}
	start := func() {
		if label != "" {
			stop = spinning(label)
		}
	}
	start()
	defer func() { stop() }()
	report := func(now string) {
		if now == trouble {
			return
		}
		stop()
		if now == "" {
			fmt.Fprintln(os.Stderr, "back in touch with the dev box")
		} else {
			fmt.Fprintln(os.Stderr, now+". Still waiting")
		}
		trouble = now
		start()
	}
	for {
		slice := waitSlice
		if !deadline.IsZero() {
			slice = min(slice, time.Until(deadline))
			if slice <= 0 {
				return core.WaitResult{}, exitCode{exitTimedOut, "timed out"}
			}
		}
		r, err := call(slice)
		var network transport.NetworkError
		if errors.As(err, &network) {
			if !network.Retryable() {
				return r, fmt.Errorf("%w. Fix SSH login to %s, then try again", err, a.host)
			}
			report(network.Error())
			time.Sleep(min(backoff, slice))
			backoff = min(backoff*2, backoffMax)
			continue
		}
		if err != nil {
			return r, err
		}
		backoff = backoffMin
		report(r.Trouble)
		if r.Done() {
			return r, nil
		}
	}
}

func (a *app) wait(args []string) error {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	timeout := fs.Int("timeout", 0, "")
	asJSON := fs.Bool("json", false, "")
	anyOne := fs.Bool("any", false, "")
	notify := fs.Bool("notify", false, "")
	names, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return errors.New("usage: gofer wait <name>...")
	}
	deadline := time.Time{}
	if *timeout > 0 {
		deadline = time.Now().Add(time.Duration(*timeout) * time.Second)
	}
	label := "Waiting for " + strings.Join(names, ", ")
	r, err := a.longPoll(label, deadline, func(slice time.Duration) (core.WaitResult, error) {
		params := core.WaitParams{Names: names, Timeout: int(math.Ceil(slice.Seconds())), Any: *anyOne}
		return transport.CallWithin(a.client, slice+waitMargin, core.Wait, params)
	})
	if err != nil {
		return err
	}
	err = finishWait(r, *asJSON)
	if *notify {
		raiseAlerts(r, !*asJSON && alertable())
	}
	return err
}
