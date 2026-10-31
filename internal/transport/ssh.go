package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/payload"
	"github.com/anjay-goel/gofer/internal/proc"
)

const (
	remoteDir = "$HOME/" + core.InstallDir
	remoteBin = `"` + remoteDir + `/gofer"`

	commandLimit = time.Minute
	callLimit    = 5 * time.Minute
	uploadLimit  = 10 * time.Minute
)

var (
	ErrNotInstalled = errors.New("gofer isn't installed on the dev box. Run gofer init")

	errBroken = errors.New("the gofer copy on the dev box can't run. Run gofer init")
)

type NetworkError struct {
	Detail string
}

func (e NetworkError) Error() string { return "can't reach the dev box: " + e.Detail }

var loginFailures = []string{
	"Permission denied",
	"Host key verification failed",
	"REMOTE HOST IDENTIFICATION HAS CHANGED",
	"Too many authentication failures",
}

func (e NetworkError) Retryable() bool {
	for _, failure := range loginFailures {
		if strings.Contains(e.Detail, failure) {
			return false
		}
	}
	return true
}

type RemoteError struct {
	Code string
	Msg  string
}

func (e RemoteError) Error() string { return e.Msg }

type Client struct {
	Host    string
	Version string
}

func (c *Client) Location(path string) string {
	user, host := "", c.Host
	if at := strings.LastIndex(host, "@"); at >= 0 {
		user, host = host[:at+1], host[at+1:]
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return user + host + ":" + path
}

func quote(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'"'"'`) + "'"
}

func Remote(script string) string {
	return "sh -c " + quote(script)
}

func (c *Client) args(extra ...string) []string {
	base := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=2",
		c.Host,
	}
	return append(base, extra...)
}

func (c *Client) exec(limit time.Duration, stdin io.Reader, script string) (stdout, stderr string, err error) {
	cmd := proc.Cmd{Name: "ssh", Label: "ssh to " + c.Host, Args: c.args(Remote(script)), Stdin: stdin, Limit: limit}
	stdout, stderr, err = cmd.Run()
	var exit proc.ExitError
	if errors.As(err, &exit) && exit.Code == 255 {
		return stdout, stderr, NetworkError{Detail: strings.TrimRight(strings.TrimSpace(stderr), ".")}
	}
	return stdout, stderr, err
}

func (c *Client) Output(script string) (string, error) {
	out, _, err := c.exec(commandLimit, nil, script)
	return strings.TrimSpace(out), err
}

func (c *Client) Reachable() error {
	_, err := c.Output("true")
	return err
}

func (c *Client) Interactive(script string) error {
	cmd := exec.Command("ssh", "-t", c.Host, Remote(script))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *Client) LineMode(script string) error {
	cmd := exec.Command("ssh", c.Host, Remote(script))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *Client) Copy(source, dest string) error {
	cmd := exec.Command("scp", "-r", "-q", "-o", "BatchMode=yes", source, dest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func Call[P, R any](c *Client, method core.Method[P, R], params P) (R, error) {
	return CallWithin(c, callLimit, method, params)
}

func CallWithin[P, R any](c *Client, limit time.Duration, method core.Method[P, R], params P) (R, error) {
	var result R
	err := c.call(limit, string(method), params, &result)
	var remote RemoteError
	if errors.Is(err, errBroken) || (errors.As(err, &remote) && remote.Code == core.CodeVersionMismatch) {
		if up := c.Upgrade(); up != nil {
			return result, fmt.Errorf("couldn't update gofer on the dev box: %w", up)
		}
		err = c.call(limit, string(method), params, &result)
	}
	return result, err
}

func (c *Client) call(limit time.Duration, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := json.Marshal(core.Request{Version: c.Version, Method: method, Params: raw})
	if err != nil {
		return err
	}
	out, errOut, err := c.exec(limit, bytes.NewReader(req), remoteBin+" rpc")
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, core.RPCPrefix) {
			continue
		}
		var resp core.Response
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, core.RPCPrefix)), &resp); err != nil {
			return err
		}
		if resp.Error != "" {
			return RemoteError{Code: resp.Code, Msg: resp.Error}
		}
		if len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
	return noReply(method, limit, errOut, err)
}

func noReply(method string, limit time.Duration, errOut string, err error) error {
	var network NetworkError
	if errors.As(err, &network) {
		return err
	}
	if errors.Is(err, proc.ErrTimeout) {
		return NetworkError{Detail: fmt.Sprintf("gofer %s gave no answer within %s", method, limit)}
	}
	var exit proc.ExitError
	if errors.As(err, &exit) {
		switch exit.Code {
		case 126:
			return errBroken
		case 127:
			return ErrNotInstalled
		}
	}
	return fmt.Errorf("no reply from gofer on the dev box: %s. If this keeps happening, run gofer init", strings.TrimSpace(errOut))
}

func (c *Client) platform() (goos, arch string, err error) {
	out, err := c.Output("uname -sm")
	if err != nil {
		return "", "", err
	}
	return parseUname(out)
}

func parseUname(out string) (goos, arch string, err error) {
	lines := strings.Split(out, "\n")
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) != 2 {
		return "", "", fmt.Errorf("unexpected uname output: %s", out)
	}
	goos, arch = strings.ToLower(fields[0]), fields[1]
	switch arch {
	case "x86_64":
		arch = "amd64"
	case "aarch64":
		arch = "arm64"
	}
	return goos, arch, nil
}

func installScript(version string, size int) string {
	return strings.Join([]string{
		`set -e`,
		`d="` + remoteDir + `"`,
		`mkdir -p "$d"`,
		`t=$(mktemp "$d/gofer.XXXXXX")`,
		`trap 'rm -f "$t"' EXIT`,
		`trap "exit 1" HUP INT TERM`,
		`cat > "$t"`,
		fmt.Sprintf(`if [ "$(wc -c < "$t")" -ne %d ]; then echo "the upload was cut short" >&2; exit 1; fi`, size),
		`chmod 755 "$t"`,
		`if [ "$("$t" version)" != ` + quote("gofer "+version) + ` ]; then echo "the new binary didn't run" >&2; exit 1; fi`,
		`mv -f "$t" "$d/gofer"`,
	}, "; ")
}

func (c *Client) Upgrade() error {
	goos, arch, err := c.platform()
	if err != nil {
		return err
	}
	data, err := payload.Binary(goos, arch)
	if err != nil {
		return err
	}
	_, _, err = c.exec(uploadLimit, bytes.NewReader(data), installScript(c.Version, len(data)))
	return err
}
