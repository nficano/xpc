# Security policy

## Supported versions

`xpc` is pre-release. There is no production release yet; all security
guarantees apply to the current `main` branch only.

## Threat model (v0)

- The XP VM lives on a private LAN.
- Host ↔ agent traffic is wrapped in TLS 1.2 with a self-signed certificate
  whose fingerprint is pinned in the host's `~/.xpc/config`.
- Every ARCP envelope carries an HMAC-SHA256 signature derived from a
  pre-shared key (PSK). The PSK is generated at `xpc bootstrap` time and
  stored in `~/.xpc/credentials` (host) and `C:\xpc\agent.key` (VM).
- A trusted agent cert + valid HMAC are both required for any tool dispatch.
- Untrusted-LAN scenarios (e.g. shared Wi-Fi) are NOT in scope for v0.

## Reporting a vulnerability

Email Nick Ficano at `nficano@gmail.com` with the subject `xpc security`. Do
not open a public issue for vulnerabilities until a fix is published.

For non-critical issues (e.g. handshake bugs that do not affect
confidentiality or integrity), feel free to open a regular issue.
