package cmd

import (
	"bufio"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/anjay-goel/gofer/internal/config"
	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/driver/claude"
	"github.com/anjay-goel/gofer/internal/transport"
	"github.com/anjay-goel/gofer/skill"
)

func isTTY() bool {
	return isTerminal(os.Stdin)
}

var stdin = bufio.NewReader(os.Stdin)

func readLine() string {
	line, _ := stdin.ReadString('\n')
	return strings.TrimSpace(line)
}

func askRoot(cfg *config.Config, host string) string {
	if !isTTY() || cfg.Hosts[host].Root != "" {
		return ""
	}
	return askText("Working directory on "+host, "~")
}

func pickHost() (string, error) {
	if !isTTY() {
		return "", errors.New("no host given and no terminal for the picker. Run gofer init <host>")
	}
	candidates := config.Candidates()
	options := make([]choice, 0, len(candidates)+1)
	for _, c := range candidates {
		options = append(options, choice{c.Host, c.Source})
	}
	options = append(options, choice{label: "Enter a host yourself"})
	picked, err := choose("Which dev box should gofer use?", options)
	if err != nil {
		return "", err
	}
	if picked < len(candidates) {
		return candidates[picked].Host, nil
	}
	host := askText("Host", "")
	if host == "" {
		return "", errors.New("no host entered")
	}
	return host, nil
}

func tildePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+"/") {
		return "~/" + strings.TrimPrefix(path, home+"/")
	}
	return path
}

func skillPath(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills", name, "SKILL.md"), nil
}

func syncSkill() error {
	for _, s := range skill.All {
		path, err := skillPath(s.Name)
		if err != nil {
			return err
		}
		if data, err := os.ReadFile(path); err == nil && string(data) == s.Text {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(s.Text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) init(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	remove := fs.Bool("remove", false, "")
	yes := fs.Bool("yes", false, "")
	root := fs.String("workdir", "", "")
	term := fs.String("terminal", "", "")
	rc := fs.Bool("rc", false, "")
	rcName := fs.String("rc-name", "", "")
	rcDir := fs.String("rc-dir", "", "")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return errors.New("usage: gofer init [host]")
	}
	if *term != "" && *term != "ssh" && *term != "rex" {
		return errors.New("--terminal must be ssh or rex")
	}
	host := config.Requested(a.hostFlag)
	if len(pos) == 1 {
		host = pos[0]
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *remove {
		return a.uninstall(cfg, cmp.Or(host, cfg.DefaultHost))
	}
	if styled {
		fmt.Printf("%s %s\n\n", paint(bold, "gofer"), paint(dim, "setup"))
	}
	if host == "" {
		if host, err = pickHost(); err != nil {
			return err
		}
	}
	if strings.Contains(host, "://") {
		return fmt.Errorf("gofer needs a host name, not the URL %s. Use a name, or add a Host entry to your SSH config", host)
	}
	client := a.newClient(host)
	loggedIn, err := setUpBox(client, *yes)
	if err != nil {
		return err
	}
	entry, err := updateHost(cfg, host, cmp.Or(*root, askRoot(cfg, host)), *term)
	if err != nil {
		return err
	}
	if err := syncSkill(); err != nil {
		return err
	}
	done("Installed the gofer skills for Claude Code on this machine")
	linger, err := transport.Call(client, core.Linger, struct{}{})
	if err != nil {
		return err
	}
	if linger.Note != "" {
		fmt.Println(linger.Note)
	}
	if *rc {
		if err := installRC(client, host, *rcName, cmp.Or(*rcDir, entry.Root)); err != nil {
			return err
		}
	}
	stop := spinning("Checking " + host)
	checks, err := transport.Call(client, core.Doctor, core.DoctorParams{Root: entry.Root})
	stop()
	if err != nil {
		return err
	}
	allOK := printCheckSummary(checks)
	if !loggedIn {
		return exitCode{1, "Setup finished, but Claude isn't logged in on " + host + ". Run gofer init " + host + " in a terminal to log in"}
	}
	if !allOK {
		return exitCode{1, "Setup finished, but fix the FAIL checks above before using " + host}
	}
	fmt.Println()
	done("%s Start your first agent:", paint(bold, "Ready."))
	fmt.Printf("  %s\n", paint(cyan, `gofer run <dir> "<prompt>"`))
	return offerBoxSetup(host)
}

func offerBoxSetup(host string) error {
	path, err := exec.LookPath("claude")
	if err != nil || !interactive() {
		return nil
	}
	fmt.Println()
	setUp, err := confirm("Set up tools and MCP servers on " + host + " with Claude?")
	if err != nil || !setUp {
		return err
	}
	return syscall.Exec(path, []string{"claude", "/dev-box-setup " + host}, os.Environ())
}

func setUpBox(c *transport.Client, yes bool) (loggedIn bool, err error) {
	stop := spinning("Connecting to " + c.Host)
	err = c.Reachable()
	stop()
	if !styled {
		fmt.Printf("Checking %s\n", c.Host)
	}
	if err != nil {
		return false, fmt.Errorf("can't reach %s with SSH. Check the host name and that key login works.\n%w", c.Host, err)
	}
	if err := ensureClaude(c, yes); err != nil {
		return false, err
	}
	if loggedIn, err = ensureLogin(c); err != nil {
		return false, err
	}
	stop = spinning("Installing gofer on " + c.Host)
	err = c.Upgrade()
	stop()
	if err != nil {
		return false, fmt.Errorf("couldn't install gofer on %s: %w", c.Host, err)
	}
	done("Installed gofer on %s", c.Host)
	return loggedIn, nil
}

func updateHost(cfg *config.Config, host, root, term string) (config.Host, error) {
	entry := cfg.Hosts[host]
	if root != "" {
		entry.Root = tildePath(root)
	}
	if entry.Root == "" {
		entry.Root = "~"
	}
	if term != "" {
		entry.Terminal = term
	}
	cfg.Hosts[host] = entry
	cfg.DefaultHost = host
	return entry, cfg.Save()
}

func installRC(c *transport.Client, host, name, dir string) error {
	params := core.RCParams{Name: cmp.Or(name, rcName(host)), Dir: tildePath(dir)}
	stop := spinning("Installing Remote Control on " + host)
	result, err := transport.Call(c, core.RCInstall, params)
	stop()
	if err != nil {
		return err
	}
	done("%s", result.Note)
	return nil
}

func rcName(host string) string {
	host = host[strings.LastIndex(host, "@")+1:]
	if net.ParseIP(host) != nil {
		return strings.ReplaceAll(host, ":", "-")
	}
	return strings.ToLower(strings.SplitN(host, ".", 2)[0])
}

func ensureClaude(c *transport.Client, yes bool) error {
	if _, err := c.Output(claude.ProbeScript); err == nil {
		return nil
	}
	fmt.Printf("Claude Code isn't installed on %s.\nThe official install command is:\n  %s\n", c.Host, claude.InstallScript)
	if !yes {
		if !isTTY() {
			return errors.New("not installing without confirmation. Run with --yes, or install Claude Code first")
		}
		install, err := confirm("Run it on " + c.Host + "?")
		if err != nil {
			return err
		}
		if !install {
			return errors.New("gofer needs Claude Code on the dev box")
		}
	}
	if err := c.Interactive(claude.InstallScript); err != nil {
		return fmt.Errorf("the Claude Code install failed: %w", err)
	}
	if _, err := c.Output(claude.ProbeScript); err != nil {
		return errors.New("Claude Code still isn't on the dev box after the install")
	}
	return nil
}

func loggedIn(c *transport.Client) bool {
	out, _ := c.Output(claude.AuthStatusScript)
	ok, _, _ := claude.ParseAuth(out)
	return ok
}

func ensureLogin(c *transport.Client) (bool, error) {
	if loggedIn(c) {
		return true, nil
	}
	if !isTTY() {
		fmt.Printf("Claude Code isn't logged in on %s. Run gofer init %s in a terminal to log in.\n", c.Host, c.Host)
		return false, nil
	}
	fmt.Printf("Log in to Claude on %s: open the link, then paste the code.\n", c.Host)
	if err := c.LineMode(claude.LoginScript); err != nil {
		return false, fmt.Errorf("claude auth login failed: %w", err)
	}
	return loggedIn(c), nil
}

func (a *app) uninstall(cfg *config.Config, host string) error {
	if host == "" {
		return errors.New("no dev box set up")
	}
	client := a.newClient(host)
	result, err := transport.Call(client, core.Uninstall, struct{}{})
	if err != nil {
		if !errors.Is(err, transport.ErrNotInstalled) {
			return err
		}
		fmt.Println("gofer wasn't installed on", host)
	}
	for _, item := range result.Removed {
		fmt.Println("Removed", item)
	}
	for _, item := range result.Kept {
		fmt.Println("Kept", item)
	}
	delete(cfg.Hosts, host)
	if len(cfg.Hosts) == 0 {
		for _, s := range skill.All {
			path, err := skillPath(s.Name)
			if err != nil {
				return err
			}
			if err := os.RemoveAll(filepath.Dir(path)); err != nil {
				return err
			}
			fmt.Println("Removed", filepath.Dir(path))
		}
	}
	if cfg.DefaultHost == host {
		cfg.DefaultHost = ""
		for name := range cfg.Hosts {
			cfg.DefaultHost = name
			break
		}
	}
	return cfg.Save()
}

func checkKind(c core.Check) string {
	if !c.OK {
		return "FAIL"
	}
	return "ok"
}

func printCheck(c core.Check) {
	fmt.Printf("%s %s", checkMark(checkKind(c)), c.Name)
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
	return checkKind(c) == "FAIL"
}

func printCheckSummary(checks []core.Check) bool {
	if !styled {
		return printChecks(checks)
	}
	passed := 0
	for _, c := range checks {
		if checkKind(c) == "ok" {
			passed++
		} else {
			printCheck(c)
		}
	}
	done("%d checks passed", passed)
	return !slices.ContainsFunc(checks, failed)
}
