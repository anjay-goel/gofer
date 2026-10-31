---
name: gofer
description: Delegate work to Claude Code agents on a dev box with the gofer CLI. Use when work should run on the dev box, outlive this session or the laptop being awake, or when the user mentions gofer, handing off, or remote agents.
---

# Gofer

Gofer runs Claude Code agents on a dev box. They keep working while the laptop sleeps, and the user can follow them on their phone at claude.ai/code.

## Start here

Run `gofer ls` at the start of any session that touches dev box work, so you see what finished, what is blocked and what changed. It marks those changes as seen, so use `gofer ls --peek` if you only want a look.

A `*` marks an agent with news. There is no finished state: an agent that finished its turn shows as `idle`, with `turn finished` under CHANGED.

## When to delegate

- Use gofer for long jobs, work that needs the dev box, and anything that should outlive this session.
- Use built-in subagents for quick work on this laptop. They are simpler and return results by themselves.

## Starting agents

Quick agent, in a folder on the dev box. `<repo>` is a folder under the configured root, or an absolute or `~/` path, and it must already exist there:

```sh
gofer run <repo> "<prompt>" --name <short-name>
```

Put `--` before prompt text that starts with a dash. `--model` and `--effort` (`low`, `medium`, `high`, `xhigh` or `max`) pass through to Claude.

Full handoff from the current repo, on a new `agent/<name>` branch at your exact HEAD. Only commits reach the dev box, so gofer refuses uncommitted changes. Commit first, and only pass `--allow-dirty` when the user asks for it:

```sh
git status
gofer run <repo> --doc plan.md --worktree --name <short-name>
```

An agent only knows what you send it. It can't see this conversation or this laptop. Write a complete prompt: the goal, the relevant paths and branch, how to verify the work, and what to report back.

Agents run in `auto` mode. Only pass `--bypass` when the user asks for it.

`gofer run` refuses a repo Claude doesn't trust yet, so tell the user to run `claude` there once on the dev box. It also refuses a `--name` that an agent, an `agent/<name>` branch or a worktree folder already has, so pick another. Names use lowercase letters, digits, dots, dashes or underscores, up to 48 characters.

## Waiting

Run `gofer wait` as a background command and carry on with other work. It uses no tokens, reconnects after a dropped connection, and accepts several names.

```sh
gofer wait <name> <name2>
```

- Exit 0 means every agent finished its turn. It prints the work since your last message, so read all of it, not just the end. `gofer logs <name>` has the rest.
- Exit 2 means an agent is blocked.
- Exit 3 means an agent is idle but its turn can't be confirmed finished, so what it printed may be stale. Check `gofer logs <name>` before relying on it.
- Exit 4 means an agent failed or was cut short, so its work is unfinished. Read what it printed, then use `gofer send` to wake it or tell the user.
- Exit 124 means `--timeout` ran out.
- Exit 1 is an error, such as SSH login failing or an unknown name. Read its message.

If several apply, the first of 2, 4 and 3 wins.

Never poll with repeated `gofer ls` or `gofer logs` calls, because each one spends tokens.

Finished means the agent's turn ended. Read what it printed to see whether it completed the work or stopped to ask a question.

## Messaging

```sh
gofer send <name> "<message>"
```

This works for running and stopped agents. It reports whether the message landed. Exit 3 means delivery isn't confirmed, so check `gofer logs <name>` before resending. Each message to an idle agent resends its full context, so keep messages purposeful.

`gofer run` retries a lost connection by itself and can't start a second agent that way. If it gives up, or `gofer send` loses the connection, the action may still have happened. Check `gofer ls` or `gofer logs <name>` before running it again.

## Blocked agents

A message can't approve a permission prompt or answer a question dialog, so `gofer send` refuses while an agent is blocked. Tell the user and give them `gofer attach <name>`.

An agent that ended its turn with a question in its reply isn't blocked. `gofer wait` exits 0 for it, so answer with `gofer send`.

## Files and code

- Copy files or folders with `gofer cp data.csv <name>:` and `gofer cp <name>:report.pdf .`
- Get code with the `git fetch` command that `gofer run` printed.
- Don't copy secrets. Keep those in the dev box's own config.

## Finishing

`gofer stop <name>` stops an agent and keeps its conversation, and says so if it was already stopped. `gofer rm <name>` removes it and its worktree, keeps the branch, and refuses if there is uncommitted or unfetched work. Run it from the repo you fetched into, so gofer can see the commits. Only pass `--force` when the user asks for it.
