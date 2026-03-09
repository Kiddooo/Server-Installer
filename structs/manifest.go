package structs

import (
	"encoding/json"
	"fmt"
)

// Manifest is written to .manifest.json in the install directory and tracks
// the currently installed modpack state for update detection.
type Manifest struct {
	Id             string         `json:"id"`
	Name           string         `json:"name"`
	VersionName    string         `json:"versionName"`
	VersionId      string         `json:"versionId"`
	Provider       string         `json:"provider,omitempty"`
	ModpackTargets ModpackTargets `json:"modPackTargets"`
	Files          []File         `json:"files,omitempty"`
}

// UnmarshalJSON implements custom unmarshaling to handle both string and
// numeric ID formats for backward compatibility with older manifests that
// stored IDs as integers.
func (m *Manifest) UnmarshalJSON(data []byte) error {
	// Use a type alias to prevent recursive unmarshal
	type Alias Manifest
	raw := &struct {
		Id        json.RawMessage `json:"id"`
		VersionId json.RawMessage `json:"versionId"`
		*Alias
	}{
		Alias: (*Alias)(m),
	}

	if err := json.Unmarshal(data, raw); err != nil {
		return err
	}

	m.Id = rawToString(raw.Id)
	m.VersionId = rawToString(raw.VersionId)
	return nil
}

// rawToString converts a JSON raw message (string or number) to a Go string.
// This handles backward compatibility where older manifests stored IDs as
// JSON numbers while new manifests use JSON strings.
func rawToString(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return fmt.Sprintf("%s", raw)
}
