<div align="center">

# gofer

**Hand work from your laptop to Claude Code agents on a dev box.**

They keep working while your laptop sleeps. Follow them on your phone, and attach whenever you want.

<br>

<img src="docs/architecture.svg" alt="The gofer CLI on the laptop reaches gofer rpc on the dev box over SSH, the phone reaches the same agents through Remote Control, and code moves with git." width="820">

</div>

## How it works

- **No server:** each command runs `gofer rpc` over SSH, then exits.
- **Your laptop can sleep:** results wait on the dev box until you ask.
- **Real Claude:** agents are `claude --bg` sessions, never `claude -p` or the Agent SDK.
- **Small footprint:** hooks are per session. Gofer only records Claude's first-run acceptances: repo trust, `--bypass` and Remote Control.

## Quick start

You need Go 1.22+ and SSH key login to your dev box. The laptop can be macOS or Linux, and the dev box Linux. Tested with Claude Code 2.1.316.

```sh
make install
gofer init

gofer run api "fix the flaky auth test" --name auth
gofer wait auth
```

```text
✓ Started auth on odin
  folder  /home/you/code/api
  follow  https://claude.ai/code/session_...
```

Or ask your local Claude: `gofer init` installs a skill, so "hand this off to gofer" just works.

Run `/gofer-setup` in Claude Code to set up the box. It asks what you need, then handles repos, dev tools, CLIs, MCP servers and Claude settings one at a time.

### Hand off your current work

Commit, then run this. The agent starts on a new `agent/auth` branch from your exact HEAD.

```sh
gofer run api --doc plan.md --worktree --name auth
```

`gofer run` prints the `git fetch` command that brings the work back.

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
<summary><b>Setup options</b></summary>

<br>

```sh
gofer init odin --root ~/code
```

| Option | Effect |
| --- | --- |
| `--root <dir>` | Folder that holds your repos |
| `--terminal ssh\|rex` | How `gofer attach` opens |
| `--rc` | Install the persistent Remote Control service without asking |
| `--yes` | Skip the install prompt |

`gofer init --remove [host]` undoes what gofer installed. Agent state stays on the dev box.

</details>

## Good to know

- **Repos:** `<repo>` is a folder under your root, or a full path. It must already exist on the dev box.
- **Trust:** gofer accepts Claude's trust prompt for repos under your root. Anywhere else, run `claude` there once and accept it yourself.
- **Blocked agents:** a message can't approve a prompt, so use `gofer attach`.
- **Bypass:** `--bypass` skips permission checks, so use it only for work you trust. The first use sets `skipDangerousModePermissionPrompt` in `~/.claude/settings.json`, which background sessions require.
- **Phone:** following agents needs a claude.ai login. Gofer turns on Remote Control for its own agents.
- **Phone sessions:** these show in `gofer ls` and can be messaged, but not attached or removed.
- **Lost connections:** `gofer run` can't start a second agent on retry. If `gofer send` drops, check `gofer logs` before resending.
- **Untested:** phone push notifications and waking an agent from the phone.

## Development

```sh
make test
make build
```

See [AGENTS.md](AGENTS.md) for project rules. Only `internal/driver/claude` knows Claude Code's formats.
