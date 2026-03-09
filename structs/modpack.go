// Package structs contains data types for the Server Installer.
package structs

// Modpack represents the metadata for a modpack from any provider.
type Modpack struct {
	Id       string
	Name     string
	Versions []ModpackV
}

// ModpackV represents a version entry in a modpack's version list.
type ModpackV struct {
	Id   string
	Type string // "release", "beta", or "alpha"
}

// ModpackVersion represents the detailed information for a specific modpack version,
// including all files to download and the runtime targets (modloader, MC version, etc.).
type ModpackVersion struct {
	Id         string
	Name       string
	Files      []File
	Targets    ModpackTargets
	Memory     Memory
	PackFormat string // human-readable pack format, e.g. "CurseForge", "ServerStarter", "ServerPackCreator", "Modrinth", "FTB"
}

// File represents a downloadable file with optional integrity verification.
type File struct {
	ID                 string              `json:"id,omitempty"` // stable project/mod ID (e.g. CF project ID, FTB file ID)
	Name               string              `json:"name"`
	Path               string              `json:"path"`
	Url                string              `json:"url"`
	Mirrors            []string            `json:"mirrors"`
	MirrorHeaders      []map[string]string `json:"-"` // per-mirror HTTP headers (parallel to Mirrors); not persisted
	Hash               string              `json:"hash"`
	HashType           string              `json:"hash_type"` // "sha1", "sha256", or "sha512"
	CheckContentLength bool                `json:"check_content_length"`
	// UpdatedName is set transiently during update detection when a file was
	// renamed between versions (e.g. a mod version bump). Not persisted to manifest.
	UpdatedName string `json:"-"`
}

// ModLoaderTarget identifies the modloader and its version.
type ModLoaderTarget struct {
	Name    string `json:"name"` // "forge", "neoforge", or "fabric"
	Version string `json:"version"`
}

// Memory specifies the recommended JVM heap sizes for the modpack.
type Memory struct {
	Minimum     int
	Recommended int
}

// ModpackTargets contains the runtime requirements for a modpack version.
type ModpackTargets struct {
	ModLoader    ModLoaderTarget `json:"modLoader"`
	JavaVersion  string          `json:"javaVersion"`
	McVersion    string          `json:"mcVersion"`
	InstallerUrl string          `json:"installerUrl"` // Custom installer URL (e.g., from ServerStarter config)
}
