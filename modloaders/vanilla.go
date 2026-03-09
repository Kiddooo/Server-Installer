package modloaders

import (
	"encoding/json"
	"errors"
	"fmt"
	"ftb-server-downloader/structs"
	"ftb-server-downloader/util"
)

// launcherMeta is the URL for Mojang's version manifest, which lists all
// available Minecraft versions and their metadata URLs.
const launcherMeta = "https://launchermeta.mojang.com/mc/game/version_manifest.json"

// Vanilla implements the ModLoader interface for unmodified (vanilla) Minecraft servers.
// It downloads the official server JAR directly from Mojang.
type Vanilla struct {
	InstallDir string
	Version    string
	Meta       LauncherMeta
}

// GetVanilla creates a new Vanilla instance by fetching Mojang's launcher metadata
// to resolve the server download URL for the target Minecraft version.
func GetVanilla(target structs.ModpackTargets, installDir string) (Vanilla, error) {

	rawMeta, err := util.DoGet(launcherMeta)
	if err != nil {
		return Vanilla{}, err
	}
	defer rawMeta.Body.Close()

	var meta LauncherMeta
	err = json.NewDecoder(rawMeta.Body).Decode(&meta)
	if err != nil {
		return Vanilla{}, err
	}

	return Vanilla{
		InstallDir: installDir,
		Version:    target.McVersion,
		Meta:       meta,
	}, nil
}

// GetDownload resolves the vanilla server JAR download URL from Mojang's version
// manifest and returns it as a single-element file list with SHA-1 checksum verification.
func (v Vanilla) GetDownload() ([]structs.File, error) {
	var mlFiles []structs.File

	var servDlUrl string
	for _, version := range v.Meta.Versions {
		if version.ID == v.Version {
			servDlUrl = version.URL
			break
		}
	}

	if servDlUrl == "" {
		return mlFiles, errors.New("version not found")
	}

	rawVer, err := util.DoGet(servDlUrl)
	if err != nil {
		return []structs.File{}, err
	}
	defer rawVer.Body.Close()

	var version VanillaVersion
	err = json.NewDecoder(rawVer.Body).Decode(&version)
	if err != nil {
		return []structs.File{}, err
	}

	mlFiles = append(mlFiles, structs.File{
		Name:     fmt.Sprintf("minecraft_server.%s.jar", v.Version),
		Url:      version.Downloads.Server.URL,
		Hash:     version.Downloads.Server.Sha1,
		HashType: "sha1",
	})

	return mlFiles, nil
}

// Install is a no-op for vanilla servers since no mod loader installation is needed.
func (v Vanilla) Install(bool) error {
	return nil
}

// LauncherMeta represents Mojang's version manifest containing all available
// Minecraft versions and their metadata URLs.
type LauncherMeta struct {
	Latest   VanillaLatest     `json:"latest"`
	Versions []VanillaVersions `json:"versions"`
}

// VanillaLatest holds the latest release and snapshot version IDs.
type VanillaLatest struct {
	Release  string `json:"release"`
	Snapshot string `json:"snapshot"`
}

// VanillaVersions represents a single entry in the Mojang version manifest,
// containing the version ID and the URL to its detailed metadata.
type VanillaVersions struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// VanillaVersion holds the detailed metadata for a specific Minecraft version,
// including the server JAR download URL and SHA-1 hash.
type VanillaVersion struct {
	Downloads struct {
		Server struct {
			Sha1 string `json:"sha1"`
			Size int    `json:"size"`
			URL  string `json:"url"`
		} `json:"server"`
	} `json:"downloads"`
	ID   string `json:"id"`
	Type string `json:"type"`
}
