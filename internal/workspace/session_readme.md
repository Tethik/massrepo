# Session workspace

This directory (`/workspace` inside the container) is the working area for
this session. It is bind-mounted from the host so its contents survive
container restarts, but it is **scoped to this single session** — a fresh
session starts from a clean copy of the requested repos.

## What persists

| Path                       | Scope               | Notes                                              |
| -------------------------- | ------------------- | -------------------------------------------------- |
| `/workspace/<org>/<repo>`  | This session only   | Copied in at session start. Edit freely.           |
| `/workspace/...` (other)   | This session only   | Anything else you create here lives with the session. |
| `$HOME` (`/home/node`)     | Whole workspace     | Shared across all sessions of this workspace.      |
| `$HOME/.claude`, `.claude.json` | Whole workspace | Claude Code auth/settings. Re-used across sessions. |
| `$HOME/.config/gh`, `.config/git`, `.ssh` | Whole workspace | CLI configs, persisted per workspace. |

## What does NOT persist

- Anything outside `/workspace` and `$HOME` (e.g. `/tmp`, `/var`, system
  packages installed at runtime). When the session is removed, only
  `/workspace` and the workspace-level `$HOME` remain on the host.
- Changes inside repos are **not** pushed back to the host's repo cache —
  they only live in this session's `/workspace` copy. Push to a remote
  (or open a PR) to preserve work beyond the session.

## Removing a session

`massrepo rm <workspace>/<session>` deletes this entire directory along with
the container. `$HOME` (auth, configs) is kept because it belongs to the
workspace, not the session.
