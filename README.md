<div align="center">

# gofer

**Hand work from your laptop to Claude Code agents on a dev box.**

They keep working while your laptop sleeps. Follow them on your phone, and attach whenever you want.

<br>

<img src="docs/architecture.svg" alt="The gofer CLI on the laptop reaches gofer rpc on the dev box over SSH, the phone reaches the same agents through Remote Control, and code moves with git." width="820">

</div>

## How it works

Gofer adds no server of its own. It drives the real `claude` binary over SSH, using `claude --bg`, and never uses `claude -p` or the Agent SDK.

- **No daemon:** each command runs `gofer rpc` on the dev box, sending JSON on stdin, then exits.
- **Nothing lost on sleep:** connections only go from the laptop to the dev box, so results wait there until you ask.
- **Your config stays yours:** hooks go in per session through `claude --settings`, so your Claude settings and SSH config never change.
- **Safe retries:** each `run` carries a request ID, so a lost connection can't start a second agent.

## Quick start

You need Go 1.22 or later to build, SSH key login to your dev box, and Claude Code there, which `gofer init` can install. `--worktree` needs git 2.17 or later on the dev box.

```sh
make install
gofer init

gofer run api "fix the flaky auth test" --name auth
gofer wait auth
gofer logs auth
```

```text
✓ Started auth on odin
  folder  /home/you/code/api
  follow  https://claude.ai/code/session_...
```

Your local Claude can do all of this too. `gofer init` installs a skill, so you can just say "hand this off to gofer".

### Hand off your current work

Commit your work, then run this from the repo:

```sh
gofer run api --doc plan.md --worktree --name auth
git fetch odin:/home/you/code/api agent/auth
```

Gofer sends your exact HEAD to the dev box, creates `agent/<name>` from it, and starts the agent in a new worktree at `.claude/worktrees/<name>`. Only commits reach the dev box, so gofer refuses uncommitted changes unless you pass `--allow-dirty`.

`gofer run` prints that `git fetch` command. `--doc` works without `--worktree` too, and takes a UTF-8 text file.

## Commands

| Command | What it does |
| --- | --- |
| `gofer run <repo> "<prompt>"` | Start an agent |
| `gofer ls` | See agents and live sessions |
| `gofer logs <name>` | Read the conversation |
| `gofer wait <name>...` | Block until finished or stuck |
| `gofer send <name> "<msg>"` | Message or wake an agent |
| `gofer attach <name>` | Open the full TUI, Ctrl+Z detaches |
| `gofer cp <file> <name>:` | Copy files to an agent |
| `gofer cp <name>:<path> <local>` | Copy files back |
| `gofer stop <name>` | Stop it, keep the conversation |
| `gofer rm <name>` | Remove it and its worktree, keep the branch |
| `gofer doctor` | Check the setup |

<details>
<summary><b>Options</b></summary>

<br>

| Command | Option | Effect |
| --- | --- | --- |
| `run` | `--name <name>` | Name the agent |
| | `--doc <file>` | Send a UTF-8 handoff document |
| | `--worktree` | Hand off your HEAD on a new branch |
| | `--allow-dirty` | Hand off despite uncommitted changes |
| | `--model`, `--effort` | Pass through to Claude. Effort is `low`, `medium`, `high`, `xhigh` or `max` |
| | `--bypass` | Skip permission checks |
| `ls` | `--peek` | Leave news marked as unseen |
| `logs` | `-n <count>` | Entries to show. 30 by default, `0` for all |
| `wait` | `--timeout <seconds>` | Stop waiting after this long |
| `attach` | `--rex`, `--ssh` | Choose Rex or SSH. Rex falls back to SSH |
| `rm` | `--force` | Remove despite uncommitted or unfetched work |
| `doctor` | `--live` | Run a small test agent in the root folder |
| Any | `--host <host>` | Use another dev box, or set `GOFER_HOST` |
| `run`, `ls`, `wait` | `--json` | Print JSON |

- Put `--` before prompt text that starts with a dash.
- With `gofer send`, put `--host` before the message.

</details>

<details>
<summary><b>Exit codes</b></summary>

<br>

| Code | `gofer wait` | `gofer send` |
| --- | --- | --- |
| 0 | Every agent finished its turn | Delivered |
| 1 | Error | Error |
| 2 | An agent is blocked | |
| 3 | An idle agent's turn can't be confirmed finished | Delivery can't be confirmed |
| 4 | An agent failed or was cut short | |
| 124 | `--timeout` ran out | |

If several apply to `wait`, the first of 2, 4 and 3 wins.

</details>

<details>
<summary><b>Names, states and lost connections</b></summary>

<br>

- **`<name>`** can be an agent name, a session's Claude name, a job ID, or the first 8 or more characters of a session ID.
- **`STATE`** is `running`, `idle`, `blocked`, `stopped`, `failed` or `unknown`. A finished agent shows as `idle`, or `stopped` once Claude ends the session.
- **`SOURCE`** is `gofer`, `bg`, `tui` or `phone`. In `gofer ls`, `*` marks news since this laptop last looked.
- **`gofer cp`** starts relative remote paths in the agent's folder.
- **`gofer wait`** trims long work, so `gofer logs` has the rest. It reconnects after a dropped connection, uses no tokens, and stops if SSH login fails.
- **`gofer send`** refuses to wake an agent whose folder is gone.
- **`gofer rm`** only removes agents gofer started, so run it from the repo you fetched into. A record gofer can't read goes only with `--force`, which leaves its worktree and session unchecked.
- **Lost connections:** `gofer run` retries with the same request ID, so it can't start a second agent. If retries run out, or the connection drops during `gofer send`, the action may still have happened, so check `gofer ls` or `gofer logs` first. If gofer can't confirm a start, it says so and keeps the record.

</details>

<details>
<summary><b>Setup details</b></summary>

<br>

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

</details>

<details>
<summary><b>Repos and names</b></summary>

<br>

`<repo>` is a folder under the root, or an absolute or `~/` path. It must already exist on the dev box.

Each repo needs Claude's workspace trust on the dev box. Trust on a parent folder doesn't reach into a git repo, and a linked worktree shares its main repo's trust. `gofer doctor` only notes the root folder's trust, because Claude never keeps trust for the home folder, so `gofer run` checks the repo itself before it changes anything. If it refuses, run `claude` in the repo once and accept the prompt.

Names use lowercase letters, digits, dots, dashes or underscores, up to 48 characters. A name is refused if an agent has it, or if a `--worktree` run would need a branch or folder that already exists. Without `--name`, gofer picks a free one. If a start fails, gofer removes only what that run created.

</details>

<details>
<summary><b>Good to know</b></summary>

<br>

- Output uses colour and symbols in a terminal. It stays plain when piped, when `NO_COLOR` is set, or when `TERM` is `dumb`.
- Agents run in `auto` mode. `--bypass` skips permission checks and accepts messages from other sessions, so use it only for work you trust.
- Agents only know what you send them. They can read anything your user can read on the dev box.
- A message can't approve a permission prompt or answer a question dialog, so `gofer send` refuses while an agent is blocked. Attach to clear it. An agent that ended its turn with a question in its reply isn't blocked, so answer that with `gofer send`.
- `claude daemon stop --any` ends running agents. `gofer wait` reports them as cut short, and `gofer send` wakes them again, but interrupted work doesn't finish by itself.
- Sessions started from the phone show as `phone` in `gofer ls`. They can be messaged, but not attached or removed with gofer.
- `gofer run` prints a `follow` link when Claude has linked the session to claude.ai/code. Push notifications, waking an agent from the phone, and phone sync without Remote Control on the dev box are untested.

- Remote commands run through `sh`, so your login shell on the dev box can be fish or anything else.
- Messages use the inbox socket Claude gives each session. A prompt, document or waking message over 100 KB goes in the agent's state folder, and the agent is told to read it.
- Agent state lives in `~/.local/state/gofer/agents/<name>/`, and your laptop keeps its hosts in `~/.config/gofer/config.json`.

</details>

## Development

```sh
make test
make build
make dist
```

`make build` embeds Linux binaries in the client, so `gofer init` can install on a Linux dev box from a Mac. The client updates the dev box copy when versions differ, and refreshes the skill on your laptop when it changes.

The code lives in `internal/`. `cmd`, `config`, `transport`, `terminal` and `payload` run on the laptop, `server` and `store` run on the dev box, and `core` holds the types that cross SSH. Only `driver/claude` knows Claude Code's formats.
