// Package repos contains modpack provider implementations.
package repos

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// safeFileMode ensures a file mode is at minimum 0644 (user read+write).
// If the filename has a shell script extension (.sh, .bat, .ps1, .cmd), execute
// permission is also added (0755), since zip archives do not reliably carry Unix
// execute bits across platforms and tools.
// Some pack tools (e.g. ServerPackCreator) store files as 0444 or 0000, which
// would prevent the Minecraft server from writing config files at runtime.
func safeFileMode(m os.FileMode, name string) os.FileMode {
	m |= 0644
	switch strings.ToLower(filepath.Ext(name)) {
	case ".sh", ".bat", ".ps1", ".cmd":
		m |= 0111 // add execute for user, group, other
	}
	return m
}

// extractZipToDir extracts all files from a zip archive into the target directory,
// preserving the directory structure within the zip.
func extractZipToDir(zipPath, targetDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		destPath := filepath.Join(targetDir, f.Name)

		// Guard against zip slip attacks
		if !strings.HasPrefix(filepath.Clean(destPath), filepath.Clean(targetDir)+string(os.PathSeparator)) {
			continue
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		if err := extractZipFile(f, destPath); err != nil {
			return err
		}
	}

	return nil
}

// extractOverridesFromZip extracts the contents of an overrides directory from
// a zip archive into the target directory, stripping the overrides prefix path.
// For example, files under "overrides/config/foo.json" are extracted to "config/foo.json"
// relative to targetDir.
func extractOverridesFromZip(zipPath, targetDir, overridesPrefix string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	prefix := overridesPrefix + "/"
	found := false

	for _, f := range r.File {
		if !strings.HasPrefix(f.Name, prefix) {
			continue
		}
		found = true

		// Strip the overrides prefix
		relPath := strings.TrimPrefix(f.Name, prefix)
		if relPath == "" {
			continue
		}

		destPath := filepath.Join(targetDir, relPath)

		// Guard against zip slip attacks
		if !strings.HasPrefix(filepath.Clean(destPath), filepath.Clean(targetDir)+string(os.PathSeparator)) {
			continue
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		if err := extractZipFile(f, destPath); err != nil {
			return err
		}
	}

	if !found {
		return fmt.Errorf("no '%s' directory found in zip", overridesPrefix)
	}

	return nil
}

// extractZipFile extracts a single file from a zip archive to the given destination path.
// safeFileMode is applied so that files with restrictive zip permissions (e.g. 0444 or 0000
// from ServerPackCreator packs) are always writable by the owner at runtime.
func extractZipFile(f *zip.File, destPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	outFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, safeFileMode(f.Mode(), f.Name))
	if err != nil {
		return err
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, rc)
	return err
}
