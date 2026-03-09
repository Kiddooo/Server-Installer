// Package repos contains modpack provider implementations.
package repos

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"ftb-server-downloader/structs"
	"ftb-server-downloader/util"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pterm/pterm"
	"gopkg.in/yaml.v3"
)

const (
	cfAPIUrl = "https://api.curseforge.com"
)

// CurseForge implements the ModpackRepo interface for CurseForge modpacks.
// CurseForge modpacks are distributed as zip archives containing a manifest.json
// that lists required mods and an overrides directory with config/resource files.
type CurseForge struct {
	PackId    int
	VersionId int
	apiKey    string

	// archivePath holds the path to the downloaded modpack zip for override extraction
	archivePath string
	// overridesDir is the name of the overrides directory inside the zip (usually "overrides")
	overridesDir string
}

// GetCurseForge creates a new CurseForge provider. The packId and versionId are
// provided as strings and parsed to integers. The apiKey is the CurseForge API key
// (required for all CF API access - obtain from https://console.curseforge.com).
// Returns an error if packId is not a valid integer (versionId may be empty to use latest).
func GetCurseForge(packId, versionId, apiKey string) (*CurseForge, error) {
	pId, err := strconv.Atoi(packId)
	if err != nil {
		return nil, fmt.Errorf("invalid CurseForge pack ID %q: must be a numeric project ID", packId)
	}
	vId := 0
	if versionId != "" {
		vId, err = strconv.Atoi(versionId)
		if err != nil {
			return nil, fmt.Errorf("invalid CurseForge version ID %q: must be a numeric file ID", versionId)
		}
	}
	return &CurseForge{
		PackId:    pId,
		VersionId: vId,
		apiKey:    apiKey,
	}, nil
}

// cfHeaders returns the HTTP headers required for CurseForge API requests.
func (v *CurseForge) cfHeaders() map[string]string {
	return map[string]string{
		"x-api-key":    v.apiKey,
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}
}

// GetModpack fetches modpack metadata from the CurseForge API. It retrieves the
// mod details and the full file listing, converting CurseForge release types
// (1=release, 2=beta, 3=alpha) to the common format.
func (v *CurseForge) GetModpack() (structs.Modpack, error) {
	pterm.Info.Printfln("Getting modpack with id %d from CurseForge", v.PackId)

	// Fetch mod info
	url := fmt.Sprintf("%s/v1/mods/%d", cfAPIUrl, v.PackId)
	resp, err := util.DoGetWithHeaders(url, v.cfHeaders())
	if err != nil {
		return structs.Modpack{}, fmt.Errorf("failed to fetch CurseForge modpack: %w", err)
	}
	defer resp.Body.Close()

	var modResp structs.CFModResponse
	if err := json.NewDecoder(resp.Body).Decode(&modResp); err != nil {
		return structs.Modpack{}, fmt.Errorf("failed to parse CurseForge modpack response: %w", err)
	}

	// Fetch file list for server packs
	versions, err := v.fetchAllFiles()
	if err != nil {
		return structs.Modpack{}, fmt.Errorf("failed to fetch CurseForge file list: %w", err)
	}

	return structs.Modpack{
		Id:       strconv.Itoa(modResp.Data.ID),
		Name:     modResp.Data.Name,
		Versions: versions,
	}, nil
}

// fetchAllFiles retrieves the paginated file list for the modpack project,
// converting each file into a ModpackV entry.
func (v *CurseForge) fetchAllFiles() ([]structs.ModpackV, error) {
	var allVersions []structs.ModpackV
	index := 0
	pageSize := 50

	for {
		url := fmt.Sprintf("%s/v1/mods/%d/files?index=%d&pageSize=%d", cfAPIUrl, v.PackId, index, pageSize)
		resp, err := util.DoGetWithHeaders(url, v.cfHeaders())
		if err != nil {
			return nil, err
		}

		var filesResp structs.CFFilesListResponse
		if err := json.NewDecoder(resp.Body).Decode(&filesResp); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		for _, f := range filesResp.Data {
			releaseType := cfReleaseType(f.ReleaseType)
			allVersions = append(allVersions, structs.ModpackV{
				Id:   strconv.Itoa(f.ID),
				Type: releaseType,
			})
		}

		if index+filesResp.Pagination.ResultCount >= filesResp.Pagination.TotalCount {
			break
		}
		index += pageSize
	}

	// Sort descending by ID (newest first)
	sort.Slice(allVersions, func(i, j int) bool {
		iId, _ := strconv.Atoi(allVersions[i].Id)
		jId, _ := strconv.Atoi(allVersions[j].Id)
		return iId > jId
	})

	return allVersions, nil
}

// GetVersion fetches the file details for the configured version, downloads the
// modpack archive, parses its manifest.json, and resolves all mod download URLs
// via the CurseForge batch files endpoint.
func (v *CurseForge) GetVersion() (structs.ModpackVersion, error) {
	pterm.Info.Printfln("Getting modpack version %d from CurseForge", v.VersionId)

	// First, check if this file has a server pack file ID
	fileUrl := fmt.Sprintf("%s/v1/mods/%d/files/%d", cfAPIUrl, v.PackId, v.VersionId)
	resp, err := util.DoGetWithHeaders(fileUrl, v.cfHeaders())
	if err != nil {
		return structs.ModpackVersion{}, fmt.Errorf("failed to fetch CurseForge file: %w", err)
	}
	defer resp.Body.Close()

	var fileResp structs.CFFileResponse
	if err := json.NewDecoder(resp.Body).Decode(&fileResp); err != nil {
		return structs.ModpackVersion{}, fmt.Errorf("failed to parse CurseForge file response: %w", err)
	}
	cfFile := fileResp.Data
	// Keep the original client file for metadata fallback (GameVersions contains the
	// modloader name on the client file but is often absent on the server pack file).
	clientFile := cfFile

	// If a dedicated server pack exists, use that instead
	actualFileId := v.VersionId
	if cfFile.ServerPackFileId != nil && *cfFile.ServerPackFileId != 0 {
		pterm.Info.Printfln("Server pack found (file ID %d), using server pack", *cfFile.ServerPackFileId)
		actualFileId = *cfFile.ServerPackFileId

		// Re-fetch the server pack file info
		serverUrl := fmt.Sprintf("%s/v1/mods/%d/files/%d", cfAPIUrl, v.PackId, actualFileId)
		sResp, err := util.DoGetWithHeaders(serverUrl, v.cfHeaders())
		if err != nil {
			return structs.ModpackVersion{}, fmt.Errorf("failed to fetch server pack file: %w", err)
		}
		defer sResp.Body.Close()

		var sFileResp structs.CFFileResponse
		if err := json.NewDecoder(sResp.Body).Decode(&sFileResp); err != nil {
			return structs.ModpackVersion{}, fmt.Errorf("failed to parse server pack file: %w", err)
		}
		cfFile = sFileResp.Data
	}

	// Download the modpack zip to a temp file
	archivePath, err := v.downloadModpackZip(cfFile)
	if err != nil {
		return structs.ModpackVersion{}, fmt.Errorf("failed to download modpack zip: %w", err)
	}
	v.archivePath = archivePath

	// Parse the manifest.json from the zip.
	// A CF client pack manifest has ManifestType == "minecraftModpack".
	// A ServerPackCreator manifest uses different fields entirely — JSON unmarshalling
	// silently ignores unknown fields so we must check ManifestType explicitly.
	cfManifest, err := v.parseManifestFromZip(archivePath)
	if err == nil && cfManifest.ManifestType == "minecraftModpack" {
		// Found a genuine CurseForge client pack manifest
		v.overridesDir = cfManifest.Overrides
		if v.overridesDir == "" {
			v.overridesDir = "overrides"
		}

		// Extract modloader + MC version from manifest
		targets := v.parseTargets(cfManifest)

		// Resolve download URLs for all mods listed in the manifest
		files, err := v.resolveModFiles(cfManifest.Files)
		if err != nil {
			return structs.ModpackVersion{}, fmt.Errorf("failed to resolve mod files: %w", err)
		}

		return structs.ModpackVersion{
			Id:         strconv.Itoa(cfFile.ID),
			Name:       cfFile.DisplayName,
			Files:      files,
			Targets:    targets,
			PackFormat: "CurseForge",
		}, nil
	}

	// Try ServerPackCreator manifest (manifest.json with different fields)
	spcManifest, spcErr := v.parseServerPackCreatorManifest(archivePath)
	if spcErr == nil && spcManifest.MinecraftVersion != "" {
		pterm.Info.Printfln("Found ServerPackCreator manifest (MC %s, %s %s)",
			spcManifest.MinecraftVersion, spcManifest.Modloader, spcManifest.ModloaderVersion)

		targets := structs.ModpackTargets{
			McVersion: spcManifest.MinecraftVersion,
		}
		targets.ModLoader.Name = strings.ToLower(spcManifest.Modloader)
		targets.ModLoader.Version = spcManifest.ModloaderVersion
		targets.JavaVersion = detectJavaVersion(targets.McVersion)

		// Mods are already in the zip — extract everything, no downloading needed.
		v.overridesDir = ""

		// Read memory settings from variables.txt if present.
		mem := readSPCVariables(archivePath)

		return structs.ModpackVersion{
			Id:         strconv.Itoa(cfFile.ID),
			Name:       cfFile.DisplayName,
			Files:      []structs.File{},
			Targets:    targets,
			Memory:     mem,
			PackFormat: "ServerPackCreator",
		}, nil
	}

	// Fallback SPC detection: some ServerPackCreator packs omit manifest.json but
	// always include variables.txt with MINECRAFT_VERSION and MODLOADER fields.
	if spcVars := parseSPCVariables(archivePath); spcVars != nil {
		mcVer := spcVars["MINECRAFT_VERSION"]
		modloader := spcVars["MODLOADER"]
		modloaderVer := spcVars["MODLOADER_VERSION"]
		if mcVer != "" && modloader != "" {
			pterm.Info.Printfln("Found ServerPackCreator variables.txt (MC %s, %s %s)",
				mcVer, modloader, modloaderVer)

			targets := structs.ModpackTargets{
				McVersion: mcVer,
			}
			targets.ModLoader.Name = strings.ToLower(modloader)
			targets.ModLoader.Version = modloaderVer
			targets.JavaVersion = detectJavaVersion(targets.McVersion)

			v.overridesDir = ""

			mem := readSPCVariables(archivePath)

			return structs.ModpackVersion{
				Id:         strconv.Itoa(cfFile.ID),
				Name:       cfFile.DisplayName,
				Files:      []structs.File{},
				Targets:    targets,
				Memory:     mem,
				PackFormat: "ServerPackCreator",
			}, nil
		}
	}

	// No manifest found, try ServerStarter config
	ssConfig, ssErr := readServerStarterConfig(archivePath)
	if ssErr == nil && ssConfig != nil {
		pterm.Info.Printfln("Found ServerStarter config in zip")
		targets := structs.ModpackTargets{}

		// Detect modloader type from installerUrl
		loaderName := detectModLoaderFromUrl(ssConfig.Install.InstallerUrl)
		if loaderName == "" {
			return structs.ModpackVersion{}, fmt.Errorf(
				"could not determine modloader from ServerStarter installerUrl %q", ssConfig.Install.InstallerUrl)
		}
		targets.ModLoader.Name = loaderName
		targets.ModLoader.Version = ssConfig.Install.LoaderVersion
		targets.InstallerUrl = ssConfig.Install.InstallerUrl

		// Some ServerStarter configs omit loaderVersion and embed the full version
		// directly in installerUrl as a literal URL (not a template). In that case,
		// attempt to extract the version from the URL as a fallback. The primary
		// resolution path is from the CF manifest's modLoaders (see below).
		if targets.ModLoader.Version == "" && targets.InstallerUrl != "" &&
			!strings.Contains(targets.InstallerUrl, "{{@") {
			targets.ModLoader.Version = extractLoaderVersionFromUrl(targets.InstallerUrl, ssConfig.Install.McVersion)
			pterm.Debug.Printfln("Extracted loader version from literal installerUrl: %s", targets.ModLoader.Version)
		}

		// Extract MC version from NeoForge loader version if not explicitly provided.
		// NeoForge versions after the split follow the scheme <mc_minor>.<mc_patch>.<neoforge_patch>,
		// e.g. "21.1.215" corresponds to Minecraft 1.21.1.
		if ssConfig.Install.McVersion == "" && targets.ModLoader.Version != "" {
			parts := strings.Split(targets.ModLoader.Version, ".")
			if len(parts) >= 2 {
				targets.McVersion = "1." + parts[0] + "." + parts[1]
			}
		} else {
			targets.McVersion = ssConfig.Install.McVersion
		}

		targets.JavaVersion = detectJavaVersion(targets.McVersion)

		v.overridesDir = ""

		// Resolve mods from the modpackUrl zip if present, and extract its overrides.
		var modFiles []structs.File
		if ssConfig.Install.ModpackUrl != "" {
			pterm.Info.Printfln("Downloading modpack zip from %s", ssConfig.Install.ModpackUrl)
			modZipPath, err := v.downloadZipFromUrl(cfDownloadUrl(ssConfig.Install.ModpackUrl))
			if err != nil {
				pterm.Warning.Printfln("Failed to download modpack zip: %s", err)
			} else {
				modManifest, err := v.parseManifestFromZip(modZipPath)
				if err != nil {
					pterm.Warning.Printfln("Failed to parse modpack manifest: %s", err)
					os.Remove(modZipPath)
				} else {
					// Filter out client-only mods listed in ignoreProject
					ignoreSet := make(map[int]bool, len(ssConfig.Install.FormatSpecific.IgnoreProject))
					for _, pid := range ssConfig.Install.FormatSpecific.IgnoreProject {
						ignoreSet[pid] = true
					}
					var filteredFiles []structs.CFManifestFile
					for _, mf := range modManifest.Files {
						if !ignoreSet[mf.ProjectID] {
							filteredFiles = append(filteredFiles, mf)
						}
					}
					// If loaderVersion was null in the ServerStarter config, pull it from
					// the CF client manifest's modLoaders array. The manifest encodes the
					// loader version as "<loaderName>-<version>" (e.g. "forge-40.2.21").
					if targets.ModLoader.Version == "" {
						for _, ml := range modManifest.Minecraft.ModLoaders {
							if !ml.Primary {
								continue
							}
							parts := strings.SplitN(ml.ID, "-", 2)
							if len(parts) == 2 {
								targets.ModLoader.Version = parts[1]
								pterm.Debug.Printfln("Resolved loader version from CF manifest modLoaders: %s", targets.ModLoader.Version)
							}
							break
						}
					}

					pterm.Info.Printfln("Resolving %d mods (%d ignored)", len(filteredFiles), len(modManifest.Files)-len(filteredFiles))
					modFiles, err = v.resolveModFiles(filteredFiles)
					if err != nil {
						pterm.Warning.Printfln("Failed to resolve mod files: %s", err)
					}

					// Store the client pack zip so PrepareFiles extracts its overrides.
					// The ServerStarter zip (archivePath) is no longer needed.
					os.Remove(v.archivePath)
					overridesDir := modManifest.Overrides
					if overridesDir == "" {
						overridesDir = "overrides"
					}
					v.archivePath = modZipPath
					v.overridesDir = overridesDir
				}
			}
		}

		return structs.ModpackVersion{
			Id:      strconv.Itoa(cfFile.ID),
			Name:    cfFile.DisplayName,
			Files:   modFiles,
			Targets: targets,
			Memory: structs.Memory{
				Recommended: parseRamMB(ssConfig.Launch.MaxRam),
				Minimum:     parseRamMB(ssConfig.Launch.MinRam),
			},
			PackFormat: "ServerStarter",
		}, nil
	}

	// No ServerStarter config, try to find installer JAR in zip
	pterm.Debug.Printfln("No CF manifest or ServerStarter config found, treating as direct server pack")
	v.overridesDir = ""

	zipFiles := listZipFiles(archivePath)
	// Try to get modloader info from the server pack file's GameVersions first;
	// fall back to the original client file if the server pack file has no modloader metadata.
	targets := v.detectTargetsFromFile(cfFile)
	if targets.ModLoader.Name == "" {
		targets = v.detectTargetsFromFile(clientFile)
		if targets.ModLoader.Name != "" {
			pterm.Debug.Printfln("Modloader detected from client file metadata: %s", targets.ModLoader.Name)
		}
	}

	for _, name := range zipFiles {
		if strings.HasPrefix(name, "neoforge-") && strings.HasSuffix(name, "-installer.jar") {
			parts := strings.Split(name, "-")
			if len(parts) >= 2 {
				targets.ModLoader.Version = parts[1]
				pterm.Debug.Printfln("Found NeoForge version from zip: %s", targets.ModLoader.Version)
				break
			}
		}
		// e.g. forge-1.20.1-47.4.13-installer.jar
		if strings.HasPrefix(name, "forge-") && strings.HasSuffix(name, "-installer.jar") {
			base := strings.TrimSuffix(strings.TrimPrefix(name, "forge-"), "-installer.jar")
			// base is "<mcVersion>-<forgeVersion>" or just "<forgeVersion>"
			if targets.McVersion != "" && strings.HasPrefix(base, targets.McVersion+"-") {
				targets.ModLoader.Version = strings.TrimPrefix(base, targets.McVersion+"-")
			} else {
				parts := strings.SplitN(base, "-", 2)
				if len(parts) == 2 {
					targets.ModLoader.Version = parts[1]
				}
			}
			if targets.ModLoader.Name == "" {
				targets.ModLoader.Name = "forge"
			}
			pterm.Debug.Printfln("Found Forge version from zip: %s", targets.ModLoader.Version)
			break
		}
	}

	return structs.ModpackVersion{
		Id:         strconv.Itoa(cfFile.ID),
		Name:       cfFile.DisplayName,
		Files:      []structs.File{},
		Targets:    targets,
		PackFormat: "Direct",
	}, nil
}

// downloadModpackZip downloads the modpack archive to a temporary file.
func (v *CurseForge) downloadModpackZip(cfFile structs.CFFile) (string, error) {
	var downloadUrl string
	if cfFile.DownloadUrl != nil {
		downloadUrl = cfDownloadUrl(*cfFile.DownloadUrl)
	} else {
		// Construct the download URL from the file ID (for mods with restricted distribution)
		downloadUrl = fmt.Sprintf("https://mediafilez.forgecdn.net/files/%d/%d/%s",
			cfFile.ID/1000, cfFile.ID%1000, cfFile.FileName)
	}
	return v.downloadZipFromUrl(downloadUrl)
}

// downloadZipFromUrl downloads a zip from an arbitrary URL to a temporary file
// and returns the path. The caller is responsible for removing the file when done.
func (v *CurseForge) downloadZipFromUrl(downloadUrl string) (string, error) {
	tmpFile, err := os.CreateTemp("", "cf-modpack-*.zip")
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	pterm.Debug.Printfln("Downloading CurseForge modpack zip to %s", tmpPath)

	dl, err := util.NewDownload(tmpPath, downloadUrl)
	if err != nil {
		return "", err
	}
	if err := dl.Do(); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	return tmpPath, nil
}

// parseManifestFromZip reads and parses the manifest.json from inside the modpack zip.
// Note: this only unmarshals into CFManifest; callers must check ManifestType to confirm
// the file is actually a CurseForge client pack manifest.
func (v *CurseForge) parseManifestFromZip(zipPath string) (structs.CFManifest, error) {
	data, err := readFileFromZip(zipPath, "manifest.json")
	if err != nil {
		return structs.CFManifest{}, err
	}

	var manifest structs.CFManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return structs.CFManifest{}, fmt.Errorf("failed to parse manifest.json: %w", err)
	}

	return manifest, nil
}

// parseServerPackCreatorManifest reads and parses the manifest.json from inside the zip
// as a ServerPackCreator manifest. Callers should check MinecraftVersion != "" to confirm
// the file is actually a ServerPackCreator manifest.
func (v *CurseForge) parseServerPackCreatorManifest(zipPath string) (structs.ServerPackCreatorManifest, error) {
	data, err := readFileFromZip(zipPath, "manifest.json")
	if err != nil {
		return structs.ServerPackCreatorManifest{}, err
	}

	var manifest structs.ServerPackCreatorManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return structs.ServerPackCreatorManifest{}, fmt.Errorf("failed to parse SPC manifest.json: %w", err)
	}

	return manifest, nil
}

// parseSPCVariables reads and parses variables.txt from a ServerPackCreator zip,
// returning a map of KEY → VALUE pairs. Returns nil if the file is absent.
// Lines beginning with # are treated as comments and ignored.
// Values may optionally be wrapped in double quotes, which are stripped.
func parseSPCVariables(zipPath string) map[string]string {
	data, err := readFileFromZip(zipPath, "variables.txt")
	if err != nil {
		return nil
	}

	vars := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// Strip surrounding quotes
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		vars[key] = val
	}
	return vars
}

// readSPCVariables reads the variables.txt file from a ServerPackCreator zip and
// returns the memory settings encoded in the JAVA_ARGS variable.
// The file uses KEY=VALUE lines (with optional quoted values and # comments).
// JAVA_ARGS typically looks like: JAVA_ARGS="-Xmx4G -Xms4G"
// Returns a zero Memory if variables.txt is absent or JAVA_ARGS is not present.
func readSPCVariables(zipPath string) structs.Memory {
	vars := parseSPCVariables(zipPath)
	if vars == nil {
		return structs.Memory{}
	}

	javaArgs, ok := vars["JAVA_ARGS"]
	if !ok {
		return structs.Memory{}
	}

	var mem structs.Memory
	for _, token := range strings.Fields(javaArgs) {
		switch {
		case strings.HasPrefix(token, "-Xmx"):
			mem.Recommended = parseRamMB(token[4:])
		case strings.HasPrefix(token, "-Xms"):
			mem.Minimum = parseRamMB(token[4:])
		}
	}
	return mem
}

// listZipFiles returns the filenames of all entries in the zip that are not
// in skipped directories (config/, kubejs/, defaultconfigs/, mods/), which
// avoids scanning large mod/config directories when looking for marker files.
func listZipFiles(zipPath string) []string {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		pterm.Warning.Printfln("Failed to open zip for scanning: %v", err)
		return nil
	}
	defer r.Close()

	var names []string
	for _, f := range r.File {
		name := f.Name
		if strings.HasPrefix(name, "config/") || strings.HasPrefix(name, "kubejs/") ||
			strings.HasPrefix(name, "defaultconfigs/") || strings.HasPrefix(name, "mods/") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// ServerStarterConfig represents the structure of a server-setup-config.yaml file
// as used by the ServerStarter tool (https://github.com/BleachDev/ServerStarter).
// It describes how to install the server modloader and configure launch parameters.
type ServerStarterConfig struct {
	SpecVer int           `yaml:"_specver"`
	Install InstallConfig `yaml:"install"`
	Launch  LaunchConfig  `yaml:"launch"`
	Modpack ModpackConfig `yaml:"modpack"`
}

// InstallConfig holds the modloader installation settings from a ServerStarter config.
type InstallConfig struct {
	McVersion          string         `yaml:"mcVersion"`
	LoaderVersion      string         `yaml:"loaderVersion"`
	ModpackUrl         string         `yaml:"modpackUrl"`
	InstallerUrl       string         `yaml:"installerUrl"`
	InstallerArguments []string       `yaml:"installerArguments"`
	FormatSpecific     FormatSpecific `yaml:"formatSpecific"`
}

// FormatSpecific holds pack-format-specific settings from a ServerStarter config.
// For CurseForge packs, IgnoreProject lists project IDs that should not be downloaded
// (typically client-only mods such as shader loaders, performance mods, etc.).
type FormatSpecific struct {
	IgnoreProject []int `yaml:"ignoreProject"`
}

// LaunchConfig holds memory and launch settings from a ServerStarter config.
type LaunchConfig struct {
	MaxRam string `yaml:"maxRam"`
	MinRam string `yaml:"minRam"`
}

// parseRamMB parses a RAM string such as "5G", "512M", "1024K", or "1024"
// (bare number treated as megabytes) and returns the value in megabytes.
// Supports suffixes: G/GB (gigabytes), M/MB (megabytes), K/KB (kilobytes), T/TB (terabytes).
// Returns 0 if the string is empty or cannot be parsed.
func parseRamMB(s string) int {
	if s == "" {
		return 0
	}
	s = strings.TrimSpace(s)
	upper := strings.ToUpper(s)

	// Strip optional trailing "B" (e.g. "GB" → "G", "MB" → "M")
	if strings.HasSuffix(upper, "GB") || strings.HasSuffix(upper, "MB") ||
		strings.HasSuffix(upper, "KB") || strings.HasSuffix(upper, "TB") {
		s = s[:len(s)-2]
		upper = upper[:len(upper)-2]
	}

	var multiplier int
	var numStr string
	switch {
	case strings.HasSuffix(upper, "T"):
		multiplier = 1024 * 1024
		numStr = s[:len(s)-1]
	case strings.HasSuffix(upper, "G"):
		multiplier = 1024
		numStr = s[:len(s)-1]
	case strings.HasSuffix(upper, "M"):
		multiplier = 1
		numStr = s[:len(s)-1]
	case strings.HasSuffix(upper, "K"):
		// Round up: kilobytes to megabytes (1 MB minimum)
		multiplier = 0
		numStr = s[:len(s)-1]
		val, err := strconv.Atoi(strings.TrimSpace(numStr))
		if err != nil {
			return 0
		}
		mb := val / 1024
		if mb < 1 {
			mb = 1
		}
		return mb
	default:
		// bare number — treat as megabytes
		multiplier = 1
		numStr = s
	}

	val, err := strconv.Atoi(strings.TrimSpace(numStr))
	if err != nil {
		return 0
	}
	return val * multiplier
}

// ModpackConfig holds metadata about the modpack from a ServerStarter config.
type ModpackConfig struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// readServerStarterConfig reads and parses a server-setup-config.yaml file from
// inside a zip archive. Returns nil and an error if the file is not present.
func readServerStarterConfig(zipPath string) (*ServerStarterConfig, error) {
	data, err := readFileFromZip(zipPath, "server-setup-config.yaml")
	if err != nil {
		return nil, err
	}

	var config ServerStarterConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse server-setup-config.yaml: %w", err)
	}

	return &config, nil
}

// detectModLoaderFromUrl guesses the modloader type from an installer URL.
// It checks for "neoforge" before "forge" to avoid false positives, since
// NeoForge URLs often also contain the word "forge".
// Returns an empty string if the loader cannot be determined from the URL.
func detectModLoaderFromUrl(url string) string {
	lower := strings.ToLower(url)
	if strings.Contains(lower, "neoforge") {
		return "neoforge"
	}
	if strings.Contains(lower, "fabric") {
		return "fabric"
	}
	if strings.Contains(lower, "forge") {
		return "forge"
	}
	return ""
}

// extractLoaderVersionFromUrl attempts to parse the loader version from a direct
// installer URL when the ServerStarter config does not supply a loaderVersion field.
//
// Forge Maven URLs follow the pattern:
//
//	.../forge/{mcVersion}-{forgeVersion}/forge-{mcVersion}-{forgeVersion}-installer.jar
//
// NeoForge Maven URLs follow:
//
//	.../neoforge/{version}/neoforge-{version}-installer.jar
//
// For both, we look for a path segment that starts with the mcVersion prefix and
// extract the loader version that follows it.
func extractLoaderVersionFromUrl(installerUrl, mcVersion string) string {
	// Split URL into path segments and look for one matching "{mcVersion}-{loaderVersion}"
	// or just "{loaderVersion}" (NeoForge post-split).
	parts := strings.Split(installerUrl, "/")
	for _, seg := range parts {
		if mcVersion != "" && strings.HasPrefix(seg, mcVersion+"-") {
			// Forge: segment is "{mcVersion}-{forgeVersion}"
			return strings.TrimPrefix(seg, mcVersion+"-")
		}
	}
	// NeoForge / Fabric: segment is just the loader version, found after the
	// project-name segment. Look for a segment that looks like a version number.
	for _, seg := range parts {
		if seg == "" || strings.Contains(seg, ".jar") || strings.Contains(seg, "installer") {
			continue
		}
		// A version segment contains at least one dot and starts with a digit
		if len(seg) > 0 && seg[0] >= '0' && seg[0] <= '9' && strings.Contains(seg, ".") {
			return seg
		}
	}
	return ""
}

// readFileFromZip reads a specific file from a zip archive and returns its contents.
// It skips large directories (config/, kubejs/, defaultconfigs/, mods/) for performance.
func readFileFromZip(zipPath, filename string) ([]byte, error) {
	r, err := os.Open(zipPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	stat, err := r.Stat()
	if err != nil {
		return nil, err
	}

	zr, err := zip.NewReader(r, stat.Size())
	if err != nil {
		return nil, err
	}

	for _, f := range zr.File {
		name := f.Name
		if strings.HasPrefix(name, "config/") || strings.HasPrefix(name, "kubejs/") ||
			strings.HasPrefix(name, "defaultconfigs/") || strings.HasPrefix(name, "mods/") {
			continue
		}
		// Match the filename at the root or in any subdirectory, but exclude the
		// overrides directory to avoid returning "overrides/manifest.json" when
		// looking for the root "manifest.json".
		if strings.HasPrefix(name, "overrides/") {
			continue
		}
		if filepath.Base(name) == filename {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}

	return nil, fmt.Errorf("file %s not found in zip", filename)
}

// parseTargets extracts ModpackTargets from a CurseForge manifest. The modloader
// ID format is "forge-47.2.0" or "fabric-0.15.0" or "neoforge-21.0.0".
func (v *CurseForge) parseTargets(manifest structs.CFManifest) structs.ModpackTargets {
	targets := structs.ModpackTargets{
		McVersion: manifest.Minecraft.Version,
	}

	for _, ml := range manifest.Minecraft.ModLoaders {
		if !ml.Primary {
			continue
		}
		parts := strings.SplitN(ml.ID, "-", 2)
		if len(parts) == 2 {
			targets.ModLoader.Name = strings.ToLower(parts[0])
			targets.ModLoader.Version = parts[1]
		}
		break
	}

	// Detect Java version from MC version
	targets.JavaVersion = detectJavaVersion(targets.McVersion)

	return targets
}

// detectTargetsFromFile attempts to detect modloader and MC version from the
// CurseForge file's gameVersions metadata. The gameVersions field typically
// contains entries like "1.21.1" (MC version) and "NeoForge" (loader name).
// The actual loader version is not present here and must be detected later
// by scanning the zip for an installer JAR filename.
func (v *CurseForge) detectTargetsFromFile(cfFile structs.CFFile) structs.ModpackTargets {
	targets := structs.ModpackTargets{}
	for _, gv := range cfFile.GameVersions {
		lower := strings.ToLower(gv)
		if strings.HasPrefix(lower, "1.") {
			targets.McVersion = gv
		} else if strings.Contains(lower, "neoforge") {
			targets.ModLoader.Name = "neoforge"
			targets.ModLoader.Version = extractVersion(gv)
		} else if strings.Contains(lower, "fabric") {
			targets.ModLoader.Name = "fabric"
			targets.ModLoader.Version = extractVersion(gv)
		} else if strings.Contains(lower, "forge") {
			targets.ModLoader.Name = "forge"
			targets.ModLoader.Version = extractVersion(gv)
		}
	}
	targets.JavaVersion = detectJavaVersion(targets.McVersion)
	return targets
}

// extractVersion extracts the version suffix from a loader string like "NeoForge-21.1.0",
// returning "21.1.0", or an empty string if no "-" separator is found.
func extractVersion(s string) string {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

// resolveModFiles takes the manifest file entries and resolves their download URLs
// using the CurseForge batch files endpoint.
func (v *CurseForge) resolveModFiles(manifestFiles []structs.CFManifestFile) ([]structs.File, error) {
	if len(manifestFiles) == 0 {
		return nil, nil
	}

	// Collect all file IDs
	fileIds := make([]int, 0, len(manifestFiles))
	for _, mf := range manifestFiles {
		fileIds = append(fileIds, mf.FileID)
	}

	// Batch request to resolve file download URLs
	reqBody := structs.CFFilesRequest{FileIds: fileIds}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	batchUrl := fmt.Sprintf("%s/v1/mods/files", cfAPIUrl)
	req, err := http.NewRequest("POST", batchUrl, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	for k, hv := range v.cfHeaders() {
		req.Header.Set(k, hv)
	}
	req.Header.Set("User-Agent", util.UserAgent)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to batch-fetch CurseForge files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CurseForge batch files error %d: %s", resp.StatusCode, string(b))
	}

	var filesResp structs.CFFilesResponse
	if err := json.NewDecoder(resp.Body).Decode(&filesResp); err != nil {
		return nil, err
	}

	// CF web API mirror URL template — used as a fallback for all mods.
	// Requires the x-api-key header. Works even when mediafilez returns 403
	// (e.g. for restricted-distribution files or CDN 403 quirks).
	cfApiKey := v.apiKey
	cfWebMirrorHeaders := map[string]string{"x-api-key": cfApiKey}

	var files []structs.File
	for _, cf := range filesResp.Data {
		var dlUrl string
		if cf.DownloadUrl != nil {
			dlUrl = cfDownloadUrl(*cf.DownloadUrl)
		} else {
			// File has restricted distribution — DownloadUrl is null.
			// Construct the mediafilez CDN URL directly from the file ID.
			dlUrl = fmt.Sprintf("https://mediafilez.forgecdn.net/files/%d/%d/%s",
				cf.ID/1000, cf.ID%1000, url.PathEscape(cf.FileName))
		}

		// Always add the CurseForge web API as a mirror. It works for both
		// restricted files and cases where the CDN returns 403.
		cfWebMirror := fmt.Sprintf("https://www.curseforge.com/api/v1/mods/%d/files/%d/download",
			cf.ModId, cf.ID)

		hash, hashType := cfBestHash(cf.Hashes)

		files = append(files, structs.File{
			ID:            strconv.Itoa(cf.ModId),
			Name:          cf.FileName,
			Path:          "mods",
			Url:           dlUrl,
			Mirrors:       []string{cfWebMirror},
			MirrorHeaders: []map[string]string{cfWebMirrorHeaders},
			Hash:          hash,
			HashType:      hashType,
		})
	}

	return files, nil
}

// SetVersionId sets which version (file ID) to fetch.
func (v *CurseForge) SetVersionId(versionId string) {
	v.VersionId, _ = strconv.Atoi(versionId)
}

// PrepareFiles extracts overrides from the downloaded modpack zip into the
// install directory. For server packs without a manifest, it extracts all
// files directly.
func (v *CurseForge) PrepareFiles(installDir string) error {
	if v.archivePath == "" {
		return nil
	}
	defer os.Remove(v.archivePath)

	if v.overridesDir != "" {
		pterm.Info.Printfln("Extracting CurseForge overrides (%s) to %s", v.overridesDir, installDir)
		// Try server overrides first, fall back to regular overrides
		err := extractOverridesFromZip(v.archivePath, installDir, "server-overrides")
		if err != nil {
			err = extractOverridesFromZip(v.archivePath, installDir, v.overridesDir)
			if err != nil {
				pterm.Warning.Printfln("Could not extract overrides: %s", err)
			}
		}
	} else {
		pterm.Info.Printfln("Extracting CurseForge server pack to %s", installDir)
		if err := extractZipToDir(v.archivePath, installDir); err != nil {
			return fmt.Errorf("failed to extract server pack: %w", err)
		}
	}

	return nil
}

// Cleanup removes the temporary modpack zip downloaded during GetVersion, if it
// has not already been removed by PrepareFiles.
func (v *CurseForge) Cleanup() {
	if v.archivePath != "" {
		os.Remove(v.archivePath)
		v.archivePath = ""
	}
}

// SuccessfulInstall is a no-op for CurseForge (no install tracking endpoint).
func (v *CurseForge) SuccessfulInstall() {}

// FailedInstall is a no-op for CurseForge (no install tracking endpoint).
func (v *CurseForge) FailedInstall() {}

// cfDownloadUrl rewrites a CurseForge CDN URL to use mediafilez.forgecdn.net,
// which is publicly accessible without an API key. The older hostnames
// (media.forgecdn.net and edge.forgecdn.net) return 403 for many files.
// It also strips the "?api-key=..." query parameter that is no longer needed,
// and percent-encodes '+' in the path — the CDN returns 403 for a literal '+'
// but succeeds with '%2B'.
func cfDownloadUrl(rawUrl string) string {
	u := rawUrl
	u = strings.ReplaceAll(u, "media.forgecdn.net", "mediafilez.forgecdn.net")
	u = strings.ReplaceAll(u, "edge.forgecdn.net", "mediafilez.forgecdn.net")
	// Strip legacy api-key query parameter
	if idx := strings.Index(u, "?api-key="); idx >= 0 {
		u = u[:idx]
	}
	// The mediafilez CDN rejects '+' in the path with 403 even though '+' is
	// technically valid in URL paths per RFC 3986. Encode it as '%2B'.
	if parsed, err := url.Parse(u); err == nil {
		segments := strings.Split(parsed.Path, "/")
		last := segments[len(segments)-1]
		encoded := strings.ReplaceAll(last, "+", "%2B")
		if encoded != last {
			segments[len(segments)-1] = encoded
			u = parsed.Scheme + "://" + parsed.Host + strings.Join(segments, "/")
		}
	}
	return u
}

// cfReleaseType converts a CurseForge numeric release type to a string.
func cfReleaseType(rt int) string {
	switch rt {
	case 1:
		return "release"
	case 2:
		return "beta"
	case 3:
		return "alpha"
	default:
		return "release"
	}
}

// cfBestHash returns the best available hash from a CurseForge file's hash list,
// preferring SHA1 (algo=1) over MD5 (algo=2). Returns empty strings if no hash is present.
func cfBestHash(hashes []structs.CFHash) (string, string) {
	var md5Val string
	for _, h := range hashes {
		if h.Algo == 1 {
			return h.Value, "sha1"
		}
		if h.Algo == 2 {
			md5Val = h.Value
		}
	}
	if md5Val != "" {
		return md5Val, "md5"
	}
	return "", ""
}

// detectJavaVersion returns a reasonable Java major version string based on
// the Minecraft version. This is a best-effort heuristic for providers that
// don't include explicit Java version metadata.
func detectJavaVersion(mcVersion string) string {
	if mcVersion == "" {
		return "21"
	}
	parts := strings.Split(mcVersion, ".")
	if len(parts) < 2 {
		return "21"
	}
	minor := 0
	fmt.Sscanf(parts[1], "%d", &minor)

	switch {
	case minor >= 21:
		return "21"
	case minor >= 18:
		return "17"
	case minor >= 17:
		return "16"
	default:
		return "8"
	}
}
