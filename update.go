package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"ftb-server-downloader/util"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"

	semver "github.com/hashicorp/go-version"
	"github.com/minio/selfupdate"
	"github.com/pterm/pterm"
)

const (
	// org is the GitHub organization that owns the installer repository.
	org = "Kiddooo"
	// repo is the GitHub repository name for the installer.
	repo = "Server-Installer"
)

// GHRelease represents a GitHub release as returned by the GitHub API.
type GHRelease struct {
	TagName    string `json:"tag_name"`
	Name       string `json:"name"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
}

// VersionInfo holds the result of an update check, including whether an update
// is available and the current/latest version strings.
type VersionInfo struct {
	UpdateAvailable     bool
	CurrentVersion      string
	LatestVersion       string
	Name                string
	isPreReleaseOrDraft bool
}

// checkForUpdate queries the GitHub API for the latest release and compares it
// against the current version. Returns a VersionInfo indicating whether an
// update is available, or an error if the check fails.
func checkForUpdate() (VersionInfo, error) {
	var versionInfo = VersionInfo{
		UpdateAvailable:     false,
		CurrentVersion:      util.ReleaseVersion,
		LatestVersion:       "",
		Name:                "",
		isPreReleaseOrDraft: false,
	}
	releaseApi := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", org, repo)
	resp, err := http.Get(releaseApi)
	if err != nil {
		return versionInfo, fmt.Errorf("error checking for update: %s", err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return versionInfo, fmt.Errorf("bad status: %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return versionInfo, fmt.Errorf("error reading response body: %s", err.Error())
	}

	var release GHRelease
	err = json.Unmarshal(data, &release)
	if err != nil {
		return versionInfo, fmt.Errorf("error unmarshalling response: %s", err.Error())
	}

	if release.Prerelease || release.Draft {
		versionInfo.isPreReleaseOrDraft = true
		return versionInfo, nil
	}

	versionInfo.LatestVersion = release.TagName
	versionInfo.Name = release.Name

	currentVersion, err := semver.NewVersion(strings.ReplaceAll(util.ReleaseVersion, "v", ""))
	if err != nil {
		return versionInfo, fmt.Errorf("error parsing current version: %s", err.Error())
	}
	latestVersion, err := semver.NewVersion(strings.ReplaceAll(versionInfo.LatestVersion, "v", ""))
	if err != nil {
		return versionInfo, fmt.Errorf("error parsing latest version: %s", err.Error())
	}

	if latestVersion.GreaterThan(currentVersion) && util.ReleaseVersion != "v0.0.0-beta.0" {
		versionInfo.UpdateAvailable = true
	}

	return versionInfo, nil
}

// doUpdate downloads and applies the latest release binary, verifying its
// SHA-256 checksum before replacing the current executable. The process exits
// after a successful update so the user can restart with the new version.
func doUpdate(versionInfo VersionInfo) error {
	filename := fmt.Sprintf("server-installer-%s-%s", strings.ToLower(runtime.GOOS), strings.ToLower(runtime.GOARCH))
	if runtime.GOOS == "windows" {
		filename += ".exe"
	}

	downloadUrl := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", org, repo, versionInfo.LatestVersion, filename)
	hashResp, err := http.Get(fmt.Sprintf("%s.sha256", downloadUrl))
	if err != nil {
		return fmt.Errorf("error downloading hash: %s", err.Error())
	}
	defer hashResp.Body.Close()
	if hashResp.StatusCode != http.StatusOK {
		return fmt.Errorf("error downloading hash: %s", hashResp.Status)
	}
	hashBytes, err := io.ReadAll(hashResp.Body)
	if err != nil {
		return fmt.Errorf("error reading hash response: %s", err.Error())
	}
	updateHash := strings.TrimSpace(string(hashBytes))
	pterm.Debug.Println("Update Hash: ", updateHash)

	resp, err := http.Get(downloadUrl)
	if err != nil {
		return fmt.Errorf("error downloading update: %s", err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("error downloading update: %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading update response: %s", err.Error())
	}

	binHashByte := sha256.Sum256(data)
	binHash := fmt.Sprintf("%x", binHashByte)

	if updateHash != binHash {
		return fmt.Errorf("update hash does not match")
	}
	err = selfupdate.Apply(bytes.NewReader(data), selfupdate.Options{})
	if err != nil {
		return fmt.Errorf("error applying update: %s", err.Error())
	}

	pterm.Success.Println("Update successful!\nPlease restart the installer to use the new version.")
	os.Exit(0)
	return nil
}
