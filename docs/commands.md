# Commands

## `massrepo create <name>`

Create a new workspace. Sets up persistent directories for Claude config, git credentials, and SSH keys under `~/.massrepo/workspace/<name>/data/`.

```sh
massrepo create my-workspace
massrepo create my-workspace --image massrepo-claude:latest

# Seed extra skills / MCP servers on top of the config defaults:
massrepo create my-workspace --skill ~/skills/extra --mcp sentry

# Opt out of the configured defaults:
massrepo create my-workspace --no-default-skills --no-default-mcp
```

| Flag | Description |
|------|-------------|
| `--skill <dir>` | Extra local skill directory to seed (repeatable, in addition to config defaults) |
| `--mcp <name>` | Enable a named server from `mcp_servers` (repeatable) |
| `--no-default-skills` | Skip the skills configured under `skills` |
| `--no-default-mcp` | Skip the servers listed under `default_mcp_servers` |

## `massrepo shell <workspace> <org/repo|group:name>...`

Create a new session and open an interactive shell inside it. At least one repo or group reference is required.

Repos are cloned from GitHub if not already in the local cache, then copied into the session. The session's container mounts the workspace's shared `data/` directory so authentication state is always available.

```sh
massrepo shell my-workspace org/my-repo
massrepo shell my-workspace org/repo1 org/repo2
massrepo shell my-workspace team:my-team
massrepo shell my-workspace system:booking-api org/extra-repo
massrepo shell my-workspace org/my-repo --shell /bin/sh
```

Repos are mounted inside the container at `/workspace/<org>/<repo>`. Group
reference forms are documented in [configuration.md](configuration.md#group-references).

## `massrepo list [workspace]`

List sessions, optionally filtered to a single workspace.

```sh
massrepo list
massrepo list my-workspace
massrepo list -q    # print only workspace/session references
```

## `massrepo stop <workspace>/<session>`

Stop a running session's container without removing it.

```sh
massrepo stop my-workspace/20260424-143200
```

## `massrepo rm <workspace>[/<session>]`

Remove a workspace and all its sessions, or a single session.

```sh
massrepo rm my-workspace/20260424-143200   # remove one session
massrepo rm my-workspace                   # remove workspace and all sessions
```

## `massrepo duplicate <source> <dest>`

Create a new workspace with the same image as an existing one. No sessions are copied.

```sh
massrepo duplicate my-workspace my-workspace-2
```

## `massrepo set-image <workspace> <image>`

Change the Docker image an existing workspace uses. The new image is built from
its Dockerfile (or fetched with `--pull`) immediately, so problems surface now
rather than at the next shell. Existing sessions keep their current image; new
sessions use the new one.

```sh
massrepo set-image my-workspace einride-sec-base:latest
massrepo set-image my-workspace ghcr.io/org/img:1.2.3 --pull
```

## `massrepo skill` — manage a workspace's skills

Add, list, and remove skills on an existing workspace. Changes are materialized
into the workspace's `~/.claude/skills` and recorded in its config (so they are
carried by `export` and `duplicate`).

```sh
massrepo skill add my-workspace ~/skills/repo-triage         # local directory
massrepo skill add my-workspace https://github.com/org/skills-repo \
  --ref v1.2.0 --subdir repo-triage                          # git source
massrepo skill list my-workspace
massrepo skill rm my-workspace repo-triage
```

## `massrepo mcp` — manage a workspace's MCP servers

Add, list, and remove MCP servers on an existing workspace. Changes are merged
into the workspace's `~/.claude.json` and recorded in its config.

```sh
# HTTP server
massrepo mcp add my-workspace sentry --url https://mcp.sentry.dev/mcp \
  --header "Authorization=Bearer $TOKEN"
# stdio server
massrepo mcp add my-workspace local-tool --command /usr/local/bin/tool \
  --arg --flag --env "API_KEY=$KEY"
# or look the name up in the config 'mcp_servers' library
massrepo mcp add my-workspace sentry

massrepo mcp list my-workspace
massrepo mcp rm my-workspace sentry
```

## `massrepo export` / `massrepo import` — sharing a setup

Share a workspace's skills and MCP servers with a colleague via a portable
`massrepo.yaml` manifest.

```sh
# Export a workspace's setup (skills as git refs, secrets stripped)
massrepo export my-workspace -o massrepo.yaml

# A colleague recreates the workspace from it
massrepo import massrepo.yaml their-workspace

# Later, pull an updated manifest into an existing workspace
massrepo import massrepo.yaml their-workspace --update

# ...and also drop skills/servers no longer in the manifest
massrepo import massrepo.yaml their-workspace --update --prune
```

`export` emits each skill as a **git reference** — local-path skills are skipped
with a warning, since they can't be resolved on another machine. MCP server
**secret values** (`env` / `headers`) are **stripped**, leaving the keys as empty
placeholders. The importer fills those in (in the manifest before importing, or
in the workspace's `home/.claude.json` afterwards) and supplies clone access for
any private skill repositories. Without `-o`, the manifest is written to stdout.

Without `--update`, `import` refuses to touch an existing workspace. With
`--update` the manifest is merged into it: skills and MCP servers are added or
updated, the manifest's image is adopted, and anything else is left in place
(`--prune`, which implies `--update`, additionally removes skills/servers absent
from the manifest — including local-path skills, which manifests never carry).
Re-applying is safe: a blank secret placeholder in the manifest never overwrites
a value you've already filled into `home/.claude.json`.

## `massrepo build-image [image]`

Build (or rebuild) a Docker image. See [images.md](images.md).

```sh
massrepo build-image
massrepo build-image massrepo-claude:latest
```

## `massrepo path <workspace>[/<session>] [<org/repo>]`

Print the host path of a workspace, session, or repo within a session. Useful for piping into editors or shells.

```sh
massrepo path my-workspace                              # workspace root
massrepo path my-workspace/20260424-143200              # session workspace dir
massrepo path my-workspace/20260424-143200 org/repo1    # specific repo

# Open it in your editor
$EDITOR $(massrepo path my-workspace/20260424-143200)

# cd into it
cd $(massrepo path my-workspace/20260424-143200)
```

## Global flags

| Flag | Default | Description |
|------|---------|-------------|
| `--repos-dir` | (from config) | Override the repo cache directory |
| `--images-dir` | `<data_path>/images` | Images root holding `<image>/Dockerfile` |
| `--image` | `massrepo-claude:latest` | Default Docker image for new workspaces |
