# Gofer

A Go CLI that runs Claude Code agents on a dev box from a laptop. Read [ARCHITECTURE.md](ARCHITECTURE.md) before changing how packages fit together.

## Commands

```sh
make test
go vet ./... && GOOS=linux go vet ./...
gofmt -l .
make install
```

`make install` builds the client with embedded Linux binaries and puts it in `~/.local/bin`. The next command updates the dev box copy and the laptop skill.

## Rules

- Use only the Go standard library.
- Don't add code comments of any kind.
- Keep tests to one per distinct behaviour, and prefer table-driven cases and extra assertions in existing tests.
- Keep Claude Code formats, flags and CLI facts in `internal/driver/claude`.
- Keep types that cross SSH in `internal/core`. `internal/store` imports no other internal package.
- Never use `claude -p` or the Agent SDK. Agents are `claude --bg` sessions.
- Never change the user's Claude settings or SSH config. Hooks go in per session through `--settings`.
- Colour and symbols appear only through `internal/cmd/style.go`, and output stays plain when it isn't going to a terminal.
- Fix root causes rather than adding retries or special cases.

## Docs

Update these with any change a user or agent would notice:

- `README.md` for commands, flags and limits
- `skill/SKILL.md` for how agents should use gofer
- `ARCHITECTURE.md` for design and package changes

## Testing

Unit tests use a fake `claude` on `PATH`. They can't prove Claude's real behaviour, so check changes to the driver, send, wait or hooks on a real dev box with `gofer doctor --live` and a short agent using `--model haiku`.

On a real dev box, use throwaway folders, remove test agents afterwards, and leave existing sessions and services alone.
