---
name: gofer-setup
description: Set up a gofer dev box step by step, asking the user what they need. Covers repos, dev tools, CLIs and their logins, MCP servers, Claude plugins and settings. Use after gofer init, or when the user wants to copy their setup to the dev box or says an agent there is missing something.
---

# Gofer setup

Help the user make their dev box ready for real work. This is a guided setup, so keep every step small. Ask one thing at a time, and never show a long list or a plan to read through.

Use the multiple-choice question tool when it's available, with short labels and your recommendation first. Offer at most four options in one question. If there are more, ask in batches, most useful first. Always allow "skip".

## 1. Check quietly

Read the host from `gofer doctor`. If gofer isn't set up, tell the user to run `gofer init` in a terminal, then stop.

Compare the laptop and the box, read-only, without showing the results. Read names, never values.

```sh
jq -r '.mcpServers // {} | to_entries[] | "\(.key) \(.value.type // "stdio") \(.value.url // .value.command)"' ~/.claude.json
ssh <host> 'PATH="$HOME/.local/bin:$PATH" claude mcp list'
```

Use whatever JSON tool the laptop has if `jq` is missing.

## 2. Ask what to set up

Ask one multi-select question with the areas where something is missing:

- Repos
- Dev tools
- CLIs
- MCP servers
- Claude plugins and settings

Leave out areas with nothing to do. If the box is already ready, say so and stop.

## 3. Go through each chosen area

Do one area at a time, and start it with a one-line note.

1. **Find what's missing:** ask which repos matter if you don't know. Their lockfiles, READMEs, `CLAUDE.md` and `AGENTS.md` show the runtimes, CLIs and services they need, and the CLIs on the laptop show the user's habits. Check only those on the box with `command -v`.
2. **Offer the items:** ask a multi-select question, with what you recommend first. Give a short reason only where it isn't obvious.
3. **Do the chosen items:** run them without asking again, one line per result.
4. **Stop only where help is needed:** a login, a `sudo` step, a secret, or a failure. Handle these one at a time, and wait for the user before moving on.

Area notes:

- **Repos:** clone into the root folder that `gofer doctor` shows.
- **Dev tools:** use each tool's official installer, and prefer user-level installs.
- **CLIs:** check each one's login after installing.
- **Claude plugins and settings:** show a setting before changing it.

Repos may also expect `.env` files or keys. Tell the user which ones, and they add them themselves.

## MCP servers

Decide for each server, then recommend:

- A URL on `localhost` or `127.0.0.1`: skip it unless the same service runs on the box.
- Needs a local browser, app or desktop: usually skip it.
- HTTP with no headers: copy it, then the user logs in if it asks.
- Has headers or env values: these may be secrets, so copy them with the command below.
- Local command: check the command exists on the box first.

Copy a server without ever reading its values:

```sh
jq -c '.mcpServers["<name>"]' ~/.claude.json | ssh <host> 'PATH="$HOME/.local/bin:$PATH" sh -c '"'"'claude mcp add-json --scope user <name> "$(cat)"'"'"
```

Then check it with `claude mcp list` on the box. Servers from claude.ai connectors follow the account, so they need no copying.

## Rules

- Never print, read aloud, summarise or write down a secret. That covers MCP headers and env values, tokens, `.env` files and credential files. Don't run commands that show them.
- Never copy credential files, SSH keys, cloud config folders or `.credentials.json` to the box.
- Logins are the user's. Give them the exact command to run in their own terminal, such as `ssh -t <host> 'gh auth login'`, and wait. `!` commands in Claude Code have no real terminal, so don't suggest them for logins.
- The box may be shared. Ask before anything that needs `sudo` or changes the system for other users.
- Don't change existing config without showing the change first.
- Keep track of what you did, and of anything that failed.

## Finish

Run `gofer doctor --live`. Then give a short list of what's ready, what was skipped, and what the user still has to do.
