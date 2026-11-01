<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/logo-text-dark.svg">
  <img src="docs/logo-text-light.svg" alt="Gofer" width="300">
</picture>

<br>
<br>

**Hand work from your laptop to Claude Code agents on a dev box.**

They keep working while your laptop sleeps. Follow them from your phone or browser, and attach whenever you want.

<br>

<img src="docs/architecture.svg" alt="The gofer CLI on the laptop reaches gofer rpc on the dev box over SSH, the phone reaches the same agents through Remote Control, and code moves with git." width="820">

</div>

## How it works

- **No server:** each command runs `gofer rpc` over SSH, then exits.
- **Your laptop can sleep:** results wait on the dev box until you ask.
- **Real Claude:** agents are `claude --bg` sessions, never `claude -p` or the Agent SDK.
- **Small footprint:** hooks are per session. Gofer only records Claude's first-run acceptances: repo trust and `--bypass`.

## Quick start

You need Go 1.22+ and SSH key login to your dev box. The laptop can be macOS or Linux, and the dev box Linux. Tested with Claude Code 2.1.316.

```sh
make install
gofer init

gofer run api "fix the flaky auth test" --name auth
gofer wait auth
```

```text
✓ Started auth on devbox
  folder  /home/you/code/api
  follow  https://claude.ai/code/session_...
```

Come back later and one glance tells you what happened. What needs you comes first:

```text
NAME   STATE    CHANGED   COMMITS    SUMMARY
docs   blocked  5m ago               Run gofer attach docs
auth*  idle     2h ago    4 commits  Fixed the refresh race, one test still skipped
api    running  just now  1 commit
```

Or ask your local Claude: `gofer init` installs a skill, so "hand this off to gofer" just works.

`gofer init` then offers to open Claude and set up the box. The `/dev-box-setup` skill asks what you need, then handles repos, dev tools, CLIs, MCP servers, Claude settings and app access one at a time. You can run it again at any time.

### Hand off your current work

Commit, then run this. The agent starts on a new `agent/auth` branch from your exact HEAD.

```sh
gofer run api --doc plan.md --worktree --name auth
```

`gofer run` prints the `git fetch` command that brings the work back.

## Commands

| Command | What it does |
| --- | --- |
| `gofer run <dir> "<prompt>"` | Start an agent |
| `gofer ls` | See outcomes, with what needs you first |
| `gofer logs <name>` | Read the conversation and get the follow link |
| `gofer wait <name>...` | Block until finished or stuck |
| `gofer send <name> "<msg>"` | Message or wake an agent |
| `gofer attach <name>` | Open the full TUI, Ctrl+Z detaches |
| `gofer cp <file> <name>:` | Copy files to an agent |
| `gofer cp <name>:<path> <local>` | Copy files back |
| `gofer stop <name>` | Stop it, keep the conversation |
| `gofer rm <name>...` | Remove them and their worktrees, keep the branches |
| `gofer doctor` | Check the setup |
| `gofer completion bash\|zsh\|fish` | Print a completion script |

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
| | `-f` | Follow until it settles or Ctrl+C. Survives dropped connections |
| `wait` | `--any` | Return the first agent to settle, like `wait -n` |
| | `--notify` | Raise a desktop alert for each settled agent |
| | `--timeout <seconds>` | Stop waiting after this long |
| `attach` | `--rex`, `--ssh` | Choose Rex or SSH. Rex falls back to SSH |
| `rm` | `--force` | Remove despite running, uncommitted or unfetched work |
| | `--finished` | Remove every finished agent. Skips unsaved or unfinished work and says which |
| `doctor` | `--live` | Run a small test agent in a throwaway folder |
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

If several apply to `wait`, the first of 2, 4 and 3 wins. With `--any`, the code is for the agent it printed. Agents that have already settled count, so drop that name before waiting for the rest. `gofer logs -f` exits like `wait`.

</details>

<details>
<summary><b>Shell completion</b></summary>

<br>

Add one of these to your shell's startup file:

```sh
eval "$(gofer completion bash)"
source <(gofer completion zsh)
gofer completion fish | source
```

Agent names come from a local list that `gofer ls` refreshes, so Tab never connects to the dev box.

</details>

<details>
<summary><b>Setup options</b></summary>

<br>

```sh
gofer init devbox --workdir ~/code
```

| Option | Effect |
| --- | --- |
| `--workdir <dir>` | Folder on the dev box where agents work |
| `--terminal ssh\|rex` | How `gofer attach` opens |
| `--yes` | Skip the install prompt |

`gofer init --remove [host]` undoes what gofer installed. Agent state stays on the dev box.

</details>

## Good to know

- **Folders:** `<dir>` must already exist on the dev box. `.` means the one named like your current folder.
- **Trust:** gofer accepts Claude's trust prompt inside your working directory, but not for the home folder itself.
- **Questions:** agents ask in plain text, so `gofer send` can answer. Permission prompts need `gofer attach`.
- **`--bypass`:** skips permission checks. The first use sets `skipDangerousModePermissionPrompt` in your Claude settings.
- **Following:** agents join Remote Control, which needs a claude.ai login, and have push turned on. Phone delivery is untested.
- **`/loop` agents:** these count as running, so give `gofer wait` a `--timeout`.
- **App sessions:** set up by `/dev-box-setup`. They show as `remote`, and can be messaged but not attached.

## Development

```sh
make test
make build
```

See [AGENTS.md](AGENTS.md) for project rules. Only `internal/driver/claude` knows Claude Code's formats.
