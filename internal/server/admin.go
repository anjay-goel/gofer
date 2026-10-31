package server

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/proc"
)

const (
	systemdLimit = 30 * time.Second
	unitMarker   = "(installed by gofer)"
)

var systemdRuntime = "/run/systemd/system"

func systemdBooted() bool {
	_, err := os.Stat(systemdRuntime)
	return err == nil
}

func (s *Server) dataDir() string {
	return filepath.Join(s.Home, core.InstallDir)
}

func (s *Server) unitPath() string {
	return filepath.Join(s.Home, ".config", "systemd", "user", "claude-rc.service")
}

func (s *Server) ownsUnit() bool {
	data, err := os.ReadFile(s.unitPath())
	return err == nil && strings.Contains(string(data), unitMarker)
}

func systemd(name string, args ...string) proc.Cmd {
	return proc.Cmd{Name: name, Args: args, Limit: systemdLimit}
}

func systemctl(args ...string) (string, error) {
	return systemd("systemctl", append([]string{"--user"}, args...)...).Output()
}

func (s *Server) unit(name, dir string) string {
	return fmt.Sprintf(`[Unit]
Description=Claude Code Remote Control server %s
After=network-online.target
StartLimitIntervalSec=0

[Service]
WorkingDirectory=%s
Environment=PATH=%s
ExecStart=/usr/bin/script -qfec "%s" /dev/null
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
`, unitMarker, dir, s.Driver.ServicePath(), s.Driver.RemoteControlCommand(name))
}

func (s *Server) linger(struct{}) (core.NoteResult, error) {
	var result core.NoteResult
	if !systemdBooted() || lingering() {
		return result, nil
	}
	if _, err := systemd("loginctl", "enable-linger").Output(); err != nil {
		result.Note = "Lingering is off and needs an admin, so agents stop at logout: " + err.Error()
		return result, nil
	}
	result.Note = "Turned on lingering, so agents keep running after logout"
	return result, nil
}

func (s *Server) rcInstall(p core.RCParams) (core.NoteResult, error) {
	var result core.NoteResult
	if !nameRe.MatchString(p.Name) {
		return result, fmt.Errorf("invalid --rc-name %q: %s", p.Name, nameRule)
	}
	if !systemdBooted() {
		result.Note = "this dev box isn't running systemd, so the Remote Control service was skipped"
		return result, nil
	}
	if _, err := os.Stat("/usr/bin/script"); err != nil {
		return result, fmt.Errorf("the Remote Control service needs /usr/bin/script")
	}
	if _, err := os.Stat(s.unitPath()); err == nil && !s.ownsUnit() {
		result.Note = "claude-rc already exists and isn't managed by gofer, so it was left alone"
		return result, nil
	}
	if !s.Driver.Installed() {
		return result, fmt.Errorf("claude isn't installed on the dev box")
	}
	dir := s.expand(p.Dir)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return result, fmt.Errorf("%s doesn't exist on the dev box", dir)
	}
	if err := os.MkdirAll(filepath.Dir(s.unitPath()), 0o755); err != nil {
		return result, err
	}
	unit := s.unit(p.Name, dir)
	installed, _ := os.ReadFile(s.unitPath())
	if err := os.WriteFile(s.unitPath(), []byte(unit), 0o644); err != nil {
		return result, err
	}
	if _, err := systemctl("daemon-reload"); err != nil {
		return result, fmt.Errorf("daemon-reload failed: %w", err)
	}
	if _, err := systemctl("enable", "claude-rc.service"); err != nil {
		return result, fmt.Errorf("couldn't enable claude-rc: %w", err)
	}
	start := "start"
	if string(installed) != unit {
		start = "restart"
	}
	if _, err := systemctl(start, "claude-rc.service"); err != nil {
		return result, fmt.Errorf("couldn't start claude-rc: %w", err)
	}
	result.Note = "claude-rc is installed and running"
	return result, nil
}

func lingering() bool {
	out, err := systemd("loginctl", "show-user", os.Getenv("USER"), "-p", "Linger").Output()
	return err == nil && strings.Contains(out, "Linger=yes")
}

func (s *Server) uninstall(struct{}) (core.UninstallResult, error) {
	var result core.UninstallResult
	if s.ownsUnit() {
		systemctl("disable", "--now", "claude-rc.service")
		if err := os.Remove(s.unitPath()); err != nil && !os.IsNotExist(err) {
			return result, err
		}
		systemctl("daemon-reload")
		result.Removed = append(result.Removed, "claude-rc service")
	} else if _, err := os.Stat(s.unitPath()); err == nil {
		result.Kept = append(result.Kept, "claude-rc service, which gofer didn't install")
	}
	if err := os.RemoveAll(s.dataDir()); err != nil {
		return result, err
	}
	result.Removed = append(result.Removed, s.dataDir())
	result.Kept = append(result.Kept, s.Store.Root)
	return result, nil
}

func (s *Server) doctor(p core.DoctorParams) ([]core.Check, error) {
	var checks []core.Check
	add := func(name string, ok bool, detail string) {
		checks = append(checks, core.Check{Name: name, OK: ok, Detail: detail})
	}
	note := func(name, detail string) {
		checks = append(checks, core.Check{Name: name, Optional: true, Detail: detail})
	}
	if !s.Driver.Installed() {
		add("claude installed", false, "not found on the dev box")
		return checks, nil
	}
	version, err := s.Driver.Version()
	add("claude installed", err == nil, version)
	if err := s.Driver.SupportsBackground(); err != nil {
		add("background agents", false, err.Error())
	} else {
		add("background agents", true, "")
	}
	loggedIn, detail := s.Driver.LoggedIn()
	add("claude login", loggedIn, detail)
	gitVersion, gitErr := git("", "--version")
	switch {
	case gitErr != nil:
		add("git", false, "not found on the dev box. --worktree needs it")
	case tooOldGit(gitVersion):
		add("git", false, gitVersion+" is too old. --worktree needs 2.17 or later")
	default:
		add("git", true, "")
	}
	root := s.expand(p.Root)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		add("repo root", false, root+" doesn't exist")
	} else {
		add("repo root", true, root)
		if s.trusted(root) {
			add("workspace trust", true, root)
		} else {
			note("workspace trust", s.trustAdvice(root)+". gofer run checks each repo itself")
		}
	}
	if systemdBooted() {
		if lingering() {
			add("lingering", true, "")
		} else {
			add("lingering", false, "services stop at logout. Run loginctl enable-linger")
		}
		state, _ := systemctl("is-active", "claude-rc.service")
		switch {
		case state == "active":
			add("remote control service", true, "claude-rc is active")
		case s.ownsUnit():
			add("remote control service", false, "claude-rc is "+state+", and gofer installed it. Check journalctl --user -u claude-rc")
		default:
			note("remote control service", "claude-rc is "+state+". It's only needed to start sessions from the phone, and gofer init --rc installs it")
		}
	}
	return checks, nil
}

func tooOldGit(version string) bool {
	var major, minor int
	if _, err := fmt.Sscanf(version, "git version %d.%d", &major, &minor); err != nil {
		return false
	}
	return slices.Compare([]int{major, minor}, []int{2, 17}) < 0
}
