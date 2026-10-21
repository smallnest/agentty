# agentty

**Run coding-agent CLIs on your machine and reach them from a browser as a terminal.**

[![CI](https://github.com/smallnest/agentty/actions/workflows/ci.yml/badge.svg)](https://github.com/smallnest/agentty/actions/workflows/ci.yml)
[![Release](https://github.com/smallnest/agentty/actions/workflows/release.yml/badge.svg)](https://github.com/smallnest/agentty/actions/workflows/release.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/smallnest/agentty)](go.mod)
[![License](https://img.shields.io/github/license/smallnest/agentty)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/smallnest/agentty)](https://github.com/smallnest/agentty/releases/latest)

agentty is a local-first daemon for Claude Code, Codex, OpenCode, Pi, and any
other terminal program. It spawns each session in a PTY, serves an xterm.js
frontend, and keeps the agent alive across daemon restarts with a detached
per-session host. A pairing flow makes the daemon usable from a phone without
exposing a shell to the open internet.

> agentty is early. The wire protocol and CLI may change between minor releases.

## Features

- **Any terminal agent.** Claude Code, Codex, OpenCode, Pi, or a plain shell.
  The agent list comes from what is installed on `PATH`.
- **Browser terminal.** xterm.js with a bundled JetBrainsMono NL Nerd Font, so
  Nerd Font icons render offline.
- **Session sidebar.** Create, switch, rename, and close sessions. Each session
  keeps its own PTY and scrollback.
- **Survives restarts.** A detached `agentty host` process owns each PTY. The
  daemon can restart without killing the agent, and replays the screen on
  reattach.
- **Pairing, not a shared password.** A 256-bit recovery token, short-lived
  one-time codes, and individually revocable device credentials.
- **LAN-ready.** Binds `0.0.0.0:7070` by default and advertises the best
  physical interface, skipping VPN tunnels, Docker bridges, and link-local
  addresses.
- **TLS built in.** Point it at a certificate and the UI, cookies, and WebSocket
  all use HTTPS/WSS.
- **Optional relay.** Connect outbound to a Paseo-compatible relay when neither
  direct LAN access nor a tunnel fits.
- **One binary.** The web UI, fonts, and host mode are embedded.

## How it works

```
Browser (xterm.js)
        │  WebSocket
        ▼
   agentty daemon ──unix socket──▶ agentty host ──PTY──▶ claude / codex / opencode / pi
        │
        └── optional: relay.paseo.sh ◀── browser outside the LAN
```

The daemon owns the HTTP/WebSocket server, the session registry, pairing, and
the optional relay connection. A session is a detached host process that owns
the PTY; the daemon is a client of that host. The browser speaks a small binary
frame protocol (input, output, resize, snapshot, rename) over the WebSocket.

## Install

### Install script

```sh
curl -fsSL https://raw.githubusercontent.com/smallnest/agentty/master/scripts/install.sh | sh
```

The script detects Linux/macOS and amd64/arm64, downloads the latest release,
verifies it against `checksums.txt`, and installs to `/usr/local/bin` (as root)
or `~/.local/bin`. Pin a version or choose a directory:

```sh
# specific release
curl -fsSL https://raw.githubusercontent.com/smallnest/agentty/master/scripts/install.sh | sh -s -- --version v0.1.0

# custom directory
curl -fsSL https://raw.githubusercontent.com/smallnest/agentty/master/scripts/install.sh | AGENTTY_INSTALL_DIR="$HOME/bin" sh
```

Set `AGENTTY_BASE_URL` to the directory containing `<tag>/<archive>` when using
a mirror. Windows users can run the script from Git Bash/WSL, or download the
`.zip` manually.

### Prebuilt binaries

Download the archive for your platform from
[Releases](https://github.com/smallnest/agentty/releases/latest), extract it,
and run `agentty`. Builds cover Linux, macOS, and Windows on amd64/arm64.

### Go install

```sh
go install github.com/smallnest/agentty/cmd/agentty@latest
```

Requires Go 1.26 or newer. The web assets and fonts are embedded, so the result
is a single self-contained binary.

### Build from source

```sh
git clone https://github.com/smallnest/agentty.git
cd agentty
make build
./bin/agentty
```

## Quick start

```sh
agentty
```

On startup the daemon prints a recovery token, a one-time pairing code, a URL,
and a QR code:

```
agentty 0.1.0 (commit abc1234, built 2026-10-21T09:00:00Z)
agentty auth: recovery token (treat it like a password): UxlioDQo…
agentty auth: one-time pairing code (valid until 2026-10-21T17:05:00+08:00)
  code: 10W5IyqUrHiaOefZUXKIPg
  link: http://192.168.1.23:7070/#pair=10W5IyqUrHiaOefZUXKIPg
agentty listening (default agent=shell):
  http://localhost:7070
  http://192.168.1.23:7070
```

Open the link or scan the QR from a phone on the same network. The daemon binds
`0.0.0.0:7070` by default; set `AGENTTY_LISTEN=127.0.0.1:7070` for local-only
access.

## CLI

| Command | Description |
| --- | --- |
| `agentty` | Run the daemon. |
| `agentty pair` | Mint a one-time pairing code from the running daemon and print its QR. |
| `agentty devices` | List enrolled devices. |
| `agentty devices revoke <id>` | Revoke a device immediately. |
| `agentty sessions` | List sessions with id, name, agent, state, and client count. |
| `agentty sessions rename <id> <name>` | Rename a session. |
| `agentty sessions kill <id>` | Close a session and terminate its agent. |
| `agentty version` | Print version, commit, build date, Go version, and platform. |
| `agentty help` | Show usage. |

`pair`, `devices`, and `sessions` talk to the running daemon over loopback using
`$AGENTTY_HOME/runtime.json` and `$AGENTTY_HOME/local-credential`. Use the same
`AGENTTY_HOME` as the daemon.

## Web UI

The terminal has a collapsible session sidebar:

- `☰` / `«` toggles it; the choice is remembered in `localStorage`.
- The list refreshes every few seconds and shows each session's display name,
  agent, and attached-client count.
- Click a session to attach. Switching reconnects the WebSocket with that
  session id; the host replays the screen, so the terminal comes back.
- `+` creates a new session with the selected agent.
- `×` closes a session and terminates its agent. If it was the current session,
  a fresh one is created.
- Double-click the name in the top bar to rename it (`✓` saves, `✗` cancels).

On narrow screens the sidebar overlays the terminal and closes after a
selection. When a terminal exits, its session disappears from the sidebar
immediately; the final screen and exit code stay in the terminal.

## Configuration

All settings are environment variables.

| Variable | Default | Description |
| --- | --- | --- |
| `AGENTTY_LISTEN` | `0.0.0.0:7070` | HTTP/WebSocket listen address. Use `127.0.0.1:7070` for local-only. |
| `AGENTTY_DEFAULT_AGENT` | `shell` | Agent used when a session does not name one. |
| `AGENTTY_ALLOW_CMD` | `true` | Allow the raw `cmd` create field (`sh -c`). |
| `AGENTTY_WORKING_DIR` | `$HOME` | Default working directory for spawned agents. |
| `AGENTTY_REPLAY_BYTES` | `262144` | Per-session output buffer used to rebuild a screen. |
| `AGENTTY_SEND_QUEUE` | `64` | Outbound frame queue per client before shedding. |
| `AGENTTY_IDLE_TIMEOUT_SECONDS` | `1800` | Close sessions with no attached client. |
| `AGENTTY_PERSIST` | `true` | Run sessions in a detached host. |
| `AGENTTY_BACKEND` | `auto` | `auto`, `host`, `tmux`, or `none`. |
| `AGENTTY_TLS_CERT_FILE` / `AGENTTY_TLS_KEY_FILE` | unset | Serve HTTPS/WSS directly. |
| `AGENTTY_HOME` | `~/.agentty` | State directory: keys, devices, sessions, runtime files. |
| `AGENTTY_SERVER_ID` | generated | Stable identity used on the relay. |
| `AGENTTY_INTERFACE` | auto | Force the advertised LAN interface (`en0`, `eth0`). |
| `AGENTTY_PUBLIC_URL` | derived | Base URL printed in the pairing link and QR. |
| `AGENTTY_AUTH_ENABLED` | `true` | Require pairing auth. |
| `AGENTTY_PASSWORD` | unset | Optional human password, hashed with Argon2id. |
| `AGENTTY_SESSION_TTL_SECONDS` | `2592000` | Device credential lifetime. |
| `AGENTTY_COOKIE_SECURE` | TLS on | Force the `Secure` cookie flag. |
| `AGENTTY_PAIRING_TOKEN` | generated | Override the recovery token (tests/CI). |
| `AGENTTY_RELAY_ENABLED` | true when an endpoint is set | Force the relay on or off. |
| `AGENTTY_RELAY_ENDPOINT` | unset | Relay `host:port`. |
| `AGENTTY_RELAY_USE_TLS` | true for `:443` | Dial the relay over `wss://`. |

## Session persistence

By default (`AGENTTY_PERSIST=true`), each session runs in a detached `agentty
host` child process that owns the PTY. The daemon connects to it over a Unix
socket under `$AGENTTY_HOME/sessions/`. Restarting the daemon does not kill the
agent: on startup the new daemon reads `sessions/host-*.json`, dials each
socket, and receives the host's replay buffer, so the browser reattaches to the
same terminal.

```
daemon ──unix socket──▶ agentty host ──PTY──▶ claude/codex/opencode/pi
```

`AGENTTY_BACKEND` selects the backend:

| Value | Behavior |
| --- | --- |
| `auto` (default) | tmux when installed, otherwise the built-in host |
| `host` | always the built-in detached host |
| `tmux` | always tmux |
| `none` | direct PTY; sessions end with the daemon |

`AGENTTY_PERSIST=false` disables persistence entirely. The startup log reports
which backend is active. Socket paths stay under the platform's Unix-socket
length limit; long homes fall back to a hashed path in the system temp dir.

## Authentication

agentty gates the browser behind a pairing flow. On startup it prints a
**recovery token** and a **one-time pairing code** with a QR:

```
agentty auth: recovery token (treat it like a password): UxlioDQo…
agentty auth: one-time pairing code (valid until 2026-10-21T17:05:00+08:00)
  code: 10W5IyqUrHiaOefZUXKIPg
  link: http://192.168.1.23:7070/#pair=10W5IyqUrHiaOefZUXKIPg
  [QR]
```

Scan the QR (or open the link) to enrol the device. The one-time code expires
after **10 minutes** and can be used once; run `agentty pair` on the daemon host
to mint another.

How it works:

- **Recovery credential.** A 256-bit random token at
  `$AGENTTY_HOME/pairing-token` (mode `0600`), or an optional
  `AGENTTY_PASSWORD`. The token is verified with constant-time `SHA-256`
  (appropriate because the input is already high-entropy). The password is
  hashed with **Argon2id** (64 MiB, 3 passes), requires at least 12 characters,
  and rejects common choices.
- **One-time pairing codes.** 128-bit random, 10-minute TTL, single use. The QR
  encodes `<public-url>/#pair=<code>`; the fragment is never sent to the server.
- **Device credentials.** A successful exchange creates a device with its own
  256-bit secret. Only the SHA-256 digest is stored, in
  `$AGENTTY_HOME/devices.json` (mode `0600`). The browser keeps
  `agentty_device=<id>.<secret>` in an `HttpOnly`, `SameSite=Strict` cookie
  (`Secure` under TLS), with a sliding `AGENTTY_SESSION_TTL_SECONDS` lifetime.
- **Revocation.** `GET /api/devices` lists devices;
  `POST /api/devices/revoke` deletes one. Revocation is immediate because the
  server checks the device store, not just the cookie. `POST /api/auth/logout`
  revokes the calling device.
- **Rate limiting.** Ten failed logins per IP per minute, plus a global cap of
  four concurrent Argon2 verifications.
- **Protected routes.** `/ws` and `/api/agents` require a device.
  `/api/devices`, `/api/devices/revoke`, `/api/sessions`,
  `/api/sessions/rename`, `/api/sessions/close`, and `/api/pairing/code` also
  accept the local admin credential. Static assets and `/health` stay public so
  the sign-in page can load.

Use TLS if the daemon is reachable from another machine. Without it, pairing
material and device cookies cross the network in plaintext. Set
`AGENTTY_TLS_CERT_FILE` / `AGENTTY_TLS_KEY_FILE` directly, or put a TLS tunnel in
front and set `AGENTTY_PUBLIC_URL` to the public https URL.

## Security

`agentty` exposes a PTY, which is shell-equivalent access. The pairing secret is
the only boundary: treat the token or QR like a password, and rotate it by
deleting `$AGENTTY_HOME/pairing-token` and restarting.

- The default bind is `0.0.0.0:7070` so phones on the LAN can pair. Set
  `AGENTTY_LISTEN=127.0.0.1:7070` if you only use the local machine.
- For remote access, use TLS (`AGENTTY_TLS_*`) or a TLS tunnel. Plain `http://`
  on a LAN exposes pairing material and device cookies.
- `AGENTTY_AUTH_ENABLED=false` disables auth entirely; only use it on loopback.
- The optional relay path forwards plaintext terminal frames. If you enable it,
  only point it at a relay you operate.

## Relay (optional)

agentty can register with a Paseo-compatible relay so it is reachable without an
inbound port. Start the relay (the sibling `github.com/smallnest/relay`), then
point agentty at it:

```sh
AGENTTY_RELAY_ENDPOINT=relay.example.com:443 \
AGENTTY_RELAY_USE_TLS=true \
AGENTTY_SERVER_ID=srv_mybox \
agentty
```

agentty dials a v2 **control** socket and opens one **data** socket per browser
connection. The browser connects as `role=client`; the relay assigns the
`connectionId` and bridges bytes. A data socket carries the exact same terminal
frames as a local connection, so session handling, replay, and reattach are
shared code.

Open the UI with relay parameters:

```
http://<ui-host>/?relay=relay.example.com:443&server=srv_mybox&tls=1
```

- `relay` — relay `host:port`
- `server` — daemon `serverId`
- `tls=1` — use `wss://` (omitted: follow the page scheme, or `wss` for `:443`)

For a headless check, use the bundled probe:

```sh
go run ./cmd/relayprobe -relay 127.0.0.1:4000 -server srv_mybox -cmd "printf hello"
```

The relay path is plaintext today; end-to-end encryption is on the roadmap.

## Architecture

| Path | Responsibility |
| --- | --- |
| `cmd/agentty` | Daemon, CLI, and the internal `host` subcommand. |
| `internal/auth` | Recovery credential, one-time codes, devices, rate limiting. |
| `internal/config` | `AGENTTY_*` environment parsing. |
| `internal/netinfo` | Ranks LAN interfaces and picks the advertised IPv4. |
| `internal/relayclient` | Optional outbound relay control/data sockets. |
| `internal/server` | HTTP, WebSocket, admin API, transports, sessions API. |
| `internal/sessionhost` | Detached PTY host protocol, server, client, and spawn. |
| `internal/terminal` | PTY sessions, frame codec, replay, tmux/host backends. |
| `internal/web` | Embedded xterm.js UI and the bundled font. |

## Wire protocol

Terminal frames are binary and multiplexed by slot:

```
[ opcode:1 ][ slot:2 ][ payload... ]
```

| Opcode | Direction | Payload |
| --- | --- | --- |
| `0x01` output | server → client | raw PTY bytes |
| `0x02` input | client → server | keyboard bytes |
| `0x03` resize | client → server | rows, cols (uint16 big-endian) |
| `0x04` snapshot | server → client | replay buffer |
| `0x10` create | client → server | JSON `CreateRequest` |
| `0x11` close | client → server | terminate session |
| `0x12` attached | server → client | session id |
| `0x13` exit | server → client | exit code (int32 big-endian) |
| `0x14` error | server → client | error text |
| `0x15` rename | client → server | new display name |
| `0x16` name | server → client | current display name |

`CreateRequest`:

```json
{ "slot": 0, "session": "sess_…", "name": "api refactor", "agent": "claude", "cmd": "", "cwd": "", "cols": 80, "rows": 24 }
```

An empty `session` gets a generated id. Reusing an id reattaches to the running
session instead of spawning a new process.

## Development

Requires Go 1.26+ and `make`.

```sh
make help        # list targets
make build       # build ./bin/agentty
make run         # run from source
make test        # go test ./...
make test-race   # go test -race -count=1 ./...
make cover       # coverage.out + coverage.html
make check       # formatting, vet, tests
make lint        # golangci-lint when installed, otherwise go vet
make snapshot    # local GoReleaser build into dist/
```

The test suite uses real PTYs, Unix sockets, and WebSockets. The tmux
persistence test skips when `tmux` is not installed; the built-in host tests do
not need it.

## Release

Releases are cut from annotated tags. CI runs GoReleaser, which builds Linux,
macOS, and Windows archives, writes `checksums.txt`, and publishes a GitHub
release:

```sh
git tag v0.1.0
git push origin v0.1.0
```

Use `make snapshot` to produce the same artifacts locally without publishing.
The workflow lives in `.github/workflows/release.yml`; the configuration lives
in `.goreleaser.yaml`.

## Contributing

1. Fork and create a topic branch.
2. Keep the wire protocol backward-compatible; add optional fields, never
   narrow or remove them.
3. Run `make check` and `make test-race` before opening a pull request.
4. Describe the user-visible behavior and how you verified it.

Bug reports and feature requests are welcome at
[github.com/smallnest/agentty/issues](https://github.com/smallnest/agentty/issues).

## License

MIT. See [LICENSE](LICENSE).

## Acknowledgements

- [xterm.js](https://xtermjs.org/) for the browser terminal.
- [JetBrains Mono Nerd Font](https://github.com/ryanoasis/nerd-fonts) for the
  bundled font (SIL OFL 1.1).
- [creack/pty](https://github.com/creack/pty) for PTY handling.
- [Gorilla WebSocket](https://github.com/gorilla/websocket) for the transport.
- [qrterminal](https://github.com/mdp/qrterminal) for terminal QR codes.
- [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto) for Argon2id.
