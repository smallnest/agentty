# Security Policy

## Supported versions

| Version | Supported |
| --- | --- |
| 0.1.x | Yes |
| Older | No |

## Reporting a vulnerability

Do **not** open a public issue. Report privately through GitHub Security
Advisories:

<https://github.com/smallnest/agentty/security/advisories/new>

Please include:

- the affected version (`agentty version`),
- platform and install method,
- reproduction steps or a proof of concept,
- impact, and any suggested fix.

We will acknowledge as soon as possible and coordinate a fix and disclosure.
Please allow a reasonable window before publishing.

## Threat model

agentty exposes a PTY. A connected client can run commands as the daemon user;
that is the product, not a bug. The security boundary is the pairing credential.

- The daemon binds `0.0.0.0:7070` by default. Set
  `AGENTTY_LISTEN=127.0.0.1:7070` for local-only access.
- Browser access requires a device credential. The recovery token and one-time
  pairing codes are equivalent to passwords.
- TLS is required for any network you do not control. Without it, pairing
  material and the device cookie are visible on the wire.
- `AGENTTY_AUTH_ENABLED=false` disables the boundary. Use it only on loopback.
- The optional relay currently forwards plaintext terminal frames. Use a relay
  you operate; end-to-end encryption is on the roadmap.
- The daemon trusts the local OS user. A same-user process can read
  `$AGENTTY_HOME` and control the daemon.

## In scope

- Authentication or authorization bypass.
- Device credential or session forgery, replay, or revocation bypass.
- Cookie handling flaws (HttpOnly, SameSite, Secure).
- Command injection in the daemon or host, or path traversal in file APIs.
- Denial of service that affects the daemon outside the requesting session, for
  example unbounded memory from a slow client.
- Leaks of pairing material, device secrets, or terminal contents.

## Out of scope

- Running untrusted agents or commands by design.
- Exposing the daemon without TLS or a password on an untrusted network.
- Data available to the same OS user; agentty is local-first.
- Vulnerabilities in agent CLIs (Claude Code, Codex, OpenCode, Pi), xterm.js, or
  other third-party dependencies. Report those upstream.
- The relay deployment itself; see the relay repository.

## Hardening checklist

- [ ] Bind to `127.0.0.1` unless remote access is required.
- [ ] Use TLS (`AGENTTY_TLS_*`) or a tunnel for remote access.
- [ ] Keep the recovery token and pairing QR private.
- [ ] Revoke lost devices from the **devices** panel or with
      `agentty devices revoke <id>`.
- [ ] Rotate the recovery token by deleting `$AGENTTY_HOME/pairing-token` and
      restarting the daemon.
- [ ] Run the daemon as a dedicated, unprivileged user.
