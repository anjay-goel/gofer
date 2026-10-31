package cmd

import (
	"bufio"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/anjay-goel/gofer/internal/config"
	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/driver/claude"
	"github.com/anjay-goel/gofer/internal/transport"
	"github.com/anjay-goel/gofer/skill"
)

func isTTY() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

var stdin = bufio.NewReader(os.Stdin)

func readLine() string {
	line, _ := stdin.ReadString('\n')
	return strings.TrimSpace(line)
}

func confirm(question string) bool {
	fmt.Printf("%s [y/N] ", question)
	answer := strings.ToLower(readLine())
	return answer == "y" || answer == "yes"
}

func pickHost() (string, error) {
	if !isTTY() {
		return "", errors.New("no host given and no terminal for the picker. Run gofer init <host>")
	}
	candidates := config.Candidates()
	fmt.Println("Which dev box should gofer use?")
	for i, c := range candidates {
		fmt.Printf("  %d. %s (%s)\n", i+1, c.Host, c.Source)
	}
	manual := len(candidates) + 1
	fmt.Printf("  %d. Enter a host yourself\n", manual)
	fmt.Print("Choice: ")
	n, err := strconv.Atoi(readLine())
	if err != nil || n < 1 || n > manual {
		return "", errors.New("that isn't one of the choices")
	}
	if n < manual {
		return candidates[n-1].Host, nil
	}
	fmt.Print("Host: ")
	host := readLine()
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

func skillPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills", "gofer", "SKILL.md"), nil
}

func syncSkill() error {
	path, err := skillPath()
	if err != nil {
		return err
	}
	if data, err := os.ReadFile(path); err == nil && string(data) == skill.Text {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(skill.Text), 0o644)
}

func (a *app) init(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	remove := fs.Bool("remove", false, "")
	yes := fs.Bool("yes", false, "")
	root := fs.String("root", "", "")
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
	entry, err := updateHost(cfg, host, *root, *term)
	if err != nil {
		return err
	}
	if err := syncSkill(); err != nil {
		return err
	}
	fmt.Println("Installed the gofer skill for Claude Code on this machine")
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
	checks, err := transport.Call(client, core.Doctor, core.DoctorParams{Root: entry.Root})
	if err != nil {
		return err
	}
	allOK := printChecks(checks)
	if !loggedIn {
		return exitCode{1, "Setup finished, but Claude isn't logged in on " + host + ". Run gofer init " + host + " in a terminal to log in"}
	}
	if !allOK {
		return exitCode{1, "Setup finished, but fix the FAIL checks above before using " + host}
	}
	fmt.Println("Ready. Try: gofer run <repo> \"<prompt>\"")
	return nil
}

func setUpBox(c *transport.Client, yes bool) (loggedIn bool, err error) {
	fmt.Printf("Checking %s\n", c.Host)
	if err := c.Reachable(); err != nil {
		return false, fmt.Errorf("can't reach %s with SSH. Check the host name and that key login works.\n%w", c.Host, err)
	}
	if err := ensureClaude(c, yes); err != nil {
		return false, err
	}
	if loggedIn, err = ensureLogin(c); err != nil {
		return false, err
	}
	if err := c.Upgrade(); err != nil {
		return false, fmt.Errorf("couldn't install gofer on %s: %w", c.Host, err)
	}
	fmt.Println("Installed gofer on", c.Host)
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
	result, err := transport.Call(c, core.RCInstall, params)
	if err != nil {
		return err
	}
	fmt.Println(result.Note)
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
		if !confirm("Run it on " + c.Host + "?") {
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
	fmt.Printf("Claude Code isn't logged in on %s.\n", c.Host)
	if !isTTY() {
		fmt.Printf("Login needs you at a terminal. Run: ssh -t %s %s\n", c.Host, transport.Remote(claude.LoginScript))
		return false, nil
	}
	if !confirm("Log in now?") {
		return false, nil
	}
	if err := c.Interactive(claude.LoginScript); err != nil {
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
		path, err := skillPath()
		if err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Dir(path)); err != nil {
			return err
		}
		fmt.Println("Removed", filepath.Dir(path))
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

func printChecks(checks []core.Check) bool {
	allOK := true
	for _, c := range checks {
		mark := "ok  "
		switch {
		case c.Optional:
			mark = "note"
		case !c.OK:
			mark = "FAIL"
			allOK = false
		}
		fmt.Printf("%s %s", mark, c.Name)
		if c.Detail != "" {
			fmt.Printf(": %s", c.Detail)
		}
		fmt.Println()
	}
	return allOK
}
