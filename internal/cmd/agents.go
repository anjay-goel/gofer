package cmd

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/driver/claude"
	"github.com/anjay-goel/gofer/internal/proc"
	"github.com/anjay-goel/gofer/internal/terminal"
	"github.com/anjay-goel/gofer/internal/transport"
)

const (
	waitSlice     = 50 * time.Second
	waitMargin    = 45 * time.Second
	backoffMin    = 5 * time.Second
	backoffMax    = 30 * time.Second
	runAttempts   = 5
	localGitLimit = time.Hour
	logIndent     = 8
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
		return errors.New("usage: gofer run <repo> \"<prompt>\"")
	}
	if err := claude.ValidEffort(*effort); err != nil {
		return err
	}
	repo := pos[0]
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

func (a *app) ls(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	peek := fs.Bool("peek", false, "")
	asJSON := fs.Bool("json", false, "")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	stop := spinning("Reading " + a.host)
	views, err := transport.Call(a.client, core.List, core.ListParams{Client: a.cfg.Client, Peek: *peek})
	stop()
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(views)
	}
	if len(views) == 0 {
		fmt.Println("No agents on", a.host)
		return nil
	}
	fmt.Print(agentTable(views))
	return nil
}

func agentTable(views []core.View) string {
	branches := slices.ContainsFunc(views, func(v core.View) bool { return v.Branch != "" })
	row := func(name, state, changed, branch, source cell) []cell {
		cells := []cell{name, state, changed}
		if branches {
			cells = append(cells, branch)
		}
		return append(cells, source)
	}
	header := func(title string) cell { return cell{title, dim} }
	rows := [][]cell{row(header("NAME"), header("STATE"), header("CHANGED"), header("BRANCH"), header("SOURCE"))}
	for _, v := range views {
		name := cell{v.Name, bold}
		if v.New {
			name.text += "*"
		}
		rows = append(rows, row(name, stateCell(v), cell{v.Changed, dim}, cell{v.Branch, ""}, cell{v.Source, dim}))
	}
	return table(rows)
}

func (a *app) logs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	limit := fs.Int("n", 30, "")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: gofer logs <name>")
	}
	stop := spinning("Reading " + pos[0])
	lines, err := transport.Call(a.client, core.Logs, core.LogsParams{Name: pos[0], Limit: *limit})
	stop()
	if err != nil {
		return err
	}
	printLines(lines)
	return nil
}

func printLines(lines []core.LogLine) {
	for _, l := range lines {
		look, ok := roleLooks[l.Role]
		if !ok {
			look = cell{l.Role, ""}
		}
		indent := strings.Repeat(" ", logIndent)
		text := strings.ReplaceAll(strings.TrimSpace(l.Text), "\n", "\n"+indent)
		text = strings.ReplaceAll(text, indent+"\n", "\n")
		if l.Role == "tool" {
			text = paint(dim, text)
		}
		fmt.Printf("%s%s%s\n", look, strings.Repeat(" ", logIndent-len(look.text)), text)
	}
}

func stateLabel(v core.View) string {
	if v.Detail == "" {
		return string(v.State)
	}
	return fmt.Sprintf("%s (%s)", v.State, v.Detail)
}

func printWait(r core.WaitResult) {
	for i, v := range r.Views {
		if i > 0 {
			fmt.Println()
		}
		state := stateCell(v)
		switch {
		case v.Cut:
			state.text += ", cut short before finishing. Use gofer send to wake it"
		case v.State == core.StateFailed:
			state.text += ". Use gofer send to try again"
		case v.Unconfirmed:
			state.text += ", but its turn can't be confirmed finished, so this work may be stale. Check gofer logs " + v.Name
		}
		fmt.Printf("%s: %s\n", paint(bold, v.Name), state)
		printLines(v.Work)
	}
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

func (a *app) wait(args []string) error {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	timeout := fs.Int("timeout", 0, "")
	asJSON := fs.Bool("json", false, "")
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
	backoff := backoffMin
	trouble := ""
	label := "Waiting for " + strings.Join(names, ", ")
	stop := func() {}
	if !*asJSON {
		stop = spinning(label)
	}
	defer func() { stop() }()
	report := func(now string) {
		if now != trouble {
			stop()
		}
		switch {
		case now == trouble:
		case now == "":
			fmt.Fprintln(os.Stderr, "back in touch with the dev box")
		default:
			fmt.Fprintln(os.Stderr, now+". Still waiting")
		}
		trouble = now
	}
	for {
		slice := waitSlice
		if !deadline.IsZero() {
			slice = min(slice, time.Until(deadline))
			if slice <= 0 {
				return exitCode{exitTimedOut, "timed out"}
			}
		}
		params := core.WaitParams{Names: names, Timeout: int(math.Ceil(slice.Seconds()))}
		r, err := transport.CallWithin(a.client, slice+waitMargin, core.Wait, params)
		var network transport.NetworkError
		if errors.As(err, &network) {
			if !network.Retryable() {
				return fmt.Errorf("%w. Fix SSH login to %s, then run gofer wait again", err, a.host)
			}
			report(network.Error())
			time.Sleep(min(backoff, slice))
			if !*asJSON {
				stop = spinning(label)
			}
			backoff = min(backoff*2, backoffMax)
			continue
		}
		if err != nil {
			return err
		}
		backoff = backoffMin
		report(r.Trouble)
		if r.Done() {
			return finishWait(r, *asJSON)
		}
	}
}

func messageWords(args []string) (string, error) {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	message := strings.Join(args, " ")
	if strings.TrimSpace(message) == "" {
		return "", errors.New("the message is empty")
	}
	return message, nil
}

func (a *app) send(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: gofer send <name> \"<message>\"")
	}
	name := args[0]
	message, err := messageWords(args[1:])
	if err != nil {
		return err
	}
	stop := spinning("Sending to " + name)
	r, err := transport.Call(a.client, core.Send, core.SendParams{Name: name, Message: message})
	stop()
	if err != nil {
		var network transport.NetworkError
		if errors.As(err, &network) {
			return exitCode{exitUnconfirmed, fmt.Sprintf("%s. The message may have landed anyway, so check gofer logs %s before resending", err, name)}
		}
		return err
	}
	if r.Delivered {
		if r.Action == "resumed" {
			done("Woke %s and delivered your message", name)
		} else {
			done("Delivered to %s", name)
		}
		return nil
	}
	return exitCode{exitUnconfirmed, fmt.Sprintf("Sent to %s (%s), delivery not confirmed: %s", name, r.Action, r.Note)}
}

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
	r, err := transport.Call(a.client, core.Resolve, core.NameParams{Name: pos[0]})
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

var remoteArg = regexp.MustCompile(`^([a-z0-9][a-z0-9._-]*):(.*)$`)

func splitRemote(arg string) (name, path string, ok bool) {
	m := remoteArg.FindStringSubmatch(arg)
	if m == nil {
		return "", "", false
	}
	if _, err := os.Stat(arg); err == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

func (a *app) remotePath(name, file string) (string, error) {
	r, err := transport.Call(a.client, core.Resolve, core.NameParams{Name: name})
	if err != nil {
		return "", err
	}
	return a.client.Location(inAgentDir(r.Dir, file)), nil
}

func inAgentDir(dir, file string) string {
	switch {
	case path.IsAbs(file), file == "~", strings.HasPrefix(file, "~/"):
		return file
	case file == "", strings.HasSuffix(file, "/"):
		return path.Join(dir, file) + "/"
	}
	return path.Join(dir, file)
}

func localPath(file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return "./" + file
}

func (a *app) cp(args []string) error {
	fs := flag.NewFlagSet("cp", flag.ContinueOnError)
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return errors.New("usage: gofer cp <local> <name>:[path], or gofer cp <name>:<path> <local>")
	}
	fromName, fromPath, fromRemote := splitRemote(pos[0])
	toName, toPath, toRemote := splitRemote(pos[1])
	switch {
	case fromRemote == toRemote:
		return errors.New("exactly one side must be <name>:<path>")
	case fromRemote:
		source, err := a.remotePath(fromName, fromPath)
		if err != nil {
			return err
		}
		return a.client.Copy(source, localPath(pos[1]))
	default:
		target, err := a.remotePath(toName, toPath)
		if err != nil {
			return err
		}
		return a.client.Copy(localPath(pos[0]), target)
	}
}

func (a *app) stop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: gofer stop <name>")
	}
	stop := spinning("Stopping " + pos[0])
	r, err := transport.Call(a.client, core.Stop, core.NameParams{Name: pos[0]})
	stop()
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
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: gofer rm <name>")
	}
	name := pos[0]
	params := core.RemoveParams{Name: name, Force: *force}
	for {
		stop := spinning("Removing " + name)
		r, err := transport.Call(a.client, core.Remove, params)
		stop()
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
