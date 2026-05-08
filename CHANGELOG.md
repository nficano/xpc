# Changelog

All notable changes to `xpc` will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Phase 0 investigation document (`docs/INVESTIGATION.md`) capturing xpctl's
  architecture, the live target VM environment, and a complete xpctl-to-xpc
  command-surface mapping.
- Phase 1 architecture decisions (`docs/ARCHITECTURE.md`) — twelve locked
  decisions with rationale, rejected alternatives, risks, and a locked
  subcommand surface.
- ARCP RFC 0001 frozen snapshot (`docs/protocol/RFC-0001.md`).
- Master development prompt (`MASTER.md`) committed at repo root.
- Granular task tracker (`TASKS.md`).
- Repository scaffolding: Go module, CI workflows (lint, test, manual real-VM),
  pre-commit hooks, MIT license, branch protection on `main`.
