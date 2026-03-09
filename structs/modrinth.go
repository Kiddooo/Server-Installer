// Package structs contains data types for the Server Installer.
package structs

// MRProject represents a Modrinth project (modpack).
type MRProject struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	ProjectType string   `json:"project_type"` // "modpack", "mod", etc.
	Versions    []string `json:"versions"`     // list of version IDs
}

// MRVersion represents a specific version of a Modrinth project.
type MRVersion struct {
	ID            string   `json:"id"`
	ProjectID     string   `json:"project_id"`
	Name          string   `json:"name"`
	VersionNumber string   `json:"version_number"`
	Files         []MRFile `json:"files"`
	GameVersions  []string `json:"game_versions"`
	Loaders       []string `json:"loaders"`      // ["fabric", "forge", "neoforge", etc.]
	VersionType   string   `json:"version_type"` // "release", "beta", "alpha"
}

// MRFile represents a downloadable file attached to a Modrinth version.
type MRFile struct {
	Hashes   MRHashes `json:"hashes"`
	URL      string   `json:"url"`
	Filename string   `json:"filename"`
	Primary  bool     `json:"primary"`
	Size     int      `json:"size"`
}

// MRHashes contains the hash values for a Modrinth file.
type MRHashes struct {
	SHA1   string `json:"sha1"`
	SHA512 string `json:"sha512"`
}

// MRIndex is the modrinth.index.json file found inside a .mrpack archive.
type MRIndex struct {
	FormatVersion int               `json:"formatVersion"`
	Game          string            `json:"game"`
	VersionID     string            `json:"versionId"`
	Name          string            `json:"name"`
	Files         []MRIndexFile     `json:"files"`
	Dependencies  map[string]string `json:"dependencies"` // e.g., {"minecraft": "1.20.1", "fabric-loader": "0.15.0"}
}

// MRIndexFile represents a single file entry in the mrpack index.
type MRIndexFile struct {
	Path      string            `json:"path"`
	Hashes    map[string]string `json:"hashes"` // {"sha1": "...", "sha512": "..."}
	Env       *MREnv            `json:"env"`
	Downloads []string          `json:"downloads"`
	FileSize  int               `json:"fileSize"`
}

// MREnv specifies which environments (client/server) a file is needed for.
type MREnv struct {
	Client string `json:"client"` // "required", "optional", or "unsupported"
	Server string `json:"server"` // "required", "optional", or "unsupported"
}
