# Configuration

Config lives at `~/.config/massrepo/config.yaml` and is created with defaults
on first run.

```yaml
repo_path: ~/repos          # local repo cache directory
data_path: ~/.massrepo      # workspace storage directory

# Optional: Backstage integration for group resolution
backstage_url: https://api.roadie.so
backstage_token: rut_xxxxx

# Optional: static group definitions
groups:
  team:
    my-team: [org/repo1, org/repo2]
  system:
    my-system: [org/service-a, org/service-b]

# Optional: skills seeded into every new workspace's .claude/skills directory.
# An entry is either a local host directory or a git-backed source.
skills:
  - ~/skills/repo-triage                      # local directory
  - git: https://github.com/org/skills-repo   # git source (portable/shareable)
    ref: v1.2.0                               # branch, tag, or commit (required)
    subdir: repo-triage                       # dir within the repo (required; "." for repo root)

# Optional: a library of named MCP server definitions.
mcp_servers:
  sentry:
    type: http
    url: https://mcp.sentry.dev/mcp
  local-tool:
    type: stdio
    command: /usr/local/bin/tool
    args: ["--flag"]

# Optional: which servers from the library to enable in every new workspace.
default_mcp_servers: [sentry]
```

## Concepts

**Workspace** — a named environment that persists authentication state (Claude
config, git credentials, SSH keys) across sessions. Create one per task
context.

**Session** — a short-lived Docker container spun up from a workspace. Each
session gets its own copy of the requested repos so multiple sessions can work
on the same repo independently.

**Repo cache** — repositories are cloned on demand from GitHub into `~/repos`
(configurable) and reused across sessions. Think of it as a local mirror, not
a working copy.

**Groups** — logical collections of repositories referenced by `kind:name`.

## Group references

| Form                | Resolves via       | Notes                                    |
|---------------------|--------------------|------------------------------------------|
| `team:<name>`       | static / Backstage | Configured map or Backstage owner filter |
| `system:<name>`     | static / Backstage | Configured map or Backstage system       |
| `org:<name>`        | `gh` CLI           | All repos owned by a GitHub org          |
| `user:<name>`       | `gh` CLI           | All repos owned by a GitHub user         |

`org:` and `user:` exclude archived repos and forks by default. Append
modifiers (separated by `+`) to relax the filter:

```sh
massrepo shell my-ws org:my-org+archived   # include archived repos
massrepo shell my-ws org:my-org+forks      # include forks
massrepo shell my-ws org:my-org+all        # include both
```

## Skills and MCP servers

Skills and MCP servers are seeded into a workspace's persistent home **once at
creation time**, so every session of that workspace inherits them
(`~/.claude/skills/` and `~/.claude.json` inside the container). The `skills`
and `default_mcp_servers` config keys apply to **every** new workspace; the
`massrepo create` flags add per-workspace overrides on top, and `massrepo
skill` / `massrepo mcp` change an existing workspace. See
[commands.md](commands.md).
