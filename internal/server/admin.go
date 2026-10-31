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

const systemdLimit = 30 * time.Second

var systemdRuntime = "/run/systemd/system"

func systemdBooted() bool {
	_, err := os.Stat(systemdRuntime)
	return err == nil
}

func (s *Server) dataDir() string {
	return filepath.Join(s.Home, core.InstallDir)
}

func systemd(name string, args ...string) proc.Cmd {
	return proc.Cmd{Name: name, Args: args, Limit: systemdLimit}
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

func lingering() bool {
	out, err := systemd("loginctl", "show-user", os.Getenv("USER"), "-p", "Linger").Output()
	return err == nil && strings.Contains(out, "Linger=yes")
}

func (s *Server) uninstall(struct{}) (core.UninstallResult, error) {
	var result core.UninstallResult
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
		add("working directory", false, root+" doesn't exist")
	} else {
		add("working directory", true, root)
		add("workspace trust", true, "gofer run trusts folders under "+root)
	}
	if systemdBooted() {
		if lingering() {
			add("lingering", true, "")
		} else {
			add("lingering", false, "services stop at logout. Run loginctl enable-linger")
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
