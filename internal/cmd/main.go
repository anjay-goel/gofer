package cmd

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anjay-goel/gofer/internal/config"
	"github.com/anjay-goel/gofer/internal/server"
	"github.com/anjay-goel/gofer/internal/transport"
)

const usage = `gofer hands work to Claude Code agents on a dev box.

Start and follow:
  gofer run <dir> "<prompt>" [--name n] [--model m] [--effort e] [--bypass] [--json]
  gofer run <dir> --doc plan.md --worktree [--name n] [--allow-dirty] [--json]
  gofer ls [--peek] [--json]
  gofer logs <name> [-n lines]
  gofer wait <name>... [--timeout seconds] [--json]

Interact and manage:
  gofer send <name> "<message>"
  gofer attach <name> [--rex] [--ssh]
  gofer cp <local> <name>:[path]
  gofer cp <name>:<path> <local>
  gofer stop <name>
  gofer rm <name> [--force]

Set up:
  gofer init [host] [--workdir dir] [--terminal ssh|rex] [--yes]
  gofer init --remove [host]
  gofer doctor [--live]
  gofer version

Use --host <host> or GOFER_HOST with any command to pick a dev box other than
the default. With gofer send, put --host before the message, and put -- before
a message that starts with --host.
`

const (
	exitBlocked     = 2
	exitUnconfirmed = 3
	exitFailed      = 4
	exitTimedOut    = 124
)

type exitCode struct {
	code int
	msg  string
}

func (e exitCode) Error() string { return e.msg }

type app struct {
	version  string
	hostFlag string
	host     string
	cfg      *config.Config
	hostCfg  config.Host
	client   *transport.Client
}

func Main(version string, args []string) int {
	a := &app{version: version}
	args, a.hostFlag = extractHost(args)
	if len(args) == 0 {
		fmt.Print(styledHelp(usage))
		return 0
	}
	name, rest := args[0], args[1:]
	if text, ok := commandHelp[name]; ok && wantsHelp(name, rest) {
		fmt.Print(styledHelp(text))
		return 0
	}
	var err error
	switch name {
	case "rpc":
		return a.serve()
	case "hook":
		a.hook(rest)
		return 0
	case "version", "--version":
		fmt.Println("gofer", version)
		return 0
	case "help", "--help", "-h":
		text, ok := commandHelp[strings.Join(rest, " ")]
		if !ok {
			text = usage
		}
		fmt.Print(styledHelp(text))
		return 0
	case "init":
		err = a.init(rest)
	case "run":
		err = a.withClient(a.run, rest)
	case "ls":
		err = a.withClient(a.ls, rest)
	case "logs":
		err = a.withClient(a.logs, rest)
	case "wait":
		err = a.withClient(a.wait, rest)
	case "send":
		err = a.withClient(a.send, rest)
	case "attach":
		err = a.withClient(a.attach, rest)
	case "cp":
		err = a.withClient(a.cp, rest)
	case "stop":
		err = a.withClient(a.stop, rest)
	case "rm":
		err = a.withClient(a.rm, rest)
	case "doctor":
		err = a.withClient(a.doctor, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", name, usage)
		return 1
	}
	if err == nil {
		return 0
	}
	var code exitCode
	if errors.As(err, &code) {
		if code.msg != "" {
			fmt.Fprintln(os.Stderr, code.msg)
		}
		return code.code
	}
	fmt.Fprintln(os.Stderr, errorPrefix(), err)
	return 1
}

func extractHost(args []string) ([]string, string) {
	var out []string
	host := ""
	for i := 0; i < len(args); i++ {
		inMessage := len(out) > 2 && out[0] == "send"
		if args[i] == "--" || inMessage {
			return append(out, args[i:]...), host
		}
		switch {
		case args[i] == "--host" && i+1 < len(args):
			host = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--host="):
			host = strings.TrimPrefix(args[i], "--host=")
		default:
			out = append(out, args[i])
		}
	}
	return out, host
}

func (a *app) serve() int {
	srv, err := server.New(a.version)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	srv.Serve(os.Stdin, os.Stdout)
	return 0
}

func (a *app) hook(args []string) {
	if len(args) != 1 {
		return
	}
	if srv, err := server.New(a.version); err == nil {
		srv.Hook(args[0], os.Stdin)
	}
}

func (a *app) newClient(host string) *transport.Client {
	return &transport.Client{Host: host, Version: a.version}
}

func (a *app) withClient(fn func([]string) error, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	host, hostCfg, err := cfg.Select(a.hostFlag)
	if err != nil {
		return err
	}
	if err := syncSkill(); err != nil {
		fmt.Fprintln(os.Stderr, "gofer: couldn't update the gofer skill:", err)
	}
	a.cfg, a.host, a.hostCfg = cfg, host, hostCfg
	a.client = a.newClient(host)
	return fn(args)
}

func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var positional []string
	for {
		err := fs.Parse(args)
		if errors.Is(err, flag.ErrHelp) {
			fmt.Print(styledHelp(commandHelp[fs.Name()]))
			return nil, exitCode{0, ""}
		}
		if err != nil {
			return nil, err
		}
		rest := fs.Args()
		consumed := args[:len(args)-len(rest)]
		if len(rest) == 0 || (len(consumed) > 0 && consumed[len(consumed)-1] == "--") {
			return append(positional, rest...), nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
