package util

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/pterm/pterm"
)

// Download manages the download of a single file from a URL to a local path.
// It supports optional checksum verification, content-length validation, and
// cancellation via a context.
type Download struct {
	destPath           string
	reqURL             string
	headers            map[string]string
	hash               hash.Hash
	checksum           []byte
	deleteOnError      bool
	checkContentLength bool
	timeout            time.Duration
	CancelFunc         context.CancelFunc
}

// NewDownload creates a new Download instance for the given destination path and URL.
// Returns an error if the URL is empty.
func NewDownload(destPath string, reqUrl string) (*Download, error) {
	if reqUrl == "" {
		return nil, fmt.Errorf("required URL is empty")
	}
	return &Download{
		reqURL:             reqUrl,
		destPath:           destPath,
		checkContentLength: false,
		timeout:            30 * time.Minute,
	}, nil
}

// Do perform the file download with the configured parameters.
// It handles directory creation, checksum verification, and cleanup on error if configured.
// Returns an error if the download or verification fails.
func (dl *Download) Do() error {
	ctx, cancel := context.WithTimeout(context.Background(), dl.timeout)
	dl.CancelFunc = cancel
	defer dl.Cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", dl.reqURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", UserAgent)
	for k, v := range dl.headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	//if resp.Header.Get("Cf-Cache-Status") != "HIT" && resp.Header.Get("Cf-Cache-Status") != "" {
	//	pterm.Debug.Printfln("Cf-Cache-Status for %s: %s", dl.reqURL, resp.Header.Get("Cf-Cache-Status"))
	//}
	if resp.StatusCode != http.StatusOK {
		pterm.Debug.Printfln("Headers: %+v", resp.Header)
		return fmt.Errorf("failed to download file from %s: bad status %s", dl.reqURL, resp.Status)
	}
	if dl.checkContentLength && resp.ContentLength < 1 {
		pterm.Debug.Printfln("Headers: %+v", resp.Header)
		return fmt.Errorf("invalid content length: %d", resp.ContentLength)
	}

	b := resp.Body
	err = dl.write(b)
	if err != nil {
		return err
	}

	return nil
}

func (dl *Download) write(b io.ReadCloser) error {
	// Check if the destination directory exists
	destDir := filepath.Dir(dl.destPath)
	if _, err := os.Stat(destDir); errors.Is(err, os.ErrNotExist) {
		// Create the destination directory if it doesn't exist
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %s", err.Error())
		}
	}

	f, err := os.OpenFile(dl.destPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	var writer io.Writer = f

	if dl.hash != nil {
		writer = io.MultiWriter(f, dl.hash)
	}

	if _, err = io.Copy(writer, b); err != nil {
		return fmt.Errorf("failed to write file: %s", err.Error())
	}

	if dl.hash != nil && dl.checksum != nil {
		sum := dl.hash.Sum(nil)
		if !bytes.Equal(dl.checksum, sum) {
			if dl.deleteOnError {
				if err := os.Remove(dl.destPath); err != nil {
					return fmt.Errorf("checksum mismatch, failed to remove file: %s", err.Error())
				}
			}
			return fmt.Errorf("checksum mismatch")
		}
	}
	return nil
}

// SetTimeout configures the download timeout. If not called, the default
// timeout of 30 minutes is used.
func (dl *Download) SetTimeout(timeout time.Duration) {
	dl.timeout = timeout
}

// SetHeaders sets additional HTTP request headers to send with the download
// request (e.g. authentication headers for provider-specific mirrors).
func (dl *Download) SetHeaders(headers map[string]string) {
	dl.headers = headers
}

// CheckContentLength enables or disables validation that the HTTP response has
// a positive Content-Length header. When enabled, downloads with missing or zero
// Content-Length will fail.
func (dl *Download) CheckContentLength(check bool) {
	dl.checkContentLength = check
}

// SetChecksum configures checksum verification for the download. After the file
// is written, its hash is compared against sum. If deleteOnError is true and the
// checksums don't match, the downloaded file is removed.
func (dl *Download) SetChecksum(hash hash.Hash, sum []byte, deleteOnError bool) {
	dl.hash = hash
	dl.checksum = sum
	dl.deleteOnError = deleteOnError
}

// Cancel cancels the download's context, aborting any in-progress HTTP request.
func (dl *Download) Cancel() {
	if dl.CancelFunc != nil {
		dl.CancelFunc()
	}
}
