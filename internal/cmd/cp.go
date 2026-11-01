package cmd

import (
	"errors"
	"flag"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/transport"
)

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
	r, err := transport.Call(a.client, core.Resolve, core.ResolveParams{Name: name})
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
