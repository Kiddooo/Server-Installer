// Package util provides HTTP helpers, file utilities, Java management, and
// other shared functionality used across the Server Installer.
package util

import (
	"archive/zip"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"ftb-server-downloader/structs"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	semVer "github.com/hashicorp/go-version"
	"github.com/pterm/pterm"
)

const (
	// ManifestName is the filename used for the installation manifest.
	ManifestName   = ".manifest.json"
	adoptiumApiUrl = "https://api.adoptium.net"
)

var (
	// ReleaseVersion is the semantic version of the installer, set at build time via ldflags.
	ReleaseVersion string
	// GitCommit is the git commit hash of the build, set at build time via ldflags.
	GitCommit string
	// ApiKey is the API key used for authenticated FTB API requests.
	ApiKey string
	// UserAgent is the User-Agent header value sent with all HTTP requests.
	UserAgent string
	// LogMw is the multi-writer used for logging to both stdout and the log file.
	LogMw io.Writer
	// BackoffTimes defines the sleep durations between download retry attempts.
	BackoffTimes = []time.Duration{
		1 * time.Second,
		3 * time.Second,
		10 * time.Second,
	}
)

// ParseInstallerName extracts pack and version IDs from the installer executable
// filename. Expected format: "prefix_<packId>_<versionId>" (version is optional).
func ParseInstallerName(filename string) (int, int, error) {
	re := regexp.MustCompile(`^.*?_(\d+)(?:_(\d+))?`)
	matches := re.FindStringSubmatch(filename)
	if len(matches) < 3 {
		return 0, 0, errors.New("no pack/version id in installer name")
	}
	pId, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, 0, err
	}
	vId := 0
	if matches[2] != "" {
		vId, err = strconv.Atoi(matches[2])
		if err != nil {
			return 0, 0, err
		}
	}

	return pId, vId, nil
}

func makeRequest(method, url string, requestHeaders map[string][]string) (*http.Response, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}
	// Use Header.Set to ensure proper canonical key formatting (e.g. "x-api-key" → "X-Api-Key")
	for k, vals := range requestHeaders {
		for _, v := range vals {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("User-Agent", UserAgent)
	if ApiKey != "public" && strings.Contains(url, "api.feed-the-beast.com") {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ApiKey))
	}

	return http.DefaultClient.Do(req)
}

// DoGet performs an HTTP GET request with default headers (User-Agent, FTB auth).
func DoGet(url string) (*http.Response, error) {
	headers := map[string][]string{}
	resp, err := makeRequest("GET", url, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Error: %d\n%s", resp.StatusCode, b)
	}
	return resp, nil
}

// DoGetWithHeaders performs an HTTP GET request with custom headers merged on top
// of the default headers (User-Agent). This is useful for provider-specific
// authentication (e.g., CurseForge API key, Modrinth auth tokens).
func DoGetWithHeaders(url string, extraHeaders map[string]string) (*http.Response, error) {
	headers := map[string][]string{}
	for k, v := range extraHeaders {
		headers[k] = []string{v}
	}
	resp, err := makeRequest("GET", url, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Error: %d\n%s", resp.StatusCode, b)
	}
	return resp, nil
}

// DoHead performs an HTTP HEAD request with default headers (User-Agent, FTB auth).
func DoHead(url string) (*http.Response, error) {
	headers := map[string][]string{}
	resp, err := makeRequest("HEAD", url, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Error: %d\n%s", resp.StatusCode, b)
	}
	return resp, nil
}

// IsEmptyDir checks whether a directory is empty, ignoring installer-related
// files (the installer binary itself, log files, install scripts, and README).
func IsEmptyDir(path string) (bool, error) {
	dir, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	count := len(dir)
	pterm.Debug.Printfln("Is %s is empty: %t", path, count == 0)

	// We want to check if the dir is empty or only contains the installer file
	if count == 0 {
		return true, nil
	}

	hasNonInstallerFiles := false
	installerName := filepath.Base(os.Args[0])
	for _, f := range dir {
		if !f.IsDir() && (f.Name() == installerName || f.Name() == "server-installer.log" || f.Name() == "install.bat" || f.Name() == "install.sh" || f.Name() == "README.md") {
			continue
		}
		hasNonInstallerFiles = true
	}
	return !hasNonInstallerFiles, nil
}

// IsEmptyDirRecursive checks whether a directory and all its subdirectories
// contain no files. Currently unused but retained for potential future use.
//
//goland:noinspection GoUnusedExportedFunction
func IsEmptyDirRecursive(path string) (bool, error) {
	dir, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}

	for _, f := range dir {
		path := filepath.Join(path, f.Name())
		if f.IsDir() {
			empty, err := IsEmptyDirRecursive(path)
			if err != nil {
				return false, err
			}

			if !empty {
				return false, nil
			}
		} else {
			return false, nil
		}
	}
	return true, nil
}

// ReadManifest reads and parses the .manifest.json file from the install directory.
func ReadManifest(installDir string) (structs.Manifest, error) {
	pterm.Debug.Println("Reading manifest from", installDir)
	file, err := os.ReadFile(filepath.Join(installDir, ManifestName))
	if err != nil {
		return structs.Manifest{}, err
	}

	var manifest structs.Manifest
	err = json.Unmarshal(file, &manifest)
	if err != nil {
		return structs.Manifest{}, err
	}
	return manifest, nil
}

// WriteManifest writes the version manifest to the install directory as .manifest.json.
func WriteManifest(installDir string, manifest structs.Manifest) error {
	manifestJson, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("unable to marshal manifest: %s", err.Error())
	}
	versionFile := filepath.Join(installDir, ManifestName)
	vFile, err := os.Create(versionFile)
	if err != nil {
		return fmt.Errorf("unable to create manifest: %s", err.Error())
	}
	defer vFile.Close()
	_, err = vFile.Write(manifestJson)
	if err != nil {
		return fmt.Errorf("unable to write manifest: %s", err.Error())
	}
	return nil
}

// PathExists checks whether a file or directory exists at the given path.
func PathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// OsJavaExists checks whether the java executable is available on the system PATH.
func OsJavaExists() bool {
	path, err := exec.LookPath("java")
	pterm.Debug.Printfln("Looking for java in %s", path)
	if err != nil {
		return false
	}
	return true
}

// GetJava queries the Adoptium API for the latest release of the specified Java
// major version and returns a File struct with the download URL and checksum.
func GetJava(version string) (structs.File, error) {
	adoptiumUrl, err := makeAdoptiumUrl(version)
	if err != nil {
		return structs.File{}, err
	}

	get, err := DoGet(adoptiumUrl)
	if err != nil {
		return structs.File{}, err
	}
	defer get.Body.Close()

	var adoptium structs.Adoptium

	err = json.NewDecoder(get.Body).Decode(&adoptium)
	if err != nil {
		return structs.File{}, err
	}

	if len(adoptium) == 0 {
		return structs.File{}, fmt.Errorf("no JRE found for Java %s on this platform", version)
	}

	pkg := adoptium[0].Binary.Package

	var fileExt string
	if strings.HasSuffix(pkg.Name, ".zip") {
		fileExt = ".zip"
	} else if strings.HasSuffix(pkg.Name, ".tar.gz") {
		fileExt = ".tar.gz"
	} else {
		fileExt = ""
	}

	return structs.File{
		Name:     "jre" + fileExt,
		Path:     "",
		Url:      pkg.Link,
		Hash:     pkg.Checksum,
		HashType: "sha256",
	}, nil
}

// GetJavaPath returns the platform-specific relative path to the java binary
// within the bundled JRE directory for the given version.
func GetJavaPath(version string) (string, error) {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join("jre", version, "bin", "java.exe"), nil
	case "darwin":
		return filepath.Join("jre", version, "Contents", "Home", "bin", "java"), nil
	case "linux":
		return filepath.Join("jre", version, "bin", "java"), nil
	default:
		return "", errors.New("unsupported platform")
	}
}

// makeAdoptiumUrl builds a URL for the Adoptium API to query the latest JRE
// release for the given Java major version, targeting the current OS and architecture.
// Uses the /v3/assets/latest/{version}/hotspot endpoint which accepts bare major
// version numbers (e.g. "21") unlike /v3/assets/version which requires full semver.
func makeAdoptiumUrl(version string) (string, error) {
	parsedUrl, err := url.Parse(adoptiumApiUrl + "/v3/assets/latest/" + version + "/hotspot")
	if err != nil {
		return "", err
	}

	q := parsedUrl.Query()
	q.Add("image_type", "jre")
	q.Add("vendor", "eclipse")

	if runtime.GOOS == "windows" {
		q.Add("os", "windows")
	} else if runtime.GOOS == "darwin" {
		q.Add("os", "mac")
	} else if runtime.GOOS == "linux" {
		if _, err := os.Stat("/etc/alpine-release"); !os.IsNotExist(err) {
			q.Add("os", "alpine-linux")
		} else {
			q.Add("os", "linux")
		}
	}

	arch, err := validJavaArch(version)
	if err != nil {
		return "", err
	}
	q.Add("architecture", arch)

	parsedUrl.RawQuery = q.Encode()

	return parsedUrl.String(), nil
}

// validJavaArch returns the Adoptium architecture string for the current platform
// and the given Java version. On macOS arm64, versions below 11 fall back to x64
// since native arm64 JREs were not available before Java 11.
func validJavaArch(version string) (string, error) {
	targetVersion, err := semVer.NewVersion(version)
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			limit, err := semVer.NewVersion("11.0.0")
			if err != nil {
				return "", err
			}
			if targetVersion.LessThan(limit) {
				return "x64", nil
			}
			return "aarch64", nil
		}
		if runtime.GOARCH == "amd64" {
			return "x64", nil
		}
		if runtime.GOARCH == "386" {
			return "x86", nil
		}
	case "windows":
		if runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" {
			return "x64", nil
		}
		if runtime.GOARCH == "386" || runtime.GOARCH == "arm" {
			return "x86", nil
		}
	case "linux":
		if runtime.GOARCH == "amd64" {
			return "x64", nil
		}
		if runtime.GOARCH == "386" {
			return "x86", nil
		}
		if runtime.GOARCH == "arm64" {
			return "aarch64", nil
		}
		if runtime.GOARCH == "arm" {
			return "arm", nil
		}
	}
	return "", errors.New("unsupported architecture, please open an issue at https://github.com/Kiddooo/Server-Installer/issues")
}

// FileHash computes the hex-encoded hash of a file using the specified algorithm
// ("sha1" or "sha256").
func FileHash(path string, hash string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	switch hash {
	case "sha1":
		h := sha1.New()
		if _, err = io.Copy(h, f); err != nil {
			return "", err
		}
		return fmt.Sprintf("%x", h.Sum(nil)), nil
	case "sha256":
		h := sha256.New()
		if _, err = io.Copy(h, f); err != nil {
			return "", err
		}
		return fmt.Sprintf("%x", h.Sum(nil)), nil
	default:
		return "", errors.New("unsupported hash type")
	}
}

// CombineZip merges two ZIP files into a single ZIP at destZip. Used by old
// Forge versions that distribute as a universal.zip that must be combined
// with the vanilla server jar.
func CombineZip(inZip string, destZip string) error {
	_ = os.Rename(destZip, destZip+".tmp")
	defer os.Remove(destZip + ".tmp")

	newZipFile, err := os.Create(destZip)
	if err != nil {
		return err
	}
	defer newZipFile.Close()

	writer := zip.NewWriter(newZipFile)
	defer writer.Close()

	zips := []string{destZip + ".tmp", inZip}

	for _, filename := range zips {
		zipReader, err := zip.OpenReader(filename)
		if err != nil {
			return err
		}

		for _, file := range zipReader.File {
			zipFileReader, err := file.Open()
			if err != nil {
				_ = zipReader.Close()
				return err
			}

			header, err := zip.FileInfoHeader(file.FileInfo())
			if err != nil {
				_ = zipFileReader.Close()
				_ = zipReader.Close()
				return err
			}
			header.Name = file.Name

			zipWriter, err := writer.CreateHeader(header)
			if err != nil {
				_ = zipFileReader.Close()
				_ = zipReader.Close()
				return err
			}

			_, err = io.Copy(zipWriter, zipFileReader)
			if err != nil {
				_ = zipFileReader.Close()
				_ = zipReader.Close()
				return err
			}
			_ = zipFileReader.Close()
		}
		_ = zipReader.Close()
	}
	return nil
}

// ConfirmYN displays an interactive yes/no prompt with the given text and default value.
func ConfirmYN(text string, value bool, style *pterm.Style) bool {
	if style == nil {
		style = pterm.Info.MessageStyle
	}
	show, err := pterm.DefaultInteractiveConfirm.
		WithDefaultText(text).
		WithDefaultValue(value).
		WithTextStyle(style).
		Show()
	if err != nil {
		pterm.Fatal.Printfln("Interactive confirm error: %s", err.Error())
	}
	return show
}

// CopyDir recursively copies the contents of src into dst.
//
//goland:noinspection GoUnusedExportedFunction
func CopyDir(src string, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)

		if d.IsDir() {
			if _, err := os.Stat(dstPath); os.IsNotExist(err) {
				if err := os.Mkdir(dstPath, d.Type().Perm()); err != nil {
					return err
				}
			}
		} else {
			if err := CopyFile(path, dstPath); err != nil {
				return err
			}
		}
		return nil
	})
}

// CopyFile copies a single file from src to dst.
func CopyFile(src string, dst string) error {
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer file.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, file); err != nil {
		return err
	}

	return nil
}

// CustomWriter is an io.Writer that strips ANSI escape sequences from output,
// used to produce clean log file output without terminal formatting codes.
type CustomWriter struct {
	writer io.Writer
}

// NewCustomWriter creates a new CustomWriter.
func NewCustomWriter(writer io.Writer) *CustomWriter {
	return &CustomWriter{writer: writer}
}

// Write implements the io.Writer interface.
func (cw *CustomWriter) Write(p []byte) (n int, err error) {

	re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	stripped := re.ReplaceAll(p, []byte{})

	filtered := make([]byte, 0, len(stripped))
	for _, b := range stripped {
		if b == '\n' || (unicode.IsPrint(rune(b)) || b < 0x20 || b > 0x7E) {
			filtered = append(filtered, b)
		}
	}
	return cw.writer.Write(filtered)
}

// FailedDownloadHandler handles download retry and mirror failover logic.
// Returns (shouldRetry, tryNextMirror, error). When shouldRetry is true, the
// caller should retry the same mirror. When tryNextMirror is true, the caller
// should move to the next mirror. When error is non-nil, all retries are exhausted.
func FailedDownloadHandler(attempts, m int, file structs.File, mirror string, mirrors []string) (bool, bool, error) {
	if attempts < 2 {
		sleepTime := BackoffTimes[attempts]
		pterm.Warning.Printfln("Failed to download file %s from %s, retrying in %s", file.Name, mirror, sleepTime.String())
		time.Sleep(sleepTime)
		return true, false, nil
	} else if attempts >= 2 && m < len(mirrors)-1 { // TODO: Validate this
		pterm.Warning.Printfln("Failed to download file %s from %s, trying next mirror", file.Name, mirror)
		return false, true, nil
	} else if attempts >= 2 && m == len(mirrors)-1 { // TODO: Validate this
		return false, false, fmt.Errorf("failed to download file %s from %s, all attempts and mirrors failed", file.Name, mirror)
	}
	return false, false, fmt.Errorf("something went wrong, please open an issue at https://github.com/Kiddooo/Server-Installer/issues")
}

// RelaunchInTerminal attempts to relaunch the installer in a graphical terminal
// emulator on Linux when the process is not already running in a terminal.
func RelaunchInTerminal() {
	executable, err := os.Executable()

	if err != nil {
		fmt.Printf("Failed to get executable path: %s\n", err.Error())
		return
	}

	terminals := [][]string{
		{"gnome-terminal", "--", "bash", "-c", executable + "; read -p 'Press Enter to close...'"},
		{"konsole", "--hold", "-e", executable},
		{"xfce4-terminal", "--hold", "-e", executable},
		{"mate-terminal", "-e", executable},
		{"xterm", "-hold", "-e", executable},
	}

	for _, termCmd := range terminals {
		cmd := exec.Command(termCmd[0], termCmd[1:]...)
		if err := cmd.Start(); err == nil {
			return
		}
	}
}
