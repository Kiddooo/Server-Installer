package structs

// FTBModpack represents the response from the FTB API for a modpack query.
type FTBModpack struct {
	Versions     []FTBPackVersion `json:"versions"`
	Notification string           `json:"notification"`
	Status       string           `json:"status"`
	Message      string           `json:"message,omitempty"`
	ID           int              `json:"id"`
	Name         string           `json:"name"`
	Type         string           `json:"type"`
}

// FTBTargets represents a target entry (modloader, game, runtime) in the FTB API.
type FTBTargets struct {
	Version string `json:"version"`
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"` // "modloader", "game", or "runtime"
	Updated int    `json:"updated"`
}

// FTBPackVersion represents a version entry in the FTB modpack's version list.
type FTBPackVersion struct {
	Targets []FTBTargets `json:"targets"`
	ID      int          `json:"id"`
	Name    string       `json:"name"`
	Type    string       `json:"type"`
	Updated int          `json:"updated"`
	Private bool         `json:"private"`
}

// FTBVersion represents the detailed response for a specific FTB modpack version.
type FTBVersion struct {
	Files        []FTBFiles   `json:"files"`
	Targets      []FTBTargets `json:"targets"`
	Specs        FTBSpecs     `json:"specs"`
	Parent       int          `json:"parent"`
	Notification string       `json:"notification"`
	Status       string       `json:"status"`
	Message      string       `json:"message,omitempty"`
	ID           int          `json:"id"`
	Name         string       `json:"name"`
	Type         string       `json:"type"`
}

// FTBSpecs contains the recommended JVM memory specifications from the FTB API.
type FTBSpecs struct {
	ID          int `json:"id"`
	Minimum     int `json:"minimum"`
	Recommended int `json:"recommended"`
}

// FTBFiles represents a file entry in an FTB modpack version.
type FTBFiles struct {
	Version    string   `json:"version"`
	Path       string   `json:"path"`
	URL        string   `json:"url"`
	Sha1       string   `json:"sha1"`
	Size       int      `json:"size"`
	ClientOnly bool     `json:"clientonly"`
	ServerOnly bool     `json:"serveronly"`
	Optional   bool     `json:"optional"`
	ID         int      `json:"id"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Mirrors    []string `json:"mirrors"`
}
