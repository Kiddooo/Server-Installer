// Package modloaders provides implementations for downloading and installing
// various Minecraft server mod loaders (Forge, NeoForge, Fabric, and Vanilla).
package modloaders

import "ftb-server-downloader/structs"

// ModLoader defines the interface that all mod loader implementations must satisfy.
// GetDownload returns the files needed to install the mod loader, and Install
// performs the actual installation (running installer JARs, generating start scripts, etc.).
type ModLoader interface {
	// GetDownload returns the list of files that need to be downloaded for this mod loader.
	GetDownload() ([]structs.File, error)

	// Install runs the mod loader installer and generates server start scripts.
	// If useOwnJava is true, the installer uses the bundled JRE instead of the system Java.
	Install(useOwnJava bool) error
}
