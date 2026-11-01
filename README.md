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
```

`gofer init` sets up the box and installs skills for Claude Code, then offers to run `/dev-box-setup` for your tools, CLIs and MCP servers.

Then just ask Claude:

```text
Hand this off to gofer and tell me when it's done.
Use gofer to run the full test suite on the dev box.
Start gofer agents to fix the lint errors in api and web.
What are my gofer agents doing?
```

Come back later and `gofer ls` shows what happened, with what needs you first:

```text
NAME   STATE    CHANGED   COMMITS    SUMMARY
docs   blocked  5m ago               Run gofer attach docs
auth*  idle     2h ago    4 commits  Fixed the refresh race, one test still skipped
api    running  just now  1 commit
```

### By hand

```sh
gofer run api "fix the flaky auth test" --name auth
gofer run api --doc plan.md --worktree --name auth
gofer wait auth
```

`--worktree` sends your committed HEAD to a new `agent/auth` branch, and `gofer run` prints the `git fetch` command that brings the work back.

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

Run `gofer <command> --help` for options and exit codes.

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
