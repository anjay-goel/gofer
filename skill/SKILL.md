---
name: gofer
description: Delegate work to Claude Code agents on a dev box with the gofer CLI. Use when work should run on the dev box, outlive this session or the laptop being awake, or when the user mentions gofer, handing off, or remote agents.
---

# Gofer

Gofer runs Claude Code agents on a dev box. They keep working while the laptop sleeps, and the user can follow them from the Claude app or claude.ai/code.

## Start here

Run `gofer ls` at the start of any session that touches dev box work, so you see what finished, what is blocked and what changed. It marks those changes as seen, so use `gofer ls --peek` if you only want a look.

Rows lead with what needs attention: blocked, failed or cut short, finished with news, working, then the rest. A `*` marks an agent with news. SUMMARY is the first line of its last reply, and stuck agents show the command to run there. `gofer ls --json` adds `summary`, `commits` and `next`. There is no finished state: an agent that finished its turn shows as `idle`.

Gofer is already set up and this page covers its commands, so go straight to `gofer run`. Don't run `gofer doctor` or `--help`, read gofer's config, or SSH in to check things first. `gofer run` checks the repo itself and refuses within seconds, and it changes nothing when it does. Use `gofer doctor` only after an error you can't act on.

## When to delegate

- Use gofer for long jobs, work that needs the dev box, and anything that should outlive this session.
- Use built-in subagents for quick work on this laptop. They are simpler and return results by themselves.

## Starting agents

Quick agent, in a folder on the dev box. `<dir>` is a folder in the working directory, or an absolute or `~/` path, and it must already exist there:

```sh
gofer run <dir> "<prompt>" --name <short-name>
```

Put `--` before prompt text that starts with a dash. `--model` and `--effort` (`low`, `medium`, `high`, `xhigh` or `max`) pass through to Claude.

Full handoff from the current repo, on a new `agent/<name>` branch at your exact HEAD. Only commits reach the dev box, so gofer refuses uncommitted changes. Commit first, and only pass `--allow-dirty` when the user asks for it:

```sh
git status
gofer run <dir> --doc plan.md --worktree --name <short-name>
```

An agent only knows what you send it. It can't see this conversation, this laptop, or the user's memory and instructions here. Before you start one, think through what the job needs, and make the prompt or handoff document sufficient on its own: the goal, the current state, the relevant paths and branch, decisions already made, the user's rules that apply, how to verify the work, and what to report back. Leave out what the agent can read from the repo.

Agents run in `auto` mode. Only pass `--bypass` when the user asks for it.

`gofer run` trusts folders in the working directory by itself. It refuses an untrusted folder outside it. Don't start the agent in a parent folder instead, because it would lose the folder's CLAUDE.md and project settings. Stop and give the user this command to run once, then retry: `ssh -t <host> 'cd <path> && PATH="$HOME/.local/bin:$PATH" claude'`. It also refuses a `--name` that an agent, an `agent/<name>` branch or a worktree folder already has, so pick another. Names use lowercase letters, digits, dots, dashes or underscores, up to 48 characters.

## Waiting

Run `gofer wait` as a background command and carry on with other work. It uses no tokens, reconnects after a dropped connection, and accepts several names.

```sh
gofer wait <name> <name2>
```

- Exit 0 means every agent finished its turn. It prints the work since your last message, so read all of it, not just the end. `gofer logs <name>` has the rest.
- Exit 2 means an agent is blocked.
- Exit 3 means an agent is idle but its turn can't be confirmed finished, so what it printed may be stale. Check `gofer logs <name>` before relying on it.
- Exit 4 means an agent failed or was cut short, so its work is unfinished. Read what it printed, then use `gofer send` to wake it or tell the user.
- Exit 124 means `--timeout` ran out. An agent with a `/loop` or wakeup pending counts as running, so pass `--timeout` when waiting on one.
- Exit 1 is an error, such as SSH login failing or an unknown name. Read its message.

If several apply, the first of 2, 4 and 3 wins.

With several agents, `gofer wait --any <name> <name2>` returns as soon as one settles and prints only that agent. Its exit code is for that agent. Agents that have already settled count, so drop that name before you wait for the rest.

Add `--notify` when the user wants a desktop alert. It names the agent, its state and the next command.

Never poll with repeated `gofer ls` or `gofer logs` calls, because each one spends tokens. Never use `gofer logs -f`: it's for people watching a terminal, and every entry it prints spends your tokens. Use `gofer wait` instead.

Finished means the agent's turn ended. Read what it printed to see whether it completed the work or stopped to ask a question.

## Messaging

```sh
gofer send <name> "<message>"
```

This works for running and stopped agents. It reports whether the message landed. Exit 3 means delivery isn't confirmed, so check `gofer logs <name>` before resending. Each message to an idle agent resends its full context, so keep messages purposeful.

`gofer run` retries a lost connection by itself and can't start a second agent that way. If it gives up, or `gofer send` loses the connection, the action may still have happened. Check `gofer ls` or `gofer logs <name>` before running it again.

## Blocked agents

A message can't approve a permission prompt, so `gofer send` refuses while an agent is blocked. Tell the user and give them the command `gofer wait` printed, `gofer attach <name>`.

Agents can't open question dialogs or plan mode. They ask in their reply and end their turn, which isn't blocked. `gofer wait` exits 0 for it, so answer with `gofer send`.

## Files and code

- Don't copy anything that's in git. The agent gets it from the repo.
- Copy whatever else the job needs with `gofer cp data.csv <name>:`, and bring results back with `gofer cp <name>:report.pdf .`
- Get code with the `git fetch` command that `gofer run` printed.
- Don't copy secrets. Keep those in the dev box's own config.

## Finishing

`gofer stop <name>` stops an agent and keeps its conversation, and says so if it was already stopped. `gofer rm <name>...` removes agents and their worktrees, keeps the branches, reports each one, and refuses a running agent or uncommitted or unfetched work. Run it from the repo you fetched into, so gofer can see the commits. Only pass `--force` when the user asks for it.

`gofer rm --finished` removes every finished agent and says which it skipped: those with uncommitted or unfetched work, and failed or cut-short agents that may still need waking.
