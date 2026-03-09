package structs

import "time"

// Adoptium represents the response from the Adoptium API for JRE version queries.
type Adoptium []struct {
	Binaries    []AdoptiumBinaries `json:"binaries"`
	ReleaseName string             `json:"release_name"`
}

// AdoptiumPackage contains download information for an Adoptium binary package.
type AdoptiumPackage struct {
	Checksum      string `json:"checksum"`
	ChecksumLink  string `json:"checksum_link"`
	DownloadCount int    `json:"download_count"`
	Link          string `json:"link"`
	MetadataLink  string `json:"metadata_link"`
	Name          string `json:"name"`
	SignatureLink string `json:"signature_link"`
	Size          int    `json:"size"`
}

// AdoptiumBinaries describes a single binary distribution from the Adoptium API.
type AdoptiumBinaries struct {
	Architecture  string           `json:"architecture"`
	DownloadCount int              `json:"download_count"`
	HeapSize      string           `json:"heap_size"`
	ImageType     string           `json:"image_type"`
	JvmImpl       string           `json:"jvm_impl"`
	Os            string           `json:"os"`
	Package       AdoptiumPackage  `json:"package"`
	Project       string           `json:"project"`
	ScmRef        string           `json:"scm_ref"`
	UpdatedAt     time.Time        `json:"updated_at"`
	Installer     *AdoptiumPackage `json:"installer,omitempty"`
}
