package main

import (
	"bufio"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"ftb-server-downloader/modloaders"
	"ftb-server-downloader/repos"
	"ftb-server-downloader/structs"
	"ftb-server-downloader/util"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codeclysm/extract/v4"
	"github.com/pterm/pterm"
	"github.com/pterm/pterm/putils"
	"golang.org/x/term"
)

var (
	packId        string
	versionId     string
	installDir    string
	threads       int
	provider      string
	auto          bool
	force         bool
	latest        bool
	apiKey        string
	validate      bool
	skipModloader bool
	noJava        bool
	noColours     bool
	dlTimeout     int
	acceptEula    bool
	verbose       bool

	logFile *os.File
)

func init() {
	if runtime.GOOS == "linux" && !term.IsTerminal(int(os.Stdin.Fd())) {
		util.RelaunchInTerminal()
		return
	}

	if util.ReleaseVersion == "" || util.ReleaseVersion == "main" {
		util.ReleaseVersion = "v0.0.0-beta.0"
	}

	if util.GitCommit == "" {
		util.GitCommit = "Dev"
	}

	userAgentVersion := util.ReleaseVersion
	if strings.HasPrefix(util.ReleaseVersion, "v") {
		userAgentVersion = strings.TrimPrefix(util.ReleaseVersion, "v")
	}

	util.UserAgent = fmt.Sprintf("server-installer/%s", userAgentVersion)
}

func main() {
	flag.StringVar(&provider, "provider", "ftb", "Modpack provider ('ftb', 'curseforge', or 'modrinth')")
	flag.StringVar(&packId, "pack", "", "Modpack ID (numeric for ftb/curseforge, alphanumeric slug or ID for modrinth)")
	flag.StringVar(&versionId, "version", "", "Modpack version ID, if not provided, the latest version will be used")
	flag.StringVar(&installDir, "dir", "", "Installation directory")
	flag.BoolVar(&auto, "auto", false, "Dont ask questions, just install the server")
	flag.BoolVar(&latest, "latest", false, "Gets the latest (alpha/beta/release) version of the modpack")
	flag.BoolVar(&force, "force", false, "Force the modpack install, dont ask questions just continue (only works with -auto)")
	flag.IntVar(&threads, "threads", runtime.NumCPU()*2, "Number of threads to use (Default: number of CPU cores)")
	flag.StringVar(&apiKey, "apikey", "public", "API key for the selected provider (FTB private packs, CurseForge API key)")
	flag.BoolVar(&validate, "validate", false, "Validate the modpack after install")
	flag.BoolVar(&skipModloader, "skip-modloader", false, "Skip installing the modloader")
	flag.BoolVar(&noJava, "no-java", false, "Do not install Java")
	justFiles := flag.Bool("just-files", false, "Only download the files, do not install java or the modloader")
	flag.BoolVar(&noColours, "no-colours", false, "Do not display console/terminal colours")
	flag.IntVar(&dlTimeout, "timeout", 120, "File download timeout in seconds")
	flag.BoolVar(&acceptEula, "accept-eula", false, "Accept the EULA for Minecraft. By using this flag you are indicating your agreement to Minecraft's EULA (https://account.mojang.com/documents/minecraft_eula)")
	flag.BoolVar(&verbose, "verbose", false, "Verbose output")
	flag.Parse()

	// Threads cannot be less than 1
	if threads < 1 {
		// Default to number of CPU cores * 2
		threads = runtime.NumCPU() * 2
	}

	if *justFiles {
		noJava = true
		skipModloader = true
	}

	var err error
	logFile, err = os.OpenFile("server-installer.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		panic(err)
	}

	util.LogMw = io.MultiWriter(os.Stdout, util.NewCustomWriter(logFile))
	// Temp fix for loggers not logging to file
	pterm.Debug.Writer = nil
	pterm.Info.Writer = nil
	pterm.Warning.Writer = nil
	pterm.Error.Writer = nil
	pterm.Fatal.Writer = nil
	pterm.Success.Writer = nil
	pterm.Description.Writer = nil

	log.SetOutput(util.LogMw)
	pterm.SetDefaultOutput(util.LogMw)

	pterm.Debug.Prefix = pterm.Prefix{
		Text:  "DEBUG",
		Style: pterm.NewStyle(pterm.BgLightMagenta, pterm.FgBlack),
	}
	pterm.Debug.MessageStyle = pterm.NewStyle(98)

	pterm.Info.Prefix = pterm.Prefix{
		Text:  "INFO",
		Style: pterm.NewStyle(pterm.BgYellow, pterm.FgBlack),
	}
	pterm.Info.MessageStyle = pterm.NewStyle(pterm.FgYellow)

	if noColours {
		pterm.DisableStyling()
	}

	logo, _ := pterm.DefaultBigText.WithLetters(
		putils.LettersFromStringWithStyle("S", pterm.NewStyle(pterm.FgCyan)),
		putils.LettersFromStringWithStyle("I", pterm.NewStyle(pterm.FgMagenta))).Srender()
	pterm.DefaultCenter.Println(logo)
	pterm.DefaultCenter.WithCenterEachLineSeparately().Printfln("Server installer version: %s(%s)\n%s", util.ReleaseVersion, util.GitCommit, time.Now().UTC().Format(time.RFC1123))
	pterm.DefaultCenter.WithCenterEachLineSeparately().Println(pterm.Bold.Sprintf("Installer Issue tracker\nhttps://github.com/Kiddooo/Server-Installer/issues"))

	versionInfo, err := checkForUpdate()
	if err != nil {
		pterm.Warning.Printfln("Error checking for installer update: %v", err)
	}
	if versionInfo.UpdateAvailable {
		pterm.Info.Printfln("Installer update available:\nCurrent version: %s\nLatest version: %s", versionInfo.CurrentVersion, versionInfo.LatestVersion)
		pterm.Println()
		// Skip the update auto flag is set
		if !auto {
			update := util.ConfirmYN(
				fmt.Sprintf("Do you want to update the installer to version %s?", versionInfo.LatestVersion),
				true,
				pterm.Info.MessageStyle,
			)
			if update {
				pterm.Info.Println("Downloading update...")
				err = doUpdate(versionInfo)
				if err != nil {
					pterm.Error.Printfln("Error updating installer: %s", err.Error())

				}
			}
		}
	}

	if verbose {
		pterm.EnableDebugMessages()
		pterm.Debug.Println("Verbose output enabled")
	}

	abs, err := filepath.Abs(installDir)
	if err != nil {
		pterm.Fatal.Println("Error getting absolute path:", err.Error())
	}
	installDir = abs

	defer logFile.Close()
	// Get the pack ID and version ID from the installer name if not provided as flags
	if packId == "" {
		pId, vId, err := util.ParseInstallerName(filepath.Base(os.Args[0]))
		if err != nil {
			pterm.Warning.Println("Unable to parse installer name for modpack and version id:", err)
			pId, vId, err = modpackQuestion()
			if err != nil {
				pterm.Fatal.Println(err)
			}
		}
		packId = strconv.Itoa(pId)
		if vId != 0 && versionId == "" {
			versionId = strconv.Itoa(vId)
		}
	}

	// Get the provider
	selectedProvider, err := getProvider()
	if err != nil {
		pterm.Fatal.Printfln("Error getting provider: %s\nValid providers are 'ftb', 'curseforge', 'modrinth'", err.Error())
	}
	pterm.Debug.Printfln("Got provider '%s'", provider)

	var filesToDownload []structs.File

	if selectedProvider == nil {
		pterm.Fatal.Println("No provider selected")
		return
	}

	// Get modpack details from the provider
	modpack, err := selectedProvider.GetModpack()
	if err != nil {
		selectedProvider.FailedInstall()
		pterm.Error.Println("Error getting modpack:", err.Error())
		os.Exit(1)
	}
	pterm.Debug.Printfln("Modpack: %+v", modpack)

	// Get the latest version id if not provided or if the latest flag is set
	if versionId == "" || latest {
		latestVersion, err := getLatestRelease(modpack.Versions, latest)
		if err != nil {
			pterm.Error.Println("Error getting latest release:", err.Error())
			os.Exit(1)
		}
		selectedProvider.SetVersionId(latestVersion.Id)
		pterm.Debug.Printfln("No version provided or latest flag set, using latest version: %s", latestVersion.Id)
	}

	// Get the version information for the modpack from the provider
	modpackVersion, err := selectedProvider.GetVersion()
	if err != nil {
		selectedProvider.FailedInstall()
		pterm.Error.Println("Error getting modpack version:", err.Error())
		os.Exit(1)
	}
	// Ensure any temp files (downloaded zips) are cleaned up on all exit paths.
	// PrepareFiles also removes them, so this is a safe no-op if we get that far.
	defer selectedProvider.Cleanup()
	filesToDownload = append(filesToDownload, modpackVersion.Files...)

	// build the version manifest
	manifest := structs.Manifest{
		Id:             modpack.Id,
		Name:           modpack.Name,
		VersionName:    modpackVersion.Name,
		VersionId:      modpackVersion.Id,
		Provider:       provider,
		ModpackTargets: modpackVersion.Targets,
		Files:          modpackVersion.Files,
	}

	// Check if the install location exists, if it doesn't, ask if they want to create the folder(s)
	exists, err := util.PathExists(installDir)
	if err != nil {
		selectedProvider.FailedInstall()
		pterm.Fatal.Println("Unable to check if path exists:", err.Error())
	}
	mkdir := true
	if !exists {
		if !auto {
			mkdir = util.ConfirmYN(fmt.Sprintf("Install folder does not exists, do you want to create it? (%s)", installDir), true, pterm.Info.MessageStyle)
			if !mkdir {
				pterm.Error.Println("Installation path does not exist...")
				os.Exit(1)
			}
		}
	}

	var updatedFiles, removedFiles, unchangedFiles []structs.File
	updateMsg := ""
	isUpdate := false
	var previousManifest structs.Manifest
	if exists {
		manifestExists, err := util.PathExists(filepath.Join(installDir, util.ManifestName))
		if err != nil {
			return
		}

		if !manifestExists {
			installDirEmpty, err := util.IsEmptyDir(installDir)
			if err != nil {
				selectedProvider.FailedInstall()
				pterm.Fatal.Println("Error checking if directory is empty:", err.Error())
			}

			if !installDirEmpty {
				if !auto {
					pterm.Warning.Printfln("Install directory is not empty, installing the modpack may cause issues")
					cont := util.ConfirmYN("Would you like to continue?", false, pterm.Warning.MessageStyle)
					if !cont {
						pterm.Error.Println("Installation path is not empty, exiting...")
						os.Exit(1)
					}
				}
				if auto && !force {
					pterm.Warning.Printfln("Install directory is not empty, installing the modpack may cause issues")
					pterm.Warning.Printfln("To force install use the -force flag")
					os.Exit(1)
				}
			}
		}

		if manifestExists {
			existingManifest, err := util.ReadManifest(installDir)
			if err != nil {
				selectedProvider.FailedInstall()
				pterm.Fatal.Println("Error reading manifest:", err.Error())
			}
			previousManifest = existingManifest

			/*
				Check the manifest to see if it's the same modpack installed, if it's not the same modpack then ask the user
				if they intend to install a different modpack and the issues that can arise for it.
				If auto is specified but not the force flag show a warning and exit
			*/
			isSamePack := isSameModpack(existingManifest, manifest)

			if !isSamePack {
				if !auto && !force {
					pterm.Warning.Printfln("You currently have a different modpack installed, installing this modpack may cause issues")
					cont := util.ConfirmYN("Would you like to continue?", false, pterm.Warning.MessageStyle)
					if !cont {
						os.Exit(1)
					}
				}
				if auto && !force {
					pterm.Warning.Printfln("You currently have a different modpack installed, installing this modpack may cause issues")
					pterm.Warning.Printfln("To force install use the -force flag")
					os.Exit(1)
				}
			}

			/*
				Check if the modpack is the same version, if it's not compute the differences based on the manifest
			*/
			sameVersion := isSameModpackVersion(existingManifest, manifest)

			if !sameVersion && isSamePack {
				isUpdate, err = checkUpdate(existingManifest, manifest)
				if err != nil {
					selectedProvider.FailedInstall()
					pterm.Fatal.Println("Check Update error:", err.Error())
				}

				if isUpdate {
					existingManifest, err := util.ReadManifest(installDir)
					if err != nil {
						selectedProvider.FailedInstall()
						pterm.Fatal.Println("Error reading manifest:", err.Error())
						return
					}
					updatedFiles, removedFiles, unchangedFiles, err = computeUpdatedFiles(existingManifest.Files, manifest.Files)
					if err != nil {
						return
					}
					filesToDownload = removeUnchangedFiles(filesToDownload, unchangedFiles)
				}
			}
		}
	}

	// set up the modloader getter and installer
	modLoader, err := getModLoader(modpackVersion.Targets, modpackVersion.Memory)
	if err != nil {
		selectedProvider.FailedInstall()
		pterm.Error.Println("Error getting modloader:", err.Error())
		os.Exit(1)
	}

	// Add the modloader downloads to the files list
	mlDownloads, err := modLoader.GetDownload()
	if err != nil {
		selectedProvider.FailedInstall()
		pterm.Fatal.Println("Error getting mod loader downloads:", err.Error())
	}
	filesToDownload = append(filesToDownload, mlDownloads...)

	if isUpdate {
		updateMsg = fmt.Sprintf("\nUpdate: %s (%s) → %s (%s)",
			previousManifest.VersionName, previousManifest.VersionId,
			modpackVersion.Name, modpackVersion.Id,
		)
		if previousManifest.ModpackTargets.McVersion != modpackVersion.Targets.McVersion && modpackVersion.Targets.McVersion != "" {
			updateMsg += fmt.Sprintf("\nMinecraft: %s → %s", previousManifest.ModpackTargets.McVersion, modpackVersion.Targets.McVersion)
		}
		if previousManifest.ModpackTargets.ModLoader.Version != modpackVersion.Targets.ModLoader.Version && modpackVersion.Targets.ModLoader.Version != "" {
			updateMsg += fmt.Sprintf("\n%s: %s → %s",
				modpackVersion.Targets.ModLoader.Name,
				previousManifest.ModpackTargets.ModLoader.Version,
				modpackVersion.Targets.ModLoader.Version,
			)
		}
		updateMsg += fmt.Sprintf("\nUnchanged: %d | Changed: %d | Removed: %d", len(unchangedFiles), len(updatedFiles), len(removedFiles))
		updateMsg += fileListSummary("Changed", updatedFiles)
		updateMsg += fileListSummary("Removed", removedFiles)
	}

	pterm.Debug.Printfln("Files to download: %d", len(filesToDownload))

	// Show a quick overview of the pack they are installing then ask if they want to continue with downloading the pack
	memStr := ""
	if modpackVersion.Memory.Recommended > 0 {
		memStr = fmt.Sprintf("\nMemory: %d MB", modpackVersion.Memory.Recommended)
		if modpackVersion.Memory.Minimum > 0 {
			memStr += fmt.Sprintf(" (min: %d MB)", modpackVersion.Memory.Minimum)
		}
	}
	packFormatStr := ""
	if modpackVersion.PackFormat != "" {
		packFormatStr = fmt.Sprintf("\nPack Format: %s", modpackVersion.PackFormat)
	}
	pterm.Info.Printfln("Fetched modpack:\nName: %s (%s)\nProvider: %s\nVersion: %s (%s)\nMinecraft: %s\nModLoader: %s (%s)\nJava: %s\nFiles: %d\nThreads: %d%s%s\nIs Update: %t%s\nInstall Path: %s",
		modpack.Name, modpack.Id,
		provider,
		modpackVersion.Name, modpackVersion.Id,
		modpackVersion.Targets.McVersion,
		modpackVersion.Targets.ModLoader.Name, modpackVersion.Targets.ModLoader.Version,
		modpackVersion.Targets.JavaVersion,
		len(filesToDownload),
		threads,
		memStr,
		packFormatStr,
		isUpdate, updateMsg,
		installDir,
	)
	if !auto {
		cont := util.ConfirmYN("Do you want to continue?", true, pterm.Info.MessageStyle)
		if !cont {
			os.Exit(1)
		}
	}
	// Ask the user if they want to download java then set the noJava flag depending on their answer
	var java structs.File
	jreAlreadyExists := false
	jrePath, _ := util.GetJavaPath(modpackVersion.Targets.JavaVersion)
	if _, err = os.Stat(filepath.Join(installDir, jrePath)); err == nil {
		jreAlreadyExists = true
	}

	// If noJava is set, or we already have java downloaded, we skip the java download
	if !noJava && !auto && !jreAlreadyExists {
		noJava = !util.ConfirmYN("Do you want to download java?", true, pterm.Info.MessageStyle)
	}
	if !noJava && !jreAlreadyExists {
		java, err = util.GetJava(modpackVersion.Targets.JavaVersion)
		if err != nil {
			selectedProvider.FailedInstall()
			pterm.Fatal.Println("Error getting java:", err.Error())
		}
		filesToDownload = append(filesToDownload, java)
	}

	if mkdir {
		err = os.MkdirAll(installDir, 0777)
		if err != nil {
			selectedProvider.FailedInstall()
			pterm.Fatal.Println("Unable to create install directory:", err.Error())
		}
	} else {
		pterm.Error.Println("Installation path does not exist...")
		os.Exit(1)
	}

	if isUpdate {
		for _, f := range removedFiles {
			err := os.Remove(filepath.Join(installDir, f.Path, f.Name))
			if err != nil {
				pterm.Error.Printfln("Removing files error: %s", err.Error())
				continue
			}
		}

		// For now, we remove the files that have been updated so they can be freshly downloaded.
		for _, f := range updatedFiles {
			err := os.Remove(filepath.Join(installDir, f.Path, f.Name))
			if err != nil {
				pterm.Error.Printfln("Removing update files error: %s", err.Error())
				continue
			}
		}

		// Remove unchanged files from filesToDownload, we don't want to re-download unchanged files
		for _, f := range unchangedFiles {
			for i, v := range filesToDownload {
				if v.Name == f.Name && v.Path == f.Path {
					filesToDownload = append(filesToDownload[:i], filesToDownload[i+1:]...)
				}
			}
		}
	}

	// download the modpack files
	pterm.Info.Printfln("Starting mod pack download...")
	err = downloadFiles(filesToDownload...)
	if err != nil {
		selectedProvider.FailedInstall()
		pterm.Fatal.Println(err.Error())
	}

	pterm.Success.Printfln("Modpack files downloaded")

	// Run provider-specific post-download steps (e.g., extracting overrides from CF/Modrinth archives)
	err = selectedProvider.PrepareFiles(installDir)
	if err != nil {
		selectedProvider.FailedInstall()
		pterm.Fatal.Println("Error preparing provider files:", err.Error())
	}

	// If we downloaded java, extract the files to a jre folder
	if !noJava && !jreAlreadyExists {

		javaFile, err := os.Open(filepath.Join(installDir, java.Name))
		if err != nil {
			selectedProvider.FailedInstall()
			pterm.Fatal.Println("Error opening java archive", err.Error())
		}
		javaPkg := bufio.NewReader(javaFile)

		var shift = func(path string) string {
			// Apparently zips in windows can use / instead of \
			// So we need to check if the path is using / or \
			sep := filepath.Separator
			if len(strings.Split(path, "\\")) > 1 {
				sep = '\\'
			} else if len(strings.Split(path, "/")) > 1 {
				sep = '/'
			}

			parts := strings.Split(path, string(sep))
			parts = parts[1:]
			join := strings.Join(parts, string(sep))
			return join
		}
		err = extract.Archive(context.TODO(), javaPkg, filepath.Join(installDir, "jre", modpackVersion.Targets.JavaVersion), shift)
		if err != nil {
			selectedProvider.FailedInstall()
			pterm.Fatal.Println("Error extracting java archive:", err.Error())
		}
		_ = javaFile.Close()
		err = os.Remove(filepath.Join(installDir, java.Name))
		if err != nil {
			pterm.Warning.Println("Error removing java archive:", err.Error())
		}
	}

	// Ask if the user would like to run the modloader installer
	// todo: if the modloader is already installed check if its the same and ignore the update
	if !auto && !skipModloader {
		skipModloader = !util.ConfirmYN(
			fmt.Sprintf("Would you like to run the %s installer?", modpackVersion.Targets.ModLoader.Name),
			true,
			pterm.Info.MessageStyle,
		)
	}
	if noJava && !util.OsJavaExists() {
		// Revisit this, and possibly ask if they want to download java
		pterm.Warning.Printfln("Java is not installed, skipping modloader installer")
		skipModloader = true
	}
	if !skipModloader {
		err = modLoader.Install(!noJava)
		if err != nil {
			selectedProvider.FailedInstall()
			pterm.Fatal.Println("ModLoader installer error:", err.Error())
		}
	}

	// if the validate flag has been enabled, validate the files we downloaded and check if they match what they should be
	if validate {
		err = runValidation(manifest)
		if err != nil {
			selectedProvider.FailedInstall()
			pterm.Fatal.Println("Error running validation:", err.Error())
		}
	}

	// write the version manifest
	err = util.WriteManifest(installDir, manifest)
	if err != nil {
		selectedProvider.FailedInstall()
		pterm.Fatal.Println("Error creating manifest:", err.Error())
	}

	selectedProvider.SuccessfulInstall()
	if acceptEula {
		// set eula=true in the eula.txt file
		eulaFile := filepath.Join(installDir, "eula.txt")
		err = os.WriteFile(eulaFile, []byte("#By changing the setting below to TRUE you are indicating your agreement to our EULA (https://account.mojang.com/documents/minecraft_eula).\neula=true\n"), 0644)
		if err != nil {
			pterm.Error.Println("Error writing eula.txt file:", err.Error())
		}
	}
	pterm.Success.Println("Modpack installed successfully")
}

// getProvider creates and returns the appropriate ModpackRepo implementation
// based on the -provider flag. It also handles API key resolution from
// environment variables when the key is not explicitly provided.
func getProvider() (repos.ModpackRepo, error) {
	// Resolve API key from environment variables if not explicitly set.
	// Provider-specific env vars take priority over the generic FTB key.
	if apiKey == "public" {
		switch provider {
		case "curseforge":
			if envAPIKey, ok := os.LookupEnv("CURSEFORGE_API_KEY"); ok {
				apiKey = envAPIKey
			}
		default:
			if envAPIKey, ok := os.LookupEnv("FTB_MODPACK_API_KEY"); ok {
				apiKey = envAPIKey
			}
		}
	}
	// Set the global API key for FTB auth in util.makeRequest
	util.ApiKey = apiKey

	switch provider {
	case "ftb":
		return repos.GetFTB(packId, versionId), nil
	case "curseforge":
		if apiKey == "public" || apiKey == "" {
			return nil, fmt.Errorf("CurseForge requires an API key. Set -apikey or the CURSEFORGE_API_KEY environment variable.\nGet a key at https://console.curseforge.com")
		}
		return repos.GetCurseForge(packId, versionId, apiKey)
	case "modrinth":
		return repos.GetModrinth(packId, versionId), nil
	default:
		return nil, fmt.Errorf("'%s' not recognised. Valid providers: ftb, curseforge, modrinth", provider)
	}
}

// getModLoader returns the correct modloader implementation for the pack's
// modloader target (forge, neoforge, or fabric).
func getModLoader(targets structs.ModpackTargets, memory structs.Memory) (modloaders.ModLoader, error) {
	switch targets.ModLoader.Name {
	case "neoforge":
		return modloaders.GetNeoForge(targets, memory, installDir), nil
	case "fabric":
		return modloaders.GetFabric(targets, memory, installDir)
	case "forge":
		return modloaders.GetForge(targets, memory, installDir), nil
	default:
		return nil, fmt.Errorf("'%s' not recognised", targets.ModLoader.Name)
	}
}

// downloadFiles downloads all given files concurrently using the configured
// number of threads. It displays a progress bar during the download.
func downloadFiles(files ...structs.File) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	// Use atomic to keep track of the progress bar
	var pCount atomic.Uint64
	threadLimit := make(chan struct{}, threads)

	p, _ := pterm.DefaultProgressbar.WithTitle("Downloading...").WithTotal(len(files)).Start()

	for _, file := range files {
		wg.Add(1)
		threadLimit <- struct{}{}
		fileCopy := file
		go func(f structs.File) {
			defer func() {
				<-threadLimit
				count := pCount.Add(1)
				if count%5 == 0 || count == uint64(len(files)) {
					mu.Lock()
					p.Current = int(count)
					mu.Unlock()
				}
				wg.Done()
			}()
			err := doDownload(f)
			if err != nil {
				pterm.Error.Printfln("Failed to download file: %s\nAll mirrors failed\n%s", f.Name, err.Error())
				pterm.Debug.Println(err)
				os.Exit(1)
			}
		}(fileCopy)
	}
	// Wait for all downloads to finish
	wg.Wait()

	// Update the progress bar to show that the downloads are complete
	p.Current = int(pCount.Load())
	_, err := p.UpdateTitle("Download complete").Stop()
	if err != nil {
		return err
	}

	return nil
}

// doDownload handles downloading a single file with retry logic and mirror
// failover. Each mirror is attempted up to 3 times with exponential backoff
// before moving to the next mirror.
func doDownload(file structs.File) error {
	destPath := filepath.Join(installDir, file.Path, file.Name)
	mirrors := append([]string{file.Url}, file.Mirrors...)
	// MirrorHeaders is parallel to file.Mirrors (index 0 = primary URL has no
	// extra headers; index i+1 corresponds to file.MirrorHeaders[i]).
	mirrorHeaders := append([]map[string]string{nil}, file.MirrorHeaders...)

	for m, mirror := range mirrors {
		for attempts := 0; attempts < 3; attempts++ {
			pterm.Debug.Printfln("Downloading file: %s from %s | attempt: %d | Mirrors %d", file.Name, mirror, attempts+1, len(mirrors))

			dl, err := util.NewDownload(destPath, mirror)
			if err != nil {
				pterm.Error.Printfln("Error creating download: %s", err.Error())
				c, b, err := util.FailedDownloadHandler(attempts, m, file, mirror, mirrors)
				if err != nil {
					return err
				} else if b {
					break
				} else if c {
					continue
				}
			}
			if dl == nil {
				return fmt.Errorf("download object is nil for file %s", file.Name)
			}
			if m < len(mirrorHeaders) && mirrorHeaders[m] != nil {
				dl.SetHeaders(mirrorHeaders[m])
			}
			if file.Hash != "" {
				hexHash, _ := hex.DecodeString(file.Hash)
				switch file.HashType {
				case "sha1":
					dl.SetChecksum(sha1.New(), hexHash, true)
				case "sha256":
					dl.SetChecksum(sha256.New(), hexHash, true)
				case "md5":
					dl.SetChecksum(md5.New(), hexHash, true)
				default:
					pterm.Warning.Printfln("Unsupported hash type: %s", file.HashType)
				}
			}
			dl.CheckContentLength(file.CheckContentLength)
			dl.SetTimeout(time.Duration(dlTimeout) * time.Second)
			err = dl.Do()
			if err != nil {
				pterm.Error.Printfln("Download request error: %s", err.Error())
				c, b, err := util.FailedDownloadHandler(attempts, m, file, mirror, mirrors)
				if err != nil {
					return err
				} else if b {
					break
				} else if c {
					continue
				}
			}

			return nil
		}
	}
	return nil
}

// runValidation verifies the checksums of all downloaded files against the
// expected hashes in the manifest. Files that fail validation can optionally
// be re-downloaded.
func runValidation(manifest structs.Manifest) error {
	var invalidFiles []structs.File
	for _, f := range manifest.Files {
		if f.HashType != "" && f.Hash != "" {
			fileHash, err := util.FileHash(filepath.Join(installDir, f.Path, f.Name), f.HashType)
			if err != nil {
				pterm.Error.Println("Error getting file hash:", err.Error())
				continue
			}
			if fileHash != f.Hash {
				pterm.Warning.Printfln("Unexpected file hash from %s\nExpected: %s\nGot: %s", f.Name, f.Hash, fileHash)
				invalidFiles = append(invalidFiles, f)
			}
		}
	}

	if len(invalidFiles) > 0 {
		if !auto {
			show := util.ConfirmYN(
				fmt.Sprintf("%d files failed validation, would you like to repair them?", len(invalidFiles)),
				true,
				pterm.Info.MessageStyle,
			)
			if !show {
				return nil
			}
		}

		err := downloadFiles(invalidFiles...)
		if err != nil {
			return err
		}
	}

	return nil
}

// isSameModpack checks whether two manifests refer to the same modpack
// by comparing their IDs.
func isSameModpack(currentManifest, newManifest structs.Manifest) bool {
	return currentManifest.Id == newManifest.Id
}

// isSameModpackVersion checks whether two manifests refer to the same
// modpack AND version.
func isSameModpackVersion(currentManifest, newManifest structs.Manifest) bool {
	return currentManifest.Id == newManifest.Id && currentManifest.VersionId == newManifest.VersionId
}

// checkUpdate determines if the version change is an upgrade or downgrade
// and prompts the user for confirmation on downgrades.
func checkUpdate(currentManifest, newManifest structs.Manifest) (isUpdate bool, err error) {
	if currentManifest.Id != newManifest.Id {
		return false, errors.New("mismatched modpack")
	}

	if currentManifest.VersionId != newManifest.VersionId {
		// For string-based version IDs, we can't always compare numerically.
		// Try numeric comparison first, fall back to treating any difference as an update.
		curNum, errCur := strconv.Atoi(currentManifest.VersionId)
		newNum, errNew := strconv.Atoi(newManifest.VersionId)

		if errCur == nil && errNew == nil {
			// Both are numeric - compare as integers
			if newNum > curNum {
				return true, nil
			}
			if newNum < curNum {
				if !auto {
					show := util.ConfirmYN(
						fmt.Sprintf("%s will be downgraded from %s to version %s, are you sure you want to downgrade?", newManifest.Name, currentManifest.VersionName, newManifest.VersionName),
						false,
						pterm.Warning.MessageStyle,
					)
					if !show {
						pterm.Info.Println("Cancelling update...")
						os.Exit(0)
					}
				}
				if auto && !force {
					pterm.Warning.Printfln("Cancelling update... %s would be downgraded from %s to %s. To force this downgrade use the -force flag", newManifest.Name, currentManifest.VersionName, newManifest.VersionName)
					os.Exit(1)
				} else if auto && force {
					pterm.Warning.Printfln("Forcing downgrade")
				}
				return true, nil
			}
		} else {
			// Non-numeric IDs (e.g., Modrinth) - treat any version change as an update
			return true, nil
		}
	} else {
		return false, nil
	}

	return currentManifest.VersionId != newManifest.VersionId, nil
}

// fileListSummary builds a formatted string listing files under a label.
// In verbose mode all files are shown; otherwise the list is capped at 10
// with a "... and N more" suffix. For updated files, if UpdatedName is set
// the entry is shown as "old → new" to make renames visible.
func fileListSummary(label string, files []structs.File) string {
	if len(files) == 0 {
		return ""
	}
	const cap = 10
	s := fmt.Sprintf("\n%s:", label)
	limit := len(files)
	if !verbose && limit > cap {
		limit = cap
	}
	for _, f := range files[:limit] {
		if f.UpdatedName != "" && f.UpdatedName != f.Name {
			s += fmt.Sprintf("\n  - %s → %s", f.Name, f.UpdatedName)
		} else {
			s += fmt.Sprintf("\n  - %s", f.Name)
		}
	}
	if !verbose && len(files) > cap {
		s += fmt.Sprintf("\n  ... and %d more", len(files)-cap)
	}
	return s
}

// computeUpdatedFiles compares two file lists and categorizes files into
// updated (hash changed or renamed), removed (no longer present), and unchanged.
// Matching priority: stable ID > hash > name+path > filename stem. This correctly
// handles mod version bumps where the filename changes between versions.
func computeUpdatedFiles(currentFiles, newFiles []structs.File) (updatedFiles, removedFiles, unchangedFiles []structs.File, err error) {
	// Build lookup indices over new files for O(1) access.
	newByID := make(map[string]structs.File, len(newFiles))
	newByHash := make(map[string]structs.File, len(newFiles))
	newByKey := make(map[string]structs.File, len(newFiles))
	newByStem := make(map[string]structs.File, len(newFiles))
	for _, f := range newFiles {
		if f.ID != "" {
			newByID[f.ID] = f
		}
		if f.Hash != "" {
			newByHash[f.Hash] = f
		}
		newByKey[f.Path+"/"+f.Name] = f
		if stem := modStem(f.Name); stem != "" {
			newByStem[f.Path+"/"+stem] = f
		}
	}

	for _, old := range currentFiles {
		var matched *structs.File

		// 1. Match by stable project ID (works for installs with new code)
		if old.ID != "" {
			if nf, ok := newByID[old.ID]; ok {
				matched = &nf
			}
		}
		// 2. Match by hash (unchanged file, possibly renamed)
		if matched == nil && old.Hash != "" {
			if nf, ok := newByHash[old.Hash]; ok {
				matched = &nf
			}
		}
		// 3. Exact name+path match
		if matched == nil {
			if nf, ok := newByKey[old.Path+"/"+old.Name]; ok {
				matched = &nf
			}
		}
		// 4. Stem match — handles version-bumped filenames in old manifests
		// that predate the ID field (e.g. sophisticatedbackpacks-1.18.2-3.18.37→3.18.40)
		if matched == nil {
			if stem := modStem(old.Name); stem != "" {
				if nf, ok := newByStem[old.Path+"/"+stem]; ok {
					matched = &nf
				}
			}
		}

		if matched == nil {
			removedFiles = append(removedFiles, old)
			continue
		}

		if matched.Hash != old.Hash {
			// File changed — store old file for deletion, set UpdatedName for display
			display := old
			display.UpdatedName = matched.Name
			updatedFiles = append(updatedFiles, display)
		} else {
			unchangedFiles = append(unchangedFiles, old)
		}
	}

	return
}

// modStem returns the stable name prefix of a mod jar filename by stripping the
// trailing version segment. It looks for the last '-' followed by a digit and
// removes everything from that point (including the .jar extension).
//
// Examples:
//
//	sophisticatedbackpacks-1.18.2-3.18.37.763.jar → sophisticatedbackpacks-1.18.2
//	EnchantmentDescriptions-Forge-1.18.2-10.0.10.jar → EnchantmentDescriptions-Forge-1.18.2
//	balm-3.2.1+0.jar → balm
func modStem(filename string) string {
	name := strings.TrimSuffix(filename, ".jar")
	// Walk backwards through '-' separated segments; drop trailing version segments
	// (those that start with a digit or contain only digits/dots/plus).
	for {
		idx := strings.LastIndex(name, "-")
		if idx < 0 {
			break
		}
		seg := name[idx+1:]
		if len(seg) > 0 && (seg[0] >= '0' && seg[0] <= '9') {
			name = name[:idx]
		} else {
			break
		}
	}
	return name
}

// removeUnchangedFiles filters out unchanged files from the download list
// to avoid redundant downloads during updates.
func removeUnchangedFiles(files []structs.File, unchangedFiles []structs.File) []structs.File {
	// removed unchanged files from files
	for _, f := range unchangedFiles {
		for i, v := range files {
			if v.Name == f.Name && v.Path == f.Path {
				files = append(files[:i], files[i+1:]...)
			}
		}
	}
	return files
}

// getLatestRelease finds the latest version from the version list. If the
// latest flag is false, only stable releases are considered; if true, the
// first version (alpha/beta/release) is returned.
func getLatestRelease(versions []structs.ModpackV, latest bool) (structs.ModpackV, error) {
	pterm.Debug.Printfln("versions: %+v", versions)
	for _, v := range versions {
		if !latest {
			if v.Type == "release" {
				return v, nil
			}
		} else {
			return v, nil
		}
	}
	if !latest {
		return structs.ModpackV{}, errors.New("no stable release found, please rerun the installer with the -latest flag or specify a version using the -version flag")
	}
	return structs.ModpackV{}, errors.New("no release found, please rerun the installer with the -version flag")
}

// modpackQuestion interactively prompts the user for the modpack and version IDs.
func modpackQuestion() (int, int, error) {
	sPId, _ := pterm.DefaultInteractiveTextInput.
		WithDefaultText("Please enter the modpack ID").
		Show()

	pId, err := strconv.Atoi(sPId)
	if err != nil {
		return 0, 0, err
	}

	getLatest := util.ConfirmYN("Would you like to get the latest version?", true, pterm.Info.MessageStyle)

	vId := 0
	if !getLatest {
		sVId, _ := pterm.DefaultInteractiveTextInput.
			WithDefaultText("Please enter the version id").
			Show()

		vId, err = strconv.Atoi(sVId)
		if err != nil {
			return 0, 0, err
		}
	}

	return pId, vId, nil
}
