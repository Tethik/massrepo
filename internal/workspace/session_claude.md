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
  - `$HOME/.claude`, `$HOME/.claude.json` — Claude Code settings/auth.
    Workspace-level skills (`$HOME/.claude/skills`) and MCP servers
    (`mcpServers` in `$HOME/.claude.json`) are pre-provisioned here at
    workspace creation.
  - `$HOME/.config/gh` — GitHub CLI auth
  - `$HOME/.config/git` — git config
  - `$HOME/.ssh` — SSH keys/known_hosts

Anything outside `/workspace` and `$HOME` (e.g. `/tmp`, system packages
installed at runtime) does **not** persist beyond container restarts.

If you need to install a binary, put it in `$HOME/.bin` (`/home/node/.bin`)
so it survives across sessions, and make sure that directory is on your
`PATH` (`export PATH="$HOME/.bin:$PATH"`). Tools installed to system paths
(e.g. via `apk`) are lost when the container is recreated.

## Repos in this session
{{REPOS}}

## Tips

- Use `gh` for GitHub operations; it's pre-authenticated via
  `$HOME/.config/gh`.
- Use `git` for local repo work; user/email come from `$HOME/.config/git`.
- The host repo cache is **not** writable from here — your edits stay in
  the session copy until you push.
