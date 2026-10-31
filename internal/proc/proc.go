package proc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

var ErrTimeout = errors.New("timed out")

const drain = time.Second

type ExitError struct {
	Code int
	Text string
}

func (e ExitError) Error() string { return e.Text }

type Cmd struct {
	Name  string
	Label string
	Args  []string
	Dir   string
	Env   []string
	Stdin io.Reader
	Limit time.Duration
}

func (c Cmd) label() string {
	if c.Label != "" {
		return c.Label
	}
	if len(c.Args) == 0 {
		return c.Name
	}
	return c.Name + " " + c.Args[0]
}

func (c Cmd) Run() (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.Limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	cmd.Stdin = c.Stdin
	cmd.WaitDelay = drain
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	stdout, stderr = out.String(), errOut.String()
	var exit *exec.ExitError
	switch {
	case errors.Is(err, exec.ErrWaitDelay):
		err = nil
	case err != nil && ctx.Err() != nil:
		err = fmt.Errorf("%s %w after %s", c.label(), ErrTimeout, c.Limit)
	case errors.As(err, &exit):
		err = ExitError{Code: exit.ExitCode(), Text: c.failureText(stdout, stderr, exit.ExitCode())}
	}
	return stdout, stderr, err
}

func (c Cmd) failureText(stdout, stderr string, code int) string {
	if text := strings.TrimSpace(stderr); text != "" {
		return text
	}
	if text := strings.TrimSpace(stdout); text != "" {
		return text
	}
	return fmt.Sprintf("%s exited with %d", c.label(), code)
}

func (c Cmd) Output() (string, error) {
	stdout, _, err := c.Run()
	return strings.TrimSpace(stdout), err
}
