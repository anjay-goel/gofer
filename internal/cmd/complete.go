package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/anjay-goel/gofer/internal/config"
	"github.com/anjay-goel/gofer/internal/core"
)

const bashCompletion = `_gofer() {
  local words=("${COMP_WORDS[@]:1:COMP_CWORD}")
  local IFS=$'\n'
  COMPREPLY=($(gofer __complete -- "${words[@]}" 2>/dev/null))
  if [[ ${COMPREPLY[0]} == *: ]]; then
    compopt -o nospace 2>/dev/null
  fi
}
complete -o default -F _gofer gofer
`

const zshCompletion = `_gofer() {
  local -a items
  items=("${(@f)$(gofer __complete -- "${(@)words[2,CURRENT]}" 2>/dev/null)}")
  items=(${items:#})
  if (( ${#items} == 0 )); then
    _files
  elif [[ ${items[1]} == *: ]]; then
    compadd -S '' -- $items
  else
    compadd -- $items
  fi
}
compdef _gofer gofer
`

const fishCompletion = `function __gofer_items
    set -l words (commandline -opc)
    set -e words[1]
    set -l current (commandline -ct)
    gofer __complete -- $words "$current" 2>/dev/null
end
complete -c gofer -f -a '(__gofer_items)'
complete -c gofer -F -n 'not count (__gofer_items) >/dev/null'
`

var (
	completionScripts = map[string]string{"bash": bashCompletion, "zsh": zshCompletion, "fish": fishCompletion}
	flagLine          = regexp.MustCompile(`(?m)^  (--?[a-z][a-z-]*)( <)?`)
)

func completion(args []string) error {
	if len(args) == 1 {
		if script, ok := completionScripts[args[0]]; ok {
			fmt.Print(script)
			return nil
		}
	}
	return errors.New("usage: gofer completion bash|zsh|fish")
}

func namesPath(host string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gofer", url.PathEscape(host)+".names"), nil
}

func saveNames(host string, views []core.View) {
	var names strings.Builder
	for _, v := range views {
		if !strings.ContainsAny(v.Name, " \t\n") {
			names.WriteString(v.Name + "\n")
		}
	}
	path, err := namesPath(host)
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "names-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(names.String())
	if closeErr := tmp.Close(); err == nil && closeErr == nil {
		os.Rename(tmp.Name(), path)
	}
}

func cachedNames(cfg *config.Config, hostFlag string) []string {
	host, _, err := cfg.Select(hostFlag)
	if err != nil {
		return nil
	}
	path, err := namesPath(host)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

func sortedKeys[V any](items map[string]V) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func commandFlags(command string) (flags []string, valued map[string]bool) {
	flags = []string{"--help", "--host"}
	valued = map[string]bool{"--host": true}
	for _, m := range flagLine.FindAllStringSubmatch(commandHelp[command], -1) {
		flags = append(flags, m[1])
		valued[m[1]] = m[2] != ""
	}
	return flags, valued
}

func matching(options []string, partial, suffix string) []string {
	var out []string
	for _, option := range options {
		if strings.HasPrefix(option, partial) {
			out = append(out, option+suffix)
		}
	}
	return out
}

func candidates(cfg *config.Config, words []string) []string {
	if len(words) == 0 {
		return nil
	}
	partial := words[len(words)-1]
	before := words[:len(words)-1]
	if len(before) > 0 && before[len(before)-1] == "--host" {
		return matching(sortedKeys(cfg.Hosts), partial, "")
	}
	before, host := extractHost(before)
	if len(before) == 0 {
		if strings.HasPrefix(partial, "-") {
			return matching([]string{"--help", "--host"}, partial, "")
		}
		return matching(append(sortedKeys(commandHelp), "help", "version"), partial, "")
	}
	command := before[0]
	flags, valued := commandFlags(command)
	positional := 0
	for i := 1; i < len(before); i++ {
		switch word := before[i]; {
		case word == "--":
			return nil
		case valued[word] && i == len(before)-1:
			return nil
		case valued[word]:
			i++
		case !strings.HasPrefix(word, "-"):
			positional++
		}
	}
	if strings.HasPrefix(partial, "-") {
		return matching(flags, partial, "")
	}
	names := func() []string { return cachedNames(cfg, host) }
	switch command {
	case "wait", "rm":
		return matching(names(), partial, "")
	case "logs", "attach", "stop", "send", "diff", "fetch":
		if positional == 0 {
			return matching(names(), partial, "")
		}
	case "cp":
		if partial != "" {
			return matching(names(), partial, ":")
		}
	case "completion":
		if positional == 0 {
			return matching(sortedKeys(completionScripts), partial, "")
		}
	case "help":
		if positional == 0 {
			return matching(sortedKeys(commandHelp), partial, "")
		}
	}
	return nil
}

func printCompletions(args []string) {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	for _, item := range candidates(cfg, args) {
		fmt.Println(item)
	}
}
