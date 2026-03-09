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
	"slices"
	"strings"

	semVer "github.com/hashicorp/go-version"
	"github.com/pterm/pterm"
)

const (
	// forgeMaven is the base URL for the MinecraftForge Maven repository.
	forgeMaven = "https://maven.minecraftforge.net"
)

var (
	// versionsToRename lists Minecraft versions where the server JAR must be renamed
	// from "minecraft_server.<version>.jar" to "minecraft_server.jar" for Forge compatibility.
	versionsToRename = []string{
		"1.5.2",
	}
)

// Forge implements the ModLoader interface for MinecraftForge-based servers.
type Forge struct {
	InstallDir string
	Targets    structs.ModpackTargets
	Memory     structs.Memory
}

// GetForge creates a new Forge instance configured with the given modpack targets,
// memory settings, and installation directory.
func GetForge(target structs.ModpackTargets, memory structs.Memory, installDir string) Forge {

	return Forge{
		Targets:    target,
		Memory:     memory,
		InstallDir: installDir,
	}
}

// resolveInstaller determines the correct Forge installer URL and filename by probing
// the Maven repository. It tries three formats in order: modern installer JAR,
// legacy installer JAR (with MC version suffix), and universal ZIP for very old versions.
// If InstallerUrl is set on the targets, that is used directly without probing.
func (s Forge) resolveInstaller() (url, name string, err error) {
	if s.Targets.InstallerUrl != "" {
		u := strings.ReplaceAll(s.Targets.InstallerUrl, "{{@loaderversion@}}", s.Targets.ModLoader.Version)
		u = strings.ReplaceAll(u, "{{@mcversion@}}", s.Targets.McVersion)
		n := fmt.Sprintf("forge-%s-%s-installer.jar", s.Targets.McVersion, s.Targets.ModLoader.Version)
		return u, n, nil
	}

	u := fmt.Sprintf("%s/releases/net/minecraftforge/forge/%s-%s/forge-%s-%s-installer.jar",
		forgeMaven, s.Targets.McVersion, s.Targets.ModLoader.Version,
		s.Targets.McVersion, s.Targets.ModLoader.Version)
	n := fmt.Sprintf("forge-%s-%s-installer.jar", s.Targets.McVersion, s.Targets.ModLoader.Version)
	if doesForgeExist(u) {
		return u, n, nil
	}

	u = fmt.Sprintf("%s/releases/net/minecraftforge/forge/%s-%s-%s/forge-%s-%s-%s-installer.jar",
		forgeMaven, s.Targets.McVersion, s.Targets.ModLoader.Version, s.Targets.McVersion,
		s.Targets.McVersion, s.Targets.ModLoader.Version, s.Targets.McVersion)
	n = fmt.Sprintf("forge-%s-%s-%s-installer.jar", s.Targets.McVersion, s.Targets.ModLoader.Version, s.Targets.McVersion)
	if doesForgeExist(u) {
		return u, n, nil
	}

	u = fmt.Sprintf("%s/releases/net/minecraftforge/forge/%s-%s/forge-%s-%s-universal.zip",
		forgeMaven, s.Targets.McVersion, s.Targets.ModLoader.Version,
		s.Targets.McVersion, s.Targets.ModLoader.Version)
	n = fmt.Sprintf("forge-%s-%s-universal.zip", s.Targets.McVersion, s.Targets.ModLoader.Version)
	if doesForgeExist(u) {
		return u, n, nil
	}

	return "", "", fmt.Errorf("cant find forge version %s", s.Targets.ModLoader.Version)
}

// GetDownload resolves the Forge installer URL by trying multiple Maven path formats
// (modern, legacy with MC version suffix, and universal ZIP for very old versions).
// Returns the installer file to download, or an error if no valid URL is found.
func (s Forge) GetDownload() ([]structs.File, error) {
	installerUrl, jarName, err := s.resolveInstaller()
	if err != nil {
		return nil, err
	}
	return []structs.File{{
		Name:               jarName,
		Url:                installerUrl,
		CheckContentLength: true,
	}}, nil
}

// Install runs the Forge installer JAR (or extracts a universal ZIP for legacy versions),
// renames server JARs if needed, and generates a start script. If useOwnJava is true,
// the bundled JRE is used instead of the system Java.
func (s Forge) Install(useOwnJava bool) error {
	_, jarName, err := s.resolveInstaller()
	if err != nil {
		return err
	}

	exists, err := util.PathExists(filepath.Join(s.InstallDir, jarName))
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("installer %s does not exist", jarName)
	}

	if filepath.Ext(jarName) == ".jar" {
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
		cmd := exec.Command(jrePath, "-jar", jarName, "--installServer")
		cmd.Dir = s.InstallDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		pterm.Info.Println("Running Forge installer")
		if err = cmd.Start(); err != nil {
			return fmt.Errorf("error running forge installer: %s", err.Error())
		}
		if err = cmd.Wait(); err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				if exitErr.ExitCode() != 0 {
					return fmt.Errorf("forge installer failed with exit code %d, error: %s", exitErr.ExitCode(), exitErr.Error())
				}
			} else {
				return fmt.Errorf("error waiting for command: %s", err.Error())
			}
		}
		pterm.Success.Println("Forge installed successfully")
		mcJarWithVer := filepath.Join(s.InstallDir, fmt.Sprintf("minecraft_server.%s.jar", s.Targets.McVersion))
		if mcJar, _ := util.PathExists(mcJarWithVer); mcJar && slices.Contains(versionsToRename, s.Targets.McVersion) {
			err := os.Rename(mcJarWithVer, filepath.Join(s.InstallDir, "minecraft_server.jar"))
			if err != nil {
				pterm.Warning.Println(err)
			}
		}
		_ = os.Remove(filepath.Join(s.InstallDir, jarName))
	} else if filepath.Ext(jarName) == ".zip" {
		pathExists, err := util.PathExists(filepath.Join(s.InstallDir, fmt.Sprintf("minecraft_server.%s.jar", s.Targets.McVersion)))
		if err != nil {
			return err
		}
		if pathExists {
			_ = os.Remove(filepath.Join(s.InstallDir, fmt.Sprintf("minecraft_server.%s.jar", s.Targets.McVersion)))
		}
		vanilla, err := GetVanilla(s.Targets, s.InstallDir)
		if err != nil {
			return err
		}
		vanillaDl, err := vanilla.GetDownload()
		if err != nil {
			return err
		}
		dest := filepath.Join(s.InstallDir, vanillaDl[0].Path, vanillaDl[0].Name)
		fDl, err := util.NewDownload(dest, vanillaDl[0].Url)
		if err != nil {
			return err
		}
		err = fDl.Do()
		if err != nil {
			return err
		}

		err = util.CombineZip(filepath.Join(s.InstallDir, jarName), filepath.Join(s.InstallDir, fmt.Sprintf("minecraft_server.%s.jar", s.Targets.McVersion)))
		if err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(s.InstallDir, jarName))
	}

	err = s.startScript(useOwnJava)
	if err != nil {
		return err
	}

	return nil
}

// doesForgeExist checks whether a Forge artifact exists at the given URL
// by performing an HTTP HEAD request.
func doesForgeExist(url string) bool {
	resp, err := util.DoHead(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return true
}

// startScript generates or patches the server start script. If a run.sh/run.bat already
// exists (created by the Forge installer), it patches in the custom Java path and nogui flag.
// Otherwise, it creates a new start.sh/start.bat with memory settings and Log4J fixes.
func (s Forge) startScript(ownJava bool) error {
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

	log4jFix, err := Log4JFixer(s.InstallDir, s.Targets.McVersion)
	if err != nil {
		pterm.Warning.Printfln("Failed to apply log4j fix: %s", err.Error())
	}

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

			customArgs := fmt.Sprintf("\n-Xmx%dM\n%s", s.Memory.Recommended, log4jFix)
			_, err = argsFile.WriteString(customArgs)
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

		// Rewrite the file with our changes, preserving execute permission
		file, _ = os.OpenFile(runScriptPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
		defer file.Close()

		writer := bufio.NewWriter(file)
		for _, line := range lines {
			_, _ = writer.WriteString(line + "\n")
		}
		_ = writer.Flush()
	} else {
		runFile, err := os.OpenFile(strings.ReplaceAll(runScriptPath, "run", "start"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
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
		dir, err := os.ReadDir(s.InstallDir)
		if err != nil {
			return err
		}
		var runJarName string
		preForgeJarVer, _ := semVer.NewVersion("1.5.1")
		mcVer, _ := semVer.NewVersion(s.Targets.McVersion)

		var re = forgeModernJarRe
		if !mcVer.GreaterThan(preForgeJarVer) {
			re = forgeLegacyJarRe
		}

		var filesInDir []pterm.TreeNode
		for _, file := range dir {
			if !file.IsDir() {
				filesInDir = append(filesInDir, pterm.TreeNode{
					Text: file.Name(),
				})
				matches := re.MatchString(file.Name())
				if matches {
					runJarName = file.Name()
					break
				}
			}
		}

		if pterm.PrintDebugMessages {
			_ = pterm.DefaultTree.WithRoot(pterm.TreeNode{Text: "Files in dir:", Children: filesInDir}).Render()
		}
		pterm.Debug.Println("Runtime jar file:", runJarName)

		if runtime.GOOS == "windows" {
			_, err = runFile.WriteString(fmt.Sprintf("\"%s\" %s -Xmx%dM -jar %s nogui", javaPath, log4jFix, s.Memory.Recommended, runJarName))
			if err != nil {
				return err
			}
		}
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
			_, err = runFile.WriteString(fmt.Sprintf("#!/usr/bin/env sh\n\"%s\" %s -Xmx%dM -jar %s nogui", javaPath, log4jFix, s.Memory.Recommended, runJarName))
			if err != nil {
				return err
			}
		}
	}

	return nil
}
