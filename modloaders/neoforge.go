package modloaders

import (
	"bufio"
	"errors"
	"fmt"
	"ftb-server-downloader/structs"
	"ftb-server-downloader/util"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	semVer "github.com/hashicorp/go-version"
	"github.com/pterm/pterm"
)

// NeoForge implements the ModLoader interface for NeoForge-based servers.
// NeoForge split from MinecraftForge after Minecraft 1.20.1; versions >= 1.20.2
// use a different Maven path and artifact naming scheme (IsAfterSplit).
type NeoForge struct {
	InstallDir   string
	Targets      structs.ModpackTargets
	Memory       structs.Memory
	IsAfterSplit bool
}

// neoForgeMaven is the base URL for the NeoForge Maven repository.
const neoForgeMaven = "https://maven.neoforged.net"

// GetNeoForge creates a new NeoForge instance. It determines whether the target
// Minecraft version is after the Forge/NeoForge split (>= 1.20.2) to select
// the correct Maven artifact path.
func GetNeoForge(target structs.ModpackTargets, memory structs.Memory, installDir string) NeoForge {
	// After 1.20.2 NeoForge changed their package names
	isAfterSplit := false
	if target.McVersion != "" {
		mcVersion, _ := semVer.NewVersion(target.McVersion)
		breakingMcVersion, _ := semVer.NewVersion("1.20.2")
		if mcVersion != nil && mcVersion.GreaterThanOrEqual(breakingMcVersion) {
			isAfterSplit = true
		}
	}

	return NeoForge{
		Targets:      target,
		Memory:       memory,
		IsAfterSplit: isAfterSplit,
		InstallDir:   installDir,
	}
}

// GetDownload returns the NeoForge installer JAR file to download. The artifact
// path depends on whether the target is after the Forge/NeoForge split.
func (s NeoForge) GetDownload() ([]structs.File, error) {
	var mlFiles []structs.File

	// Use custom installer URL if provided (e.g., from ServerStarter config)
	if s.Targets.InstallerUrl != "" {
		installerUrl := strings.ReplaceAll(s.Targets.InstallerUrl, "{{@loaderversion@}}", s.Targets.ModLoader.Version)
		jarName := fmt.Sprintf("neoforge-%s-installer.jar", s.Targets.ModLoader.Version)
		mlFiles = append(mlFiles, structs.File{
			Name:               jarName,
			Url:                installerUrl,
			CheckContentLength: true,
		})
		return mlFiles, nil
	}

	installerUrl := fmt.Sprintf("%s/releases/net/neoforged/neoforge/%s/neoforge-%s-installer.jar", neoForgeMaven, s.Targets.ModLoader.Version, s.Targets.ModLoader.Version)
	jarName := fmt.Sprintf("neoforge-%s-installer.jar", s.Targets.ModLoader.Version)
	if !s.IsAfterSplit {
		jarName = fmt.Sprintf("forge-%s-%s-installer.jar", s.Targets.McVersion, s.Targets.ModLoader.Version)
		installerUrl = fmt.Sprintf("%s/releases/net/neoforged/forge/%s-%s/forge-%s-%s-installer.jar", neoForgeMaven, s.Targets.McVersion, s.Targets.ModLoader.Version, s.Targets.McVersion, s.Targets.ModLoader.Version)
	}

	mlFiles = append(mlFiles, structs.File{
		Name:               jarName,
		Url:                installerUrl,
		CheckContentLength: true,
	})
	return mlFiles, nil
}

// Install runs the NeoForge installer JAR and generates a start script.
// If useOwnJava is true, the bundled JRE is used instead of the system Java.
func (s NeoForge) Install(useOwnJava bool) error {
	var installerName string
	if s.Targets.InstallerUrl != "" {
		// When a custom installer URL is provided (e.g. from ServerStarter config),
		// the downloaded file is always named neoforge-<version>-installer.jar.
		installerName = fmt.Sprintf("neoforge-%s-installer.jar", s.Targets.ModLoader.Version)
	} else if s.IsAfterSplit {
		installerName = fmt.Sprintf("neoforge-%s-installer.jar", s.Targets.ModLoader.Version)
	} else {
		installerName = fmt.Sprintf("forge-%s-%s-installer.jar", s.Targets.McVersion, s.Targets.ModLoader.Version)
	}
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
	cmd := exec.Command(jrePath, "-jar", installerName, "--installServer")
	cmd.Dir = s.InstallDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	pterm.Info.Println("Running NeoForge installer")
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("error running neoforge installer: %s", err.Error())
	}
	if err = cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() != 0 {
				return fmt.Errorf("neoforge installer failed with exit code %d", exitErr.ExitCode())
			}
		} else {
			return fmt.Errorf("error waiting for command: %s", err.Error())
		}
	}
	pterm.Success.Println("NeoForge installed successfully")
	// _ = os.Remove(filepath.Join(s.InstallDir, installerName) + ".log")
	_ = os.Remove(filepath.Join(s.InstallDir, installerName))

	err = s.startScript(useOwnJava)
	if err != nil {
		return err
	}
	return nil
}

// startScript generates or patches the server start script for NeoForge.
// If a run.sh/run.bat exists, it patches the Java path and adds nogui.
// It also appends memory settings to user_jvm_args.txt if not already present.
func (s NeoForge) startScript(ownJava bool) error {
	pterm.Debug.Println("Use own java:", ownJava)
	argsFilePath := filepath.Join(s.InstallDir, "user_jvm_args.txt")
	var runScriptPath string
	if runtime.GOOS == "windows" {
		runScriptPath = filepath.Join(s.InstallDir, "run.bat")
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		runScriptPath = filepath.Join(s.InstallDir, "run.sh")
	}
	pterm.Debug.Println("argFilePath:", argsFilePath)
	pterm.Debug.Println("runScriptPath:", runScriptPath)

	if argsExist, _ := util.PathExists(argsFilePath); argsExist {
		argsFile, err := os.Open(argsFilePath)
		if err != nil {
			return err
		}
		defer argsFile.Close()

		scanner := bufio.NewScanner(argsFile)
		hasXmx := false
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "-Xmx") {
				hasXmx = true
				break
			}
		}

		if !hasXmx && s.Memory.Recommended > 0 {
			argsFile, err = os.OpenFile(argsFilePath, os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				return err
			}
			defer argsFile.Close()

			_, err = argsFile.WriteString(fmt.Sprintf("\n-Xmx%dM", s.Memory.Recommended))
			if err != nil {
				return err
			}
		}
	}

	if runExists, _ := util.PathExists(runScriptPath); runExists {
		pterm.Debug.Println("Parsing run script")
		file, _ := os.Open(runScriptPath)
		defer file.Close()

		var lines []string
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()

			if javaLineRe.MatchString(line) {
				if ownJava {
					pterm.Debug.Println("Replacing java path in run script")
					javaPath, err := util.GetJavaPath(s.Targets.JavaVersion)
					if err != nil {
						return err
					}
					line = javaStartRe.
						ReplaceAllString(line, fmt.Sprintf("\"%s\"", javaPath))
				}

				if runtime.GOOS == "windows" {
					line = winArgsRe.
						ReplaceAllString(line, "nogui %*")
				} else if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
					line = unixArgsRe.
						ReplaceAllString(line, "nogui \"$@\"")
				}
			}
			lines = append(lines, line)
		}

		_ = file.Close()

		// Rewrite the file with our changes
		file, _ = os.Create(runScriptPath)
		defer file.Close()

		writer := bufio.NewWriter(file)
		for _, line := range lines {
			_, _ = writer.WriteString(line + "\n")
		}
		_ = writer.Flush()
	}

	return nil
}
