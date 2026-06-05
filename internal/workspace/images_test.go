package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStripTag(t *testing.T) {
	assert.Equal(t, "massrepo-claude", stripTag("massrepo-claude:latest"))
	assert.Equal(t, "massrepo-claude", stripTag("massrepo-claude"))
	assert.Equal(t, "ghcr.io/org/img", stripTag("ghcr.io/org/img:v1"))
}

func TestImageDirFor(t *testing.T) {
	m := &Manager{imagesRoot: "/root"}
	assert.Equal(t, filepath.Join("/root", "massrepo-claude"), m.imageDirFor("massrepo-claude:latest"))
}

func TestResolveImageDir_MaterializesEmbedded(t *testing.T) {
	m := &Manager{imagesRoot: t.TempDir()}

	dir, err := m.resolveImageDir("massrepo-claude:latest")
	require.NoError(t, err)
	assert.Equal(t, m.imageDirFor("massrepo-claude"), dir)

	// The embedded Dockerfile is written to disk and is the real one.
	data, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "FROM node:")
}

func TestResolveImageDir_PrefersOnDiskAndDoesNotClobber(t *testing.T) {
	m := &Manager{imagesRoot: t.TempDir()}
	dir := m.imageDirFor("massrepo-claude")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM custom\n"), 0o644))

	got, err := m.resolveImageDir("massrepo-claude:latest")
	require.NoError(t, err)
	assert.Equal(t, dir, got)

	data, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	require.NoError(t, err)
	assert.Equal(t, "FROM custom\n", string(data), "existing on-disk Dockerfile is not overwritten")
}

func TestResolveImageDir_UnknownImage(t *testing.T) {
	m := &Manager{imagesRoot: t.TempDir()}
	dir, err := m.resolveImageDir("ghcr.io/foo/bar:v1")
	require.NoError(t, err)
	assert.Empty(t, dir, "no embedded/on-disk Dockerfile yields an empty dir (build-only would error)")
}

func TestInstallDockerfile(t *testing.T) {
	m := &Manager{imagesRoot: t.TempDir()}
	require.NoError(t, m.installDockerfile("massrepo-custom:latest", "FROM alpine\n"))

	dir := m.imageDirFor("massrepo-custom")
	data, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	require.NoError(t, err)
	assert.Equal(t, "FROM alpine\n", string(data))
}

func TestExportManifest_IncludesDockerfileFromImagesRoot(t *testing.T) {
	m := &Manager{
		imagesRoot:    t.TempDir(),
		workspacesDir: t.TempDir(),
	}
	// A workspace referencing an image whose Dockerfile lives under the images root.
	require.NoError(t, m.installDockerfile("massrepo-claude:latest", "FROM scratch\n"))
	workDir := filepath.Join(m.workspacesDir, "demo")
	require.NoError(t, createWorkspaceDirs(workDir))
	require.NoError(t, writeWorkspaceConfig(WorkspaceConfig{
		Name: "demo", Image: "massrepo-claude:latest", WorkDir: workDir,
	}))

	man, err := m.ExportManifest("demo")
	require.NoError(t, err)
	assert.Equal(t, "FROM scratch\n", man.Dockerfile)
}

func TestHashImageContext(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\n"), 0o644))

	h1, err := hashImageContext(dir)
	require.NoError(t, err)
	h2, err := hashImageContext(dir)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "hash is deterministic")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM debian\n"), 0o644))
	h3, err := hashImageContext(dir)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h3, "hash changes when content changes")
}
