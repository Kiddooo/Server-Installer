package modloaders

import (
	"encoding/json"
	"errors"
	"fmt"
	"ftb-server-downloader/structs"
	"ftb-server-downloader/util"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/pterm/pterm"
)

// fabricMeta is the base URL for the Fabric metadata API.
const fabricMeta = "https://meta.fabricmc.net"

// Fabric implements the ModLoader interface for Fabric-based servers.
type Fabric struct {
	InstallDir      string
	Targets         structs.ModpackTargets
	Memory          structs.Memory
	IsAutoVersion   bool
	FabricInstaller FabricInstaller
}

// FabricInstaller represents an available Fabric installer release
// as returned by the Fabric metadata API.
type FabricInstaller struct {
	URL     string `json:"url"`
	Maven   string `json:"maven"`
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

// GetFabric creates a new Fabric instance by fetching the latest installer
// version from the Fabric metadata API.
func GetFabric(target structs.ModpackTargets, memory structs.Memory, installDir string) (Fabric, error) {
	fabricInstaller, err := getInstaller()
	if err != nil {
		return Fabric{}, err
	}

	return Fabric{
		InstallDir:      installDir,
		Targets:         target,
		Memory:          memory,
		FabricInstaller: fabricInstaller[0],
	}, nil
}

// GetDownload returns the Fabric installer JAR file to download.
func (s Fabric) GetDownload() ([]structs.File, error) {
	var mlFiles []structs.File

	mlFiles = append(mlFiles, structs.File{
		Name:               fmt.Sprintf("fabric-installer-%s.jar", s.FabricInstaller.Version),
		Url:                s.FabricInstaller.URL,
		CheckContentLength: true,
	})

	return mlFiles, nil
}

// Install runs the Fabric installer JAR with the appropriate Minecraft and loader
// versions, then generates a start script. If useOwnJava is true, the bundled JRE
// is used instead of the system Java.
func (s Fabric) Install(useOwnJava bool) error {
	installerName := fmt.Sprintf("fabric-installer-%s.jar", s.FabricInstaller.Version)
	exists, err := util.PathExists(filepath.Join(s.InstallDir, installerName))
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("installer %s does not exist", installerName)
	}

	jrePath := "java"
	if useOwnJava {
		jrePath, err = util.GetJavaPath(s.Targets.JavaVersion)
		if err != nil {
			jrePath = "java"
		} else {
			jrePath = filepath.Join(s.InstallDir, jrePath)
		}
	}

	pterm.Debug.Printfln("JRE Path: %s", jrePath)
	cmd := exec.Command(jrePath, "-jar", installerName, "server", "-mcversion", s.Targets.McVersion, "-loader", s.Targets.ModLoader.Version, "-downloadMinecraft")
	cmd.Dir = s.InstallDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	pterm.Info.Println("Running Fabric installer")
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("error running fabric installer: %s", err.Error())
	}
	if err = cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() != 0 {
				return fmt.Errorf("fabric installer failed with exit code %d", exitErr.ExitCode())
			}
		} else {
			return fmt.Errorf("error waiting for command: %s", err.Error())
		}
	}
	pterm.Success.Println("Fabric installed successfully")
	_ = os.Remove(filepath.Join(s.InstallDir, installerName))

	return s.startScript(useOwnJava)
}

// getInstaller fetches the list of available Fabric installer versions from the
// Fabric metadata API. The first entry is typically the latest stable version.
func getInstaller() ([]FabricInstaller, error) {
	url := fmt.Sprintf("%s/v2/versions/installer", fabricMeta)
	resp, err := util.DoGet(url)
	if err != nil {
		return []FabricInstaller{}, err
	}
	defer resp.Body.Close()
	var fabricInstaller []FabricInstaller

	err = json.NewDecoder(resp.Body).Decode(&fabricInstaller)
	if err != nil {
		return []FabricInstaller{}, err
	}

	return fabricInstaller, nil
}

// startScript generates a start.sh/start.bat script for the Fabric server with
// the configured memory settings, Log4J fix flags, and Java path.
func (s Fabric) startScript(ownJava bool) error {
	pterm.Debug.Println("Use own java:", ownJava)
	var runScriptPath string
	if runtime.GOOS == "windows" {
		runScriptPath = filepath.Join(s.InstallDir, "start.bat")
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		runScriptPath = filepath.Join(s.InstallDir, "start.sh")
	}
	pterm.Debug.Println("runScriptPath:", runScriptPath)

	log4jFix, err := Log4JFixer(s.InstallDir, s.Targets.McVersion)
	if err != nil {
		pterm.Warning.Printfln("Failed to apply log4j fix: %s", err.Error())
	}

	runFile, err := os.OpenFile(runScriptPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer runFile.Close()
	javaPath := "java"
	if ownJava {
		javaPath, err = util.GetJavaPath(s.Targets.JavaVersion)
		if err != nil {
			javaPath = "java"
		}
	}
	runJarName := "fabric-server-launch.jar"
	xmx := s.Memory.Recommended
	if xmx <= 0 {
		xmx = 4096
	}

	if runtime.GOOS == "windows" {
		_, err = runFile.WriteString(fmt.Sprintf("\"%s\" %s -Xmx%dM -jar %s nogui", javaPath, log4jFix, xmx, runJarName))
		if err != nil {
			return err
		}
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		_, err = runFile.WriteString(fmt.Sprintf("#!/usr/bin/env sh\n\"%s\" %s -Xmx%dM -jar %s nogui", javaPath, log4jFix, xmx, runJarName))
		if err != nil {
			return err
		}
	}

	return nil
}
