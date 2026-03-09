// Package repos contains modpack provider implementations.
package repos

import (
	"encoding/json"
	"fmt"
	"ftb-server-downloader/structs"
	"ftb-server-downloader/util"
	"sort"
	"strconv"
	"strings"

	"github.com/pterm/pterm"
)

const (
	ftbApiUrl = "https://api.feed-the-beast.com/v1/modpacks"
)

// FTB implements the ModpackRepo interface for Feed The Beast modpacks.
type FTB struct {
	PackId    int
	VersionId int
}

// GetFTB creates a new FTB provider with the given pack and version IDs.
// The IDs are provided as strings and parsed to integers since the FTB API
// uses numeric identifiers.
func GetFTB(packId, versionId string) *FTB {
	pId, _ := strconv.Atoi(packId)
	vId, _ := strconv.Atoi(versionId)
	return &FTB{
		PackId:    pId,
		VersionId: vId,
	}
}

// GetModpack fetches modpack metadata from the FTB API and returns it in
// the common Modpack format. The version list is sorted in descending order
// by ID so that the latest version appears first.
func (m *FTB) GetModpack() (structs.Modpack, error) {
	url := fmt.Sprintf("%s/modpack/%d", ftbApiUrl, m.PackId)
	pterm.Debug.Printfln("Getting modpack from ftb using %s", url)
	resp, err := util.DoGet(url)
	if err != nil {
		return structs.Modpack{}, err
	}
	defer resp.Body.Close()

	var ftbModpack structs.FTBModpack

	err = json.NewDecoder(resp.Body).Decode(&ftbModpack)
	if err != nil {
		return structs.Modpack{}, err
	}

	if ftbModpack.Status != "success" {
		return structs.Modpack{}, fmt.Errorf("unsuccessful response: %s, %s", ftbModpack.Status, ftbModpack.Message)
	}

	var versionList []structs.ModpackV
	for _, v := range ftbModpack.Versions {
		ver := structs.ModpackV{
			Id:   strconv.Itoa(v.ID),
			Type: strings.ToLower(v.Type),
		}
		versionList = append(versionList, ver)
	}

	sort.Slice(versionList, func(i, j int) bool {
		iId, _ := strconv.Atoi(versionList[i].Id)
		jId, _ := strconv.Atoi(versionList[j].Id)
		return iId > jId
	})

	return structs.Modpack{
		Name:     ftbModpack.Name,
		Id:       strconv.Itoa(ftbModpack.ID),
		Versions: versionList,
	}, nil
}

// GetVersion fetches version details from the FTB API including file lists,
// modloader targets, and memory specifications.
func (m *FTB) GetVersion() (structs.ModpackVersion, error) {
	url := fmt.Sprintf("%s/modpack/%d/%d", ftbApiUrl, m.PackId, m.VersionId)
	pterm.Debug.Printfln("Getting modpack version from ftb using %s", url)
	resp, err := util.DoGet(url)
	if err != nil {
		return structs.ModpackVersion{}, err
	}
	defer resp.Body.Close()

	var ftbModpackVer structs.FTBVersion

	err = json.NewDecoder(resp.Body).Decode(&ftbModpackVer)
	if err != nil {
		return structs.ModpackVersion{}, err
	}

	if ftbModpackVer.Status != "success" {
		return structs.ModpackVersion{}, fmt.Errorf("unsuccessful response: %s, %s", ftbModpackVer.Status, ftbModpackVer.Message)
	}

	var mem structs.Memory
	mem.Minimum = ftbModpackVer.Specs.Minimum
	mem.Recommended = ftbModpackVer.Specs.Recommended

	return structs.ModpackVersion{
		Id:         strconv.Itoa(ftbModpackVer.ID),
		Name:       ftbModpackVer.Name,
		Targets:    parseFTBTargets(ftbModpackVer.Targets),
		Memory:     mem,
		Files:      parseFTBFiles(ftbModpackVer.Files),
		PackFormat: "FTB",
	}, nil
}

// SuccessfulInstall notifies the FTB API that the server was installed successfully.
func (m *FTB) SuccessfulInstall() {
	url := fmt.Sprintf("%s/modpack/%d/%d/serverInstall/success", ftbApiUrl, m.PackId, m.VersionId)
	resp, err := util.DoGet(url)
	if err != nil {
		pterm.Debug.WithMessageStyle(pterm.Error.MessageStyle).Printfln("Error while sending successful install request to ftb: %s", err)
		return
	}
	_ = resp.Body.Close()
}

// FailedInstall notifies the FTB API that the server installation failed.
func (m *FTB) FailedInstall() {
	url := fmt.Sprintf("%s/modpack/%d/%d/serverInstall/failure", ftbApiUrl, m.PackId, m.VersionId)
	resp, err := util.DoGet(url)
	if err != nil {
		pterm.Debug.WithMessageStyle(pterm.Error.MessageStyle).Printfln("Error while sending failed install request to ftb: %s", err)
		return
	}
	_ = resp.Body.Close()
}

// SetVersionId sets the version to fetch. The string value is parsed to int.
func (m *FTB) SetVersionId(versionId string) {
	m.VersionId, _ = strconv.Atoi(versionId)
}

// PrepareFiles is a no-op for FTB since files are downloaded directly
// without needing archive extraction or override processing.
func (m *FTB) PrepareFiles(installDir string) error {
	return nil
}

// Cleanup is a no-op for FTB since no temporary files are created during GetVersion.
func (m *FTB) Cleanup() {}

// parseFTBTargets extracts the modloader, Minecraft, and Java version info
// from the FTB targets array into the common ModpackTargets struct.
func parseFTBTargets(targets []structs.FTBTargets) structs.ModpackTargets {
	var modpackTargets structs.ModpackTargets
	for _, t := range targets {
		if t.Type == "modloader" {
			modpackTargets.ModLoader.Name = t.Name
			modpackTargets.ModLoader.Version = t.Version
		}
		if t.Type == "game" && t.Name == "minecraft" {
			modpackTargets.McVersion = t.Version
		}
		if t.Type == "runtime" && t.Name == "java" {
			modpackTargets.JavaVersion = t.Version
		}
	}
	return modpackTargets
}

// parseFTBFiles converts FTB file entries to the common File format,
// filtering out client-only files since this is a server installer.
func parseFTBFiles(files []structs.FTBFiles) []structs.File {
	var parsedFiles []structs.File
	for _, f := range files {
		if !f.ClientOnly {
			parsedFiles = append(parsedFiles, structs.File{
				ID:       strconv.Itoa(f.ID),
				Name:     f.Name,
				Path:     f.Path,
				Url:      f.URL,
				Hash:     f.Sha1,
				HashType: "sha1",
				Mirrors:  f.Mirrors,
			})
		}
	}
	return parsedFiles
}
