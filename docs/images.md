# Images

Dockerfiles live one per image folder under the images root (default
`~/.massrepo/images`), so the image name maps to
`<images-root>/<image>/Dockerfile` — e.g. `massrepo-claude:latest` →
`~/.massrepo/images/massrepo-claude/Dockerfile`.

The default image's Dockerfile is **bundled with massrepo** and written into the
images root on first use, so this works with no repo checkout. Edit the
materialized Dockerfile (or add `~/.massrepo/images/<name>/Dockerfile`) to
customize; `--images-dir` points at a different root.

## Building and pulling

massrepo never silently pulls the workspace image from a registry. The image is
**built from a Dockerfile** (the bundled default, or one under the images root)
and **rebuilt automatically when that Dockerfile/context changes** — checked
when a workspace is created and when you open a shell into it (a content hash is
stamped on the image as a label, so an unchanged image is reused as-is). If the
image is absent and has no Dockerfile, `create`/`import` error and suggest
`--pull`, which opts into fetching it from a registry.

So to change an image: edit `~/.massrepo/images/<image>/Dockerfile` and just
`massrepo shell …` (or `create`) — it rebuilds on the next use. `massrepo
build-image` forces a rebuild immediately.

`export` embeds the image's current Dockerfile inline in the manifest, and
`import` writes and builds it locally — so a shared `massrepo.yaml` reproduces
the image without anyone needing access to a registry.
