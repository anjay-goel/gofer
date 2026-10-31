package cmd

import "slices"

var commandHelp = map[string]string{
	"run": `Usage: gofer run <dir> "<prompt>" [options]

Start an agent in a folder on the dev box.

Options:
  --name <name>     Name the agent
  --doc <file>      Send a UTF-8 handoff document
  --worktree        Hand off your HEAD on a new branch
  --allow-dirty     Hand off despite uncommitted changes
  --model <model>   Choose the model
  --effort <level>  low, medium, high, xhigh or max
  --bypass          Skip permission checks
  --json            Print JSON
`,
	"ls": `Usage: gofer ls [options]

See agents and live sessions, with what needs you first. * marks news since
you last looked.

Options:
  --peek  Leave news marked as unseen
  --json  Print JSON
`,
	"logs": `Usage: gofer logs <name> [-n count] [-f]

Read an agent's conversation, and its follow link while it runs.

Options:
  -n <count>  Entries to show. 30 by default, 0 for all
  -f          Follow until it settles, then exit like gofer wait
`,
	"wait": `Usage: gofer wait <name>... [options]

Block until agents finish or get stuck, then show their work.

Options:
  --any                Return the first agent to settle, like wait -n
  --notify             Raise a desktop alert for each settled agent
  --timeout <seconds>  Stop waiting after this long
  --json               Print JSON

Exit codes:
  0    Finished
  2    Blocked
  3    Turn can't be confirmed finished
  4    Failed or cut short
  124  Timed out

With --any, the code is for the agent it printed. Agents that have already
settled count, so drop that name before waiting for the rest.
`,
	"send": `Usage: gofer send <name> "<message>"

Message an agent, or wake a stopped one. Exits 3 if delivery can't be confirmed.
`,
	"attach": `Usage: gofer attach <name> [options]

Open an agent's full terminal UI. Ctrl+Z detaches.

Options:
  --rex  Open in a Rex tab. Falls back to SSH
  --ssh  Use SSH when Rex is your default
`,
	"cp": `Usage: gofer cp <local> <name>:[path]
       gofer cp <name>:<path> <local>

Copy files or folders to or from an agent. Remote paths start in its folder.
`,
	"stop": `Usage: gofer stop <name>

Stop an agent and keep its conversation.
`,
	"rm": `Usage: gofer rm <name>... [--force]
       gofer rm --finished

Remove agents and their worktrees. Branches stay. Running agents are refused.

Options:
  --force     Remove despite running or uncommitted or unfetched work
  --finished  Remove every finished agent, skipping unsaved or unfinished work
`,
	"init": `Usage: gofer init [host] [options]
       gofer init --remove [host]

Set up a dev box. Asks for anything you leave out.

Options:
  --workdir <dir>     Folder on the dev box where agents work
  --terminal <name>   How attach opens: ssh or rex
  --yes               Skip the Claude install prompt
  --remove            Undo what gofer installed
`,
	"completion": `Usage: gofer completion bash|zsh|fish

Print a completion script. Agent names come from a local list that gofer ls
refreshes, so Tab never connects to the dev box.

Load it from your shell's startup file:
  bash  eval "$(gofer completion bash)"
  zsh   source <(gofer completion zsh)
  fish  gofer completion fish | source
`,
	"doctor": `Usage: gofer doctor [--live]

Check the setup.

Options:
  --live  Run a small test agent in a throwaway folder
`,
}

func wantsHelp(command string, args []string) bool {
	if command == "send" {
		args = args[:min(len(args), 1)]
	}
	if end := slices.Index(args, "--"); end >= 0 {
		args = args[:end]
	}
	return slices.Contains(args, "--help") || slices.Contains(args, "-h")
}
