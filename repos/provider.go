// Package repos contains modpack provider implementations for different platforms.
package repos

import "ftb-server-downloader/structs"

// ModpackRepo defines the interface that all modpack providers must implement.
// Each provider (FTB, CurseForge, Modrinth) translates its platform-specific
// API data into the common structs used by the installer.
type ModpackRepo interface {
	// GetModpack fetches the modpack metadata including the list of available versions.
	GetModpack() (structs.Modpack, error)

	// GetVersion fetches the detailed information for the configured version,
	// including the list of files to download. For providers that use modpack
	// archives (CurseForge, Modrinth), this may download the archive to parse
	// its internal manifest.
	GetVersion() (structs.ModpackVersion, error)

	// SetVersionId sets which version to fetch in subsequent GetVersion calls.
	SetVersionId(versionId string)

	// PrepareFiles handles provider-specific post-fetch setup such as extracting
	// overrides or server pack archives into the install directory. This is called
	// after file downloads complete but before modloader installation.
	PrepareFiles(installDir string) error

	// Cleanup removes any temporary files (e.g. downloaded zip archives) created
	// during GetVersion. It is safe to call multiple times and is a no-op if
	// PrepareFiles has already cleaned up. Should be deferred immediately after
	// a successful GetVersion call to ensure temp files are removed on all exit paths.
	Cleanup()

	// SuccessfulInstall notifies the provider API of a successful installation.
	SuccessfulInstall()

	// FailedInstall notifies the provider API of a failed installation.
	FailedInstall()
}
