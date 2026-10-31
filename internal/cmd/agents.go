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
	"runtime"
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
	detailClip    = 40
	summaryClip   = 100
	summaryMin    = 16
	alertClip     = 80
	alertLimit    = 5 * time.Second
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
	saveNames(a.host, views)
	if *asJSON {
		return printJSON(views)
	}
	if len(views) == 0 {
		fmt.Println("No agents on", a.host)
		return nil
	}
	fmt.Print(agentTable(views, terminalWidth()))
	return nil
}

type column struct {
	title string
	cells []cell
}

func (c column) width() int {
	width := displayWidth(c.title)
	for _, cl := range c.cells {
		width = max(width, displayWidth(cl.text))
	}
	return width
}

func (c column) blank() bool {
	return !slices.ContainsFunc(c.cells, func(cl cell) bool {
		return cl.text != "" && (c.title != "SOURCE" || cl.text != "gofer")
	})
}

func commitWord(count *int) string {
	switch {
	case count == nil:
		return ""
	case *count == 1:
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", *count)
}

func summaryText(v core.View) string {
	if v.Stuck() && v.Next != "" {
		return "Run " + v.Next
	}
	return v.Summary
}

func agentColumns(views []core.View) []column {
	columns := []column{{title: "NAME"}, {title: "STATE"}, {title: "CHANGED"}, {title: "COMMITS"}, {title: "BRANCH"}, {title: "SOURCE"}, {title: "SUMMARY"}}
	for _, v := range views {
		name := cell{v.Name, bold}
		if v.New {
			name.text += "*"
		}
		row := []cell{
			name, stateCell(v, detailClip), {v.Ago, dim}, {commitWord(v.Commits), ""}, {v.Branch, ""}, {v.Source, dim},
			{clipWidth(summaryText(v), summaryClip), ""},
		}
		for i, c := range row {
			columns[i].cells = append(columns[i].cells, c)
		}
	}
	return columns
}

func removeColumn(columns []column, title string) []column {
	return slices.DeleteFunc(columns, func(c column) bool { return c.title == title })
}

func tableWidth(columns []column, summary int) int {
	width := (len(columns) - 1) * columnGap
	for _, c := range columns {
		if c.title == "SUMMARY" {
			width += min(c.width(), summary)
		} else {
			width += c.width()
		}
	}
	return width
}

func shrink(columns []column, title string, by int) int {
	for i, c := range columns {
		if c.title != title {
			continue
		}
		size := max(c.width()-by, 1)
		used := c.width() - size
		columns[i].title = clipWidth(c.title, size)
		columns[i].cells = make([]cell, len(c.cells))
		for j, cl := range c.cells {
			columns[i].cells[j] = cell{clipWidth(cl.text, size), cl.code}
		}
		return used
	}
	return 0
}

func fitColumns(columns []column, width int) []column {
	columns = slices.DeleteFunc(columns, func(c column) bool {
		return c.blank() && c.title != "NAME" && c.title != "STATE"
	})
	for _, title := range []string{"SOURCE", "BRANCH"} {
		if tableWidth(columns, math.MaxInt) > width {
			columns = removeColumn(columns, title)
		}
	}
	for _, title := range []string{"COMMITS", "CHANGED", "SUMMARY"} {
		if tableWidth(columns, summaryMin) > width {
			columns = removeColumn(columns, title)
		}
	}
	excess := tableWidth(columns, math.MaxInt) - width
	for _, title := range []string{"SUMMARY", "STATE", "NAME"} {
		if excess > 0 {
			excess -= shrink(columns, title, excess)
		}
	}
	return columns
}

func agentTable(views []core.View, width int) string {
	columns := agentColumns(views)
	if width > 0 {
		columns = fitColumns(columns, width)
	}
	rows := make([][]cell, len(views)+1)
	for _, c := range columns {
		rows[0] = append(rows[0], cell{c.title, dim})
		for i, cl := range c.cells {
			rows[i+1] = append(rows[i+1], cl)
		}
	}
	return table(rows)
}

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
	stop := spinning("Reading " + name)
	r, err := transport.Call(a.client, core.Logs, core.LogsParams{Name: name, Limit: *limit})
	stop()
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

func stateLabel(v core.View, limit int) string {
	detail := v.Detail
	if v.Cut {
		detail = "cut short"
	}
	if detail == "" {
		return string(v.State)
	}
	if limit > 0 {
		detail = clipWidth(core.OneLine(detail), limit)
	}
	return fmt.Sprintf("%s (%s)", v.State, detail)
}

func printWait(r core.WaitResult) {
	for i, v := range r.Views {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("%s: %s\n", paint(bold, v.Name), waitState(v))
		printLines(v.Work)
	}
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
			stop()
			time.Sleep(min(backoff, slice))
			start()
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
	first := fs.Bool("any", false, "")
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
	if *asJSON {
		label = ""
	}
	r, err := a.longPoll(label, deadline, func(slice time.Duration) (core.WaitResult, error) {
		params := core.WaitParams{Names: names, Timeout: int(math.Ceil(slice.Seconds())), Any: *first}
		return transport.CallWithin(a.client, slice+waitMargin, core.Wait, params)
	})
	if err != nil {
		return err
	}
	err = finishWait(r, *asJSON)
	if *notify {
		raiseAlerts(r, *asJSON)
	}
	return err
}

func alertText(v core.View) (title, body string) {
	title = "gofer: " + core.OneLine(v.Name)
	body = stateLabel(v, alertClip)
	if v.Next != "" {
		body += ". Run " + core.OneLine(v.Next)
	}
	return title, body
}

func alertCommand(goos, title, body string) (string, []string) {
	switch goos {
	case "darwin":
		script := "on run argv\ndisplay notification (item 2 of argv) with title (item 1 of argv)\nend run"
		return "osascript", []string{"-e", script, title, body}
	case "linux":
		return "notify-send", []string{"--", title, body}
	}
	return "", nil
}

func raiseAlerts(r core.WaitResult, asJSON bool) {
	for _, v := range r.Views {
		if !v.Returned() {
			continue
		}
		title, body := alertText(v)
		if !asJSON {
			terminalAlert(title + ": " + body)
		}
		if name, args := alertCommand(runtime.GOOS, title, body); name != "" {
			proc.Cmd{Name: name, Args: args, Limit: alertLimit}.Run()
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
	stop := spinning("Reading " + a.host)
	views, err := transport.Call(a.client, core.List, core.ListParams{Client: a.cfg.Client, Peek: true})
	stop()
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
