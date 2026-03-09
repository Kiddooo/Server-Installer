// Package structs contains data types for the Server Installer.
package structs

// CFModResponse is the top-level CurseForge API response for a single mod.
type CFModResponse struct {
	Data CFMod `json:"data"`
}

// CFMod represents a CurseForge mod/modpack project.
type CFMod struct {
	ID                 int           `json:"id"`
	Name               string        `json:"name"`
	LatestFiles        []CFFile      `json:"latestFiles"`
	LatestFilesIndexes []CFFileIndex `json:"latestFilesIndexes"`
}

// CFFileIndex is a lightweight reference to a file in a mod's file list.
type CFFileIndex struct {
	GameVersion string `json:"gameVersion"`
	FileId      int    `json:"fileId"`
	Filename    string `json:"filename"`
	ReleaseType int    `json:"releaseType"` // 1=release, 2=beta, 3=alpha
	ModLoader   *int   `json:"modLoader"`
}

// CFFile represents a CurseForge file (version) for a mod/modpack.
type CFFile struct {
	ID               int      `json:"id"`
	ModId            int      `json:"modId"`
	DisplayName      string   `json:"displayName"`
	FileName         string   `json:"fileName"`
	DownloadUrl      *string  `json:"downloadUrl"` // nullable if distribution is restricted
	Hashes           []CFHash `json:"hashes"`
	ServerPackFileId *int     `json:"serverPackFileId"`
	ReleaseType      int      `json:"releaseType"` // 1=release, 2=beta, 3=alpha
	GameVersions     []string `json:"gameVersions"`
}

// CFHash contains a file hash with its algorithm identifier.
type CFHash struct {
	Value string `json:"value"`
	Algo  int    `json:"algo"` // 1=sha1, 2=md5
}

// CFFileResponse is the API response wrapping a single file.
type CFFileResponse struct {
	Data CFFile `json:"data"`
}

// CFFilesResponse is the API response for batch file lookups.
type CFFilesResponse struct {
	Data []CFFile `json:"data"`
}

// CFFilesListResponse is the paginated file list response.
type CFFilesListResponse struct {
	Data       []CFFile     `json:"data"`
	Pagination CFPagination `json:"pagination"`
}

// CFPagination contains pagination metadata for list endpoints.
type CFPagination struct {
	Index       int `json:"index"`
	PageSize    int `json:"pageSize"`
	ResultCount int `json:"resultCount"`
	TotalCount  int `json:"totalCount"`
}

// CFFilesRequest is the request body for the batch file lookup endpoint.
type CFFilesRequest struct {
	FileIds []int `json:"fileIds"`
}

// CFManifest represents the manifest.json found inside a CurseForge modpack zip.
type CFManifest struct {
	Minecraft    CFMinecraft      `json:"minecraft"`
	ManifestType string           `json:"manifestType"`
	Name         string           `json:"name"`
	Version      string           `json:"version"`
	Author       string           `json:"author"`
	Files        []CFManifestFile `json:"files"`
	Overrides    string           `json:"overrides"`
}

// CFMinecraft holds the Minecraft version and modloader info from a CF manifest.
type CFMinecraft struct {
	Version    string        `json:"version"`
	ModLoaders []CFModLoader `json:"modLoaders"`
}

// CFModLoader identifies a modloader in a CF manifest (e.g., "forge-47.2.0").
type CFModLoader struct {
	ID      string `json:"id"`
	Primary bool   `json:"primary"`
}

// CFManifestFile is a mod entry in the CF manifest's file list.
type CFManifestFile struct {
	ProjectID int  `json:"projectID"`
	FileID    int  `json:"fileID"`
	Required  bool `json:"required"`
}

// ServerPackCreatorManifest represents the manifest.json produced by ServerPackCreator
// (https://github.com/Griefed/ServerPackCreator). Unlike a CF client pack manifest,
// this contains the resolved MC/modloader versions directly and requires no mod downloading
// since all mods are already extracted into the zip.
type ServerPackCreatorManifest struct {
	MinecraftVersion         string `json:"minecraftVersion"`
	Modloader                string `json:"modloader"`
	ModloaderVersion         string `json:"modloaderVersion"`
	ServerPackCreatorVersion string `json:"serverPackCreatorVersion"`
}
