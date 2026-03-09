// Package repos contains modpack provider implementations.
package repos

import (
	"encoding/json"
	"fmt"
	"ftb-server-downloader/structs"
	"ftb-server-downloader/util"
	"os"
	"path/filepath"
	"strings"

	"github.com/pterm/pterm"
)

const (
	mrAPIUrl = "https://api.modrinth.com/v2"
)

// Modrinth implements the ModpackRepo interface for Modrinth modpacks.
// Modrinth modpacks are distributed as .mrpack files (zip archives) containing
// a modrinth.index.json with mod download URLs and an overrides directory.
type Modrinth struct {
	PackId    string // Modrinth project ID or slug
	VersionId string // Modrinth version ID

	// archivePath holds the path to the downloaded .mrpack for override extraction
	archivePath string
}

// GetModrinth creates a new Modrinth provider with the given pack and version IDs.
// Modrinth supports both string slugs and alphanumeric IDs for project lookup.
func GetModrinth(packId, versionId string) *Modrinth {
	return &Modrinth{
		PackId:    packId,
		VersionId: versionId,
	}
}

// GetModpack fetches modpack metadata from the Modrinth API. It retrieves the
// project details and all version entries, converting Modrinth version types
// to the common format.
func (m *Modrinth) GetModpack() (structs.Modpack, error) {
	pterm.Info.Printfln("Getting modpack '%s' from Modrinth", m.PackId)

	// Fetch project info
	projectUrl := fmt.Sprintf("%s/project/%s", mrAPIUrl, m.PackId)
	pterm.Debug.Printfln("Fetching Modrinth project from %s", projectUrl)
	resp, err := util.DoGetWithHeaders(projectUrl, mrHeaders())
	if err != nil {
		return structs.Modpack{}, fmt.Errorf("failed to fetch Modrinth project: %w", err)
	}
	defer resp.Body.Close()

	var project structs.MRProject
	if err := json.NewDecoder(resp.Body).Decode(&project); err != nil {
		return structs.Modpack{}, fmt.Errorf("failed to parse Modrinth project: %w", err)
	}

	if project.ProjectType != "modpack" {
		return structs.Modpack{}, fmt.Errorf("'%s' is a Modrinth %s, not a modpack — only modpack projects are supported", m.PackId, project.ProjectType)
	}

	// Fetch all versions for this project
	versionsUrl := fmt.Sprintf("%s/project/%s/version", mrAPIUrl, m.PackId)
	pterm.Debug.Printfln("Fetching Modrinth versions from %s", versionsUrl)
	vResp, err := util.DoGetWithHeaders(versionsUrl, mrHeaders())
	if err != nil {
		return structs.Modpack{}, fmt.Errorf("failed to fetch Modrinth versions: %w", err)
	}
	defer vResp.Body.Close()

	var versions []structs.MRVersion
	if err := json.NewDecoder(vResp.Body).Decode(&versions); err != nil {
		return structs.Modpack{}, fmt.Errorf("failed to parse Modrinth versions: %w", err)
	}

	// Convert to common format - Modrinth returns versions in newest-first order
	var versionList []structs.ModpackV
	for _, v := range versions {
		versionList = append(versionList, structs.ModpackV{
			Id:   v.ID,
			Type: v.VersionType, // already "release", "beta", or "alpha"
		})
	}

	return structs.Modpack{
		Id:       project.ID,
		Name:     project.Title,
		Versions: versionList,
	}, nil
}

// GetVersion fetches the version details from the Modrinth API, downloads the
// .mrpack archive, parses the modrinth.index.json, and builds the file list
// from the index entries (filtering out client-only files).
func (m *Modrinth) GetVersion() (structs.ModpackVersion, error) {
	pterm.Info.Printfln("Getting version '%s' from Modrinth", m.VersionId)

	// Fetch version info
	versionUrl := fmt.Sprintf("%s/version/%s", mrAPIUrl, m.VersionId)
	resp, err := util.DoGetWithHeaders(versionUrl, mrHeaders())
	if err != nil {
		return structs.ModpackVersion{}, fmt.Errorf("failed to fetch Modrinth version: %w", err)
	}
	defer resp.Body.Close()

	var version structs.MRVersion
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
		return structs.ModpackVersion{}, fmt.Errorf("failed to parse Modrinth version: %w", err)
	}

	// Find the primary .mrpack file
	mrpackFile, err := findMrpackFile(version.Files)
	if err != nil {
		return structs.ModpackVersion{}, err
	}

	// Download the .mrpack to a temp file
	archivePath, err := m.downloadMrpack(mrpackFile)
	if err != nil {
		return structs.ModpackVersion{}, fmt.Errorf("failed to download .mrpack: %w", err)
	}
	m.archivePath = archivePath

	// Parse modrinth.index.json from the mrpack
	index, err := m.parseIndexFromMrpack(archivePath)
	if err != nil {
		return structs.ModpackVersion{}, fmt.Errorf("failed to parse mrpack index: %w", err)
	}

	// Extract targets from the index dependencies
	targets := m.parseTargets(index, version)

	// Convert index files to common File format
	files := m.parseFiles(index.Files)

	return structs.ModpackVersion{
		Id:         version.ID,
		Name:       version.Name,
		Files:      files,
		Targets:    targets,
		PackFormat: "Modrinth",
	}, nil
}

// findMrpackFile finds the primary .mrpack file from a version's file list.
// It prefers the file marked as primary, falling back to the first .mrpack file found.
func findMrpackFile(files []structs.MRFile) (structs.MRFile, error) {
	for _, f := range files {
		if f.Primary && strings.HasSuffix(f.Filename, ".mrpack") {
			return f, nil
		}
	}
	// Fallback: find any .mrpack file
	for _, f := range files {
		if strings.HasSuffix(f.Filename, ".mrpack") {
			return f, nil
		}
	}
	return structs.MRFile{}, fmt.Errorf("no .mrpack file found in Modrinth version")
}

// downloadMrpack downloads the .mrpack archive to a temporary file.
func (m *Modrinth) downloadMrpack(file structs.MRFile) (string, error) {
	tmpFile, err := os.CreateTemp("", "mr-modpack-*.mrpack")
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	pterm.Debug.Printfln("Downloading Modrinth mrpack to %s", tmpPath)

	dl, err := util.NewDownload(tmpPath, file.URL)
	if err != nil {
		return "", err
	}
	if err := dl.Do(); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	return tmpPath, nil
}

// parseIndexFromMrpack reads and parses the modrinth.index.json from inside the .mrpack.
func (m *Modrinth) parseIndexFromMrpack(mrpackPath string) (structs.MRIndex, error) {
	data, err := readFileFromZip(mrpackPath, "modrinth.index.json")
	if err != nil {
		return structs.MRIndex{}, err
	}

	var index structs.MRIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return structs.MRIndex{}, fmt.Errorf("failed to parse modrinth.index.json: %w", err)
	}

	return index, nil
}

// parseTargets extracts ModpackTargets from the mrpack index dependencies and
// version metadata. Modrinth dependencies use keys like "minecraft", "forge",
// "fabric-loader", "neoforge", and "quilt-loader".
func (m *Modrinth) parseTargets(index structs.MRIndex, version structs.MRVersion) structs.ModpackTargets {
	targets := structs.ModpackTargets{}

	// Extract from dependencies map
	if mc, ok := index.Dependencies["minecraft"]; ok {
		targets.McVersion = mc
	}
	if v, ok := index.Dependencies["forge"]; ok {
		targets.ModLoader.Name = "forge"
		targets.ModLoader.Version = v
	}
	if v, ok := index.Dependencies["neoforge"]; ok {
		targets.ModLoader.Name = "neoforge"
		targets.ModLoader.Version = v
	}
	if v, ok := index.Dependencies["fabric-loader"]; ok {
		targets.ModLoader.Name = "fabric"
		targets.ModLoader.Version = v
	}
	if v, ok := index.Dependencies["quilt-loader"]; ok {
		targets.ModLoader.Name = "quilt"
		targets.ModLoader.Version = v
	}

	// Fallback: detect from version loaders if dependencies are sparse
	if targets.ModLoader.Name == "" && len(version.Loaders) > 0 {
		targets.ModLoader.Name = strings.ToLower(version.Loaders[0])
	}
	if targets.McVersion == "" && len(version.GameVersions) > 0 {
		targets.McVersion = version.GameVersions[0]
	}

	// Detect Java version from MC version
	targets.JavaVersion = detectJavaVersion(targets.McVersion)

	return targets
}

// parseFiles converts mrpack index file entries to the common File format.
// Client-only files (env.server == "unsupported") are excluded since this
// is a server installer. A warning is printed listing all skipped files so
// the user is aware in case a pack incorrectly marks a server-required mod's
// dependency as client-only (which would cause a runtime crash).
func (m *Modrinth) parseFiles(indexFiles []structs.MRIndexFile) []structs.File {
	var files []structs.File
	var skipped []string

	for _, f := range indexFiles {
		// Skip client-only files
		if f.Env != nil && f.Env.Server == "unsupported" {
			pterm.Debug.Printfln("Skipping client-only file: %s", f.Path)
			skipped = append(skipped, filepath.Base(f.Path))
			continue
		}

		if len(f.Downloads) == 0 {
			pterm.Warning.Printfln("Skipping file with no downloads: %s", f.Path)
			continue
		}

		// Parse path into directory + filename
		dir := filepath.Dir(f.Path)
		name := filepath.Base(f.Path)

		// Clean up directory path
		if dir == "." {
			dir = ""
		}

		// Get best hash
		hash, hashType := mrBestHash(f.Hashes)

		// Extract project ID from Modrinth CDN URL:
		// https://cdn.modrinth.com/data/{project_id}/versions/{version_id}/{filename}
		projectID := mrProjectIDFromURL(f.Downloads[0])

		file := structs.File{
			ID:       projectID,
			Name:     name,
			Path:     dir,
			Url:      f.Downloads[0],
			Hash:     hash,
			HashType: hashType,
		}

		// Add extra downloads as mirrors
		if len(f.Downloads) > 1 {
			file.Mirrors = f.Downloads[1:]
		}

		files = append(files, file)
	}

	if len(skipped) > 0 {
		pterm.Warning.Printfln(
			"Skipped %d client-only file(s) not needed on the server: %s\n"+
				"  If the server crashes on startup, one of these may be required by a server-side mod (pack author bug).",
			len(skipped),
			strings.Join(skipped, ", "),
		)
	}

	return files
}

// SetVersionId sets which version ID to fetch.
func (m *Modrinth) SetVersionId(versionId string) {
	m.VersionId = versionId
}

// PrepareFiles extracts the overrides directories from the downloaded .mrpack
// into the install directory. Modrinth packs can contain both "overrides" (for
// all environments) and "server-overrides" (server-specific files).
func (m *Modrinth) PrepareFiles(installDir string) error {
	if m.archivePath == "" {
		return nil
	}
	defer os.Remove(m.archivePath)

	// Extract server-overrides first (takes precedence)
	err := extractOverridesFromZip(m.archivePath, installDir, "server-overrides")
	if err != nil {
		pterm.Debug.Printfln("No server-overrides found: %s", err)
	} else {
		pterm.Info.Println("Extracted Modrinth server-overrides")
	}

	// Then extract regular overrides
	err = extractOverridesFromZip(m.archivePath, installDir, "overrides")
	if err != nil {
		pterm.Debug.Printfln("No overrides found: %s", err)
	} else {
		pterm.Info.Println("Extracted Modrinth overrides")
	}

	return nil
}

// Cleanup removes the temporary .mrpack file downloaded during GetVersion, if it
// has not already been removed by PrepareFiles.
func (m *Modrinth) Cleanup() {
	if m.archivePath != "" {
		os.Remove(m.archivePath)
		m.archivePath = ""
	}
}

// SuccessfulInstall is a no-op for Modrinth (no install tracking endpoint).
func (m *Modrinth) SuccessfulInstall() {}

// FailedInstall is a no-op for Modrinth (no install tracking endpoint).
func (m *Modrinth) FailedInstall() {}

// mrHeaders returns the default HTTP headers for Modrinth API requests.
func mrHeaders() map[string]string {
	return map[string]string{
		"Accept": "application/json",
	}
}

// mrProjectIDFromURL extracts the Modrinth project ID from a CDN download URL.
// Modrinth CDN URLs follow the pattern:
//
//	https://cdn.modrinth.com/data/{project_id}/versions/{version_id}/{filename}
//
// Returns an empty string if the URL does not match the expected pattern.
func mrProjectIDFromURL(dlURL string) string {
	// Split on "/data/" and take the segment before the next "/"
	const marker = "/data/"
	idx := strings.Index(dlURL, marker)
	if idx < 0 {
		return ""
	}
	rest := dlURL[idx+len(marker):]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return rest
	}
	return rest[:slash]
}

// mrBestHash returns the best available hash from a Modrinth file's hash map,
// preferring SHA1 over SHA512 for compatibility with the common File format.
func mrBestHash(hashes map[string]string) (string, string) {
	if h, ok := hashes["sha1"]; ok {
		return h, "sha1"
	}
	if h, ok := hashes["sha512"]; ok {
		return h, "sha512"
	}
	return "", ""
}
