package workspace

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// embeddedImages holds the Dockerfiles bundled with the binary, laid out as
// images/<image-name>/Dockerfile (+ any build-context files). They are
// materialized into the on-disk images root on demand so massrepo can build its
// default image without a repo checkout.
//
//go:embed images
var embeddedImages embed.FS

// stripTag returns the image name without its ":tag" suffix.
func stripTag(imageName string) string {
	name, _, _ := strings.Cut(imageName, ":")
	return name
}

// dockerfileContent returns the Dockerfile for imageName, preferring an on-disk
// copy under the images root and falling back to the bundled default. Returns
// false when neither exists (e.g. a registry-pulled image with no recipe).
func (m *Manager) dockerfileContent(imageName string) (string, bool) {
	if data, err := os.ReadFile(filepath.Join(m.imageDirFor(imageName), "Dockerfile")); err == nil {
		return string(data), true
	}
	if data, err := embeddedImages.ReadFile(path.Join("images", stripTag(imageName), "Dockerfile")); err == nil {
		return string(data), true
	}
	return "", false
}

// materializeEmbeddedImage writes the embedded build context for imageName into
// destDir when no Dockerfile is already present there (so on-disk edits are not
// clobbered). It reports whether an embedded default exists for the image at all.
func materializeEmbeddedImage(imageName, destDir string) (bool, error) {
	srcRoot := path.Join("images", stripTag(imageName))
	if _, err := fs.Stat(embeddedImages, path.Join(srcRoot, "Dockerfile")); err != nil {
		return false, nil // no embedded default for this image
	}
	if _, err := os.Stat(filepath.Join(destDir, "Dockerfile")); err == nil {
		return true, nil // already on disk; leave user edits alone
	}
	err := fs.WalkDir(embeddedImages, srcRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, srcRoot), "/")
		target := filepath.Join(destDir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := embeddedImages.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return false, fmt.Errorf("materialize embedded image %q: %v", stripTag(imageName), err)
	}
	return true, nil
}
