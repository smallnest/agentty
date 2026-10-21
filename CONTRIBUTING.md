# Contributing to agentty

Thanks for helping. agentty is a small, opinionated codebase; the notes below
keep changes easy to review.

## Before you start

- For a bug, open an issue with the bug report form.
- For a feature, open a feature request first so we can agree on scope.
- For a security issue, do **not** open an issue. Follow
  [SECURITY.md](SECURITY.md).

## Development setup

Requirements:

- Go 1.26 or newer
- `make`
- `git`
- Optional: `tmux`, only for the tmux persistence backend

```sh
git clone https://github.com/smallnest/agentty.git
cd agentty
make build
make test
```

The web UI is embedded from `internal/web/static`; there is no separate
frontend build step. Fonts live under `internal/web/static/fonts`.

## Useful commands

| Command | Purpose |
| --- | --- |
| `make check` | gofmt check, `go vet`, installer syntax, tests |
| `make test-race` | Full test suite with the race detector |
| `make cover` | `coverage.out` and `coverage.html` |
| `make lint` | `golangci-lint` when installed, otherwise `go vet` |
| `make snapshot` | Local GoReleaser build into `dist/` |
| `make clean` | Remove `bin/`, `dist/`, and coverage output |

## Project layout

| Path | Responsibility |
| --- | --- |
| `cmd/agentty` | Daemon, CLI, and the internal `host` subcommand |
| `internal/auth` | Recovery credential, one-time codes, devices, rate limiting |
| `internal/config` | `AGENTTY_*` environment parsing |
| `internal/netinfo` | LAN interface ranking |
| `internal/relayclient` | Optional outbound relay connection |
| `internal/server` | HTTP, WebSocket, admin API, transports |
| `internal/sessionhost` | Detached PTY host protocol, server, client, spawn |
| `internal/terminal` | PTY sessions, frame codec, replay, tmux/host backends |
| `internal/web` | Embedded xterm.js UI and bundled font |

## Ground rules

- **Protocol compatibility.** WebSocket frames and JSON messages are a contract
  with existing clients. Add optional fields; never remove or narrow one. Gate a
  new capability only when both sides can negotiate it.
- **Sessions are daemon-owned.** A client detach must not kill an agent. Use
  `Manager.Remove` for explicit closes.
- **No secrets in logs.** Pairing tokens, device secrets, cookies, and terminal
  contents must never be logged.
- **Tests use real dependencies.** Real PTYs, Unix sockets, and WebSockets.
  Avoid mocks for the thing under test. The tmux test skips when tmux is absent.
- **Fail closed.** Auth, checksum, and ownership checks reject on error.
- **Platform gates stay honest.** Linux and macOS are the primary targets.
  Windows compiles, but the PTY host is Unix-first.

## Coding style

- `gofmt` is mandatory; `make check` enforces it.
- Prefer small functions and explicit errors.
- Wrap errors with context: `fmt.Errorf("start host: %w", err)`.
- No `panic` in request paths; return errors.
- Comments explain why, not what.
- Tests are table-driven where it improves failure output.

## Commit and pull requests

1. Fork and branch from `master`.
2. Keep commits focused. Use conventional-ish prefixes (`feat:`, `fix:`,
   `docs:`, `test:`, `chore:`).
3. Run `make check` and `make test-race` before pushing.
4. Open a pull request using the template. Describe the user-visible behavior
   and how you verified it.
5. Link the issue the pull request closes.

Small pull requests are reviewed faster. If a change touches the wire protocol,
authentication, or session lifecycle, include a short design note in the body.

## License

By contributing, you agree that your contribution is licensed under the MIT
License. See [LICENSE](LICENSE).
