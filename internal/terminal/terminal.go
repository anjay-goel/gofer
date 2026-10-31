package terminal

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/anjay-goel/gofer/internal/driver/claude"
	"github.com/anjay-goel/gofer/internal/proc"
	"github.com/anjay-goel/gofer/internal/transport"
)

const rexLimit = 30 * time.Second

func AttachSSH(host, job string) error {
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		return err
	}
	return syscall.Exec(ssh, []string{"ssh", "-t", host, transport.Remote(claude.AttachScript(job))}, os.Environ())
}

func rexRun(rexHost string, args ...string) ([]byte, error) {
	stdout, _, err := proc.Cmd{Name: "rex", Args: append([]string{"-S", rexHost}, args...), Limit: rexLimit}.Run()
	if err != nil {
		return nil, fmt.Errorf("rex: %w", err)
	}
	return []byte(stdout), nil
}

func RexReaches(rexHost string) bool {
	_, err := rexRun(rexHost, "whoami", "--json")
	return err == nil
}

func AttachRex(rexHost, name, job string) (string, error) {
	if !RexReaches(rexHost) {
		return "", fmt.Errorf("rex can't reach %s", rexHost)
	}
	session, err := rexSession(rexHost)
	if err != nil {
		return "", err
	}
	if session == "" {
		session, err = newRexSession(rexHost, name, job)
	} else {
		err = addRexWindow(rexHost, session, name, job)
	}
	if err != nil {
		return "", err
	}
	opened := fmt.Sprintf("Opened %s in the gofer session on Rex host %s.", name, rexHost)
	if _, err := rexRun(rexHost, "do", "session.select", "session_id="+session); err != nil {
		return opened + " Open it from the app, or turn on Remote Control in Rex's settings to jump there.", nil
	}
	return opened, nil
}

func newRexSession(rexHost, name, job string) (string, error) {
	out, err := rexRun(rexHost, "new", "gofer", "--window", name, "--json", "--", "sh", "-c", claude.AttachScript(job))
	if err != nil {
		return "", err
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(out, &created)
	return created.SessionID, nil
}

func addRexWindow(rexHost, session, name, job string) error {
	_, err := rexRun(rexHost, "window", "new", "-s", session, name, "--", "sh", "-c", claude.AttachScript(job))
	return err
}

func rexSession(rexHost string) (string, error) {
	out, err := rexRun(rexHost, "session", "ls", "--json")
	if err != nil {
		return "", err
	}
	var list struct {
		Sessions []struct {
			ID    string `json:"session_id"`
			Label string `json:"label"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return "", err
	}
	for _, s := range list.Sessions {
		if s.Label == "gofer" {
			return s.ID, nil
		}
	}
	return "", nil
}
