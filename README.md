# gofer

Hand work from your laptop to Claude Code agents on a dev box. Agents keep working while your laptop sleeps. Follow them on your phone, catch up with `gofer ls`, and attach whenever you want.

Gofer drives the real `claude` binary over SSH, using `claude --bg`. It has no server of its own, and it never uses `claude -p` or the Agent SDK.

## Install

You need Go 1.22 or later to build, and SSH key login to your dev box. `--worktree` needs git 2.17 or later on the dev box.

```sh
make install
gofer init
```

`make install` puts `gofer` in `~/.local/bin`. `gofer init` shows a picker built from your SSH config and online Tailscale peers. You can also name the host:

```sh
gofer init odin --root ~/code
```

The host you set up last becomes the default.

Setup copies gofer to `~/.local/share/gofer` on the dev box, installs the gofer skill for Claude Code on your laptop, turns on lingering so agents outlive your login, and runs the `gofer doctor` checks. If lingering needs an admin, it tells you. It says Ready only when Claude is logged in and every check passes. If Claude Code is missing, it shows the official install command and asks first. If Claude isn't logged in, it offers to run `claude auth login` over SSH.

| Option | Effect |
| --- | --- |
| `--root <dir>` | Folder on the dev box that holds your repos. The default is your home folder, and a relative path starts there |
| `--terminal ssh\|rex` | How `gofer attach` opens. `ssh` is the default, and `rex` opens Rex tabs |
| `--rc` | Install a `claude-rc` systemd user service so you can start sessions from your phone. Use `--rc-name` and `--rc-dir` to adjust it. A `claude-rc` that gofer didn't install is left alone |
| `--yes` | Skip the install prompt. Login is always interactive |

`gofer init --remove [host]` undoes only what gofer installed: its copy on the dev box, a `claude-rc` it installed, the host's entry on your laptop, and the skill once no hosts are left. Agent state stays on the dev box, lingering stays on, and so does the `.claude/worktrees/` line that `--worktree` adds to each repo's `.git/info/exclude`.

## Quick start

```sh
gofer run api "fix the flaky auth test" --name auth
gofer wait auth
gofer logs auth
```

`<repo>` is a folder under the root, or an absolute or `~/` path. It must already exist on the dev box.

For a full handoff, commit your work and run this from the repo. Gofer sends your exact HEAD to the dev box, creates `agent/<name>` from it, and starts the agent in a new worktree at `.claude/worktrees/<name>`. Only commits reach the dev box, so gofer refuses uncommitted changes unless you pass `--allow-dirty`.

```sh
gofer run api --doc plan.md --worktree --name auth
git fetch odin:/home/you/code/api agent/auth
```

`gofer run` prints that `git fetch` command. `--doc` works without `--worktree` too, and takes a UTF-8 text file.

Each repo needs Claude's workspace trust on the dev box. Trust on a parent folder doesn't reach into a git repo, and a linked worktree shares its main repo's trust. `gofer doctor` only notes the root folder's trust, because Claude never keeps trust for the home folder, so `gofer run` checks the repo itself before it changes anything. If it refuses, run `claude` in the repo once and accept the prompt.

Names use lowercase letters, digits, dots, dashes or underscores, up to 48 characters. A name is refused if an agent has it, or if a `--worktree` run would need a branch or folder that already exists. Without `--name`, gofer picks a free one. If a start fails, gofer removes only what that run created.

## Commands

| Command | What it does |
| --- | --- |
| `gofer run <repo> "<prompt>"` | Start an agent. Options: `--name`, `--doc`, `--worktree`, `--allow-dirty`, `--model`, `--effort` (`low`, `medium`, `high`, `xhigh` or `max`), `--bypass`, `--json`. Put `--` before prompt text that starts with a dash |
| `gofer ls` | Show gofer's agents and any other live Claude sessions on the dev box. `*` marks an agent with news since this laptop last looked, and CHANGED shows its latest event. SOURCE is `gofer`, `bg`, `tui` or `phone`. `--peek` leaves them unseen, and `--json` prints JSON |
| `gofer logs <name>` | Show recent conversation and tool calls. `-n` sets how many entries, 30 by default, and `-n 0` shows them all |
| `gofer wait <name>...` | Exit when an agent is blocked or all finish, and show each one's work since your last message. Long work is trimmed, and `gofer logs` has the rest. `--timeout <seconds>` sets a limit, and `--json` prints JSON. Reconnects after a dropped connection, outlasts a failed `claude agents` call, and uses no tokens. Stops if SSH login itself fails |
| `gofer send <name> "<msg>"` | Message an agent, and confirm it landed. A running or idle agent gets it on its inbox socket, and a stopped or failed one is woken with it. Refuses to wake an agent whose folder is gone |
| `gofer attach <name>` | Open the full TUI. Ctrl+Z detaches. `--rex` opens a Rex tab, and falls back to SSH if that fails. `--ssh` uses SSH when Rex is your default |
| `gofer cp <file> <name>:` | Copy files or folders to an agent. `gofer cp <name>:<path> <local>` copies them back. Relative remote paths start in the agent's folder |
| `gofer stop <name>` | Stop an agent and keep its conversation. Says so if it was already stopped |
| `gofer rm <name>` | Remove an agent and its worktree. The branch stays. Refuses over uncommitted or unfetched work unless you pass `--force`, so run it from the repo you fetched into. It only removes agents gofer started. A record gofer can't read goes only with `--force`, which leaves its worktree and session unchecked |
| `gofer doctor` | Check the setup. `--live` runs a small test agent in the root folder, so that folder must be trusted |
| `gofer version` | Print the version |

`<name>` can be an agent name, a session's Claude name, a job ID, or the first 8 or more characters of a session ID.

`STATE` is `running`, `idle`, `blocked`, `stopped`, `failed` or `unknown`. An agent that finished its turn shows as `idle`, or as `stopped` once Claude ends the session.

Add `--host <host>` to any command, or set `GOFER_HOST`, to use a dev box other than the default. With `gofer send`, put `--host` before the message, because everything after the message starts is part of it. Put `--` before a message that starts with `--host`.

`gofer wait` exits 0 when every agent finished its turn, 2 when one is blocked, 4 when one failed or was cut short, 3 when an idle agent's turn can't be confirmed finished, and 124 on `--timeout`. If several apply, the first of 2, 4 and 3 wins. `gofer send` exits 3 when it can't confirm delivery. Other errors exit 1.

If the connection drops during `gofer run`, it retries with the same request ID, so the dev box returns the agent it already started instead of starting another. If the retries run out, or the connection drops during `gofer send`, the action may still have happened, so check `gofer ls` or `gofer logs` before running it again. If Claude may have started an agent but gofer can't confirm it, `gofer run` reports that and keeps the agent's record.

## Good to know

- Output uses colour and symbols in a terminal. It stays plain when piped, when `NO_COLOR` is set, or when `TERM` is `dumb`.
- Agents run in `auto` mode. `--bypass` skips permission checks and accepts messages from other sessions, so use it only for work you trust.
- Agents only know what you send them. They can read anything your user can read on the dev box.
- A message can't approve a permission prompt or answer a question dialog, so `gofer send` refuses while an agent is blocked. Attach to clear it. An agent that ended its turn with a question in its reply isn't blocked, so answer that with `gofer send`.
- `claude daemon stop --any` ends running agents. `gofer wait` reports them as cut short, and `gofer send` wakes them again, but interrupted work doesn't finish by itself.
- Sessions started from the phone show as `phone` in `gofer ls`. They can be messaged, but not attached or removed with gofer.
- `gofer run` prints a `follow` link when Claude has linked the session to claude.ai/code. Push notifications, waking an agent from the phone, and phone sync without Remote Control on the dev box are untested.

## How it works

The laptop runs the `gofer` client. Each command runs `gofer rpc` on the dev box over SSH, sending JSON on stdin, then exits. `gofer attach` and `gofer cp` then open their own `ssh` or `scp` connection. Updates only travel over connections the laptop opens.

Remote commands run through `sh`, so your login shell on the dev box can be fish or anything else. Hooks passed through `claude --settings` record session IDs, socket paths and turn events in `~/.local/state/gofer/agents/<name>/`. Gofer never changes your Claude settings or SSH config. Messages use the inbox socket Claude gives each session. A prompt, document or waking message over 100 KB goes in the agent's state folder, and the agent is told to read it. Your laptop keeps its hosts in `~/.config/gofer/config.json`.

## Development

```sh
make test
make build
make dist
```

`make build` embeds Linux binaries in the client, so `gofer init` can install on a Linux dev box from a Mac. The client updates the dev box copy when versions differ, and refreshes the skill on your laptop when it changes.
