# Session context for Claude

You are running inside a sandboxed Docker container. This file describes
the environment so you can navigate it efficiently.

## Working directory

`/workspace` is the session's scratch area. Anything you create here lives
with this session and is wiped when the session is removed. Edits to
checked-out repos under `/workspace/<org>/<repo>/...` only affect this
session's copy — push to a remote (or open a PR) to preserve work beyond
the session.

## Persistence

- `/workspace/...` — **session-scoped**. Cleared when this session is removed.
- `$HOME` (`/home/node`) — **workspace-scoped**. Shared across every session
  of this workspace, persisting auth and CLI configs:
  - `$HOME/.claude`, `$HOME/.claude.json` — Claude Code settings/auth
  - `$HOME/.config/gh` — GitHub CLI auth
  - `$HOME/.config/git` — git config
  - `$HOME/.ssh` — SSH keys/known_hosts

Anything outside `/workspace` and `$HOME` (e.g. `/tmp`, system packages
installed at runtime) does **not** persist beyond container restarts.

## Repos in this session
{{REPOS}}

## Tips

- Use `gh` for GitHub operations; it's pre-authenticated via
  `$HOME/.config/gh`.
- Use `git` for local repo work; user/email come from `$HOME/.config/git`.
- The host repo cache is **not** writable from here — your edits stay in
  the session copy until you push.
