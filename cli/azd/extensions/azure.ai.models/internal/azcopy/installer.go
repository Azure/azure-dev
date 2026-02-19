// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcopy

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// maxDownloadSize is the maximum allowed download size (500 MB).
	maxDownloadSize int64 = 500 * 1024 * 1024
)

// allowedHosts lists hosts permitted during download and redirects.
var allowedHosts = []string{
	".blob.core.windows.net",
	".microsoft.com",
	".azure.com",
	".azureedge.net",
	".github.com",
	"github.com",
}

// isAllowedHost checks if the URL host is in the allowed hosts list.
func isAllowedHost(u *url.URL) bool {
	if !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, suffix := range allowedHosts {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// downloadURLs maps GOOS/GOARCH to the stable aka.ms download URLs.
var downloadURLs = map[string]string{
	"windows/amd64": "https://aka.ms/downloadazcopy-v10-windows",
	"linux/amd64":   "https://aka.ms/downloadazcopy-v10-linux",
	"linux/arm64":   "https://aka.ms/downloadazcopy-v10-linux-arm64",
	"darwin/amd64":  "https://aka.ms/downloadazcopy-v10-mac",
	"darwin/arm64":  "https://aka.ms/downloadazcopy-v10-mac-arm64",
}

// installDir returns ~/.azd/bin
func installDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".azd", "bin"), nil
}

// downloadAzCopy downloads and installs azcopy to ~/.azd/bin/.
// Returns the path to the installed binary.
func downloadAzCopy(ctx context.Context) (string, error) {
	key := runtime.GOOS + "/" + runtime.GOARCH
	downloadURL, ok := downloadURLs[key]
	if !ok {
		return "", fmt.Errorf("no azcopy download available for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	destDir, err := installDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", destDir, err)
	}

	binaryName := "azcopy"
	if runtime.GOOS == "windows" {
		binaryName = "azcopy.exe"
	}
	destPath := filepath.Join(destDir, binaryName)

	// Download archive to temp file
	httpClient := &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			if !isAllowedHost(req.URL) {
				return fmt.Errorf("redirect to disallowed host: %s", req.URL.Hostname())
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download azcopy: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download azcopy: HTTP %d", resp.StatusCode)
	}

	// Validate the final URL after all redirects
	if !isAllowedHost(resp.Request.URL) {
		return "", fmt.Errorf("download resolved to disallowed host: %s", resp.Request.URL.Hostname())
	}

	// Validate Content-Length if provided
	if resp.ContentLength > maxDownloadSize {
		return "", fmt.Errorf("download too large: %d bytes exceeds limit of %d bytes", resp.ContentLength, maxDownloadSize)
	}

	tmpFile, err := os.CreateTemp("", "azcopy-download-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	// Enforce size limit during copy regardless of Content-Length header
	limitedReader := io.LimitReader(resp.Body, maxDownloadSize+1)
	written, err := io.Copy(tmpFile, limitedReader)
	if err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("failed to save download: %w", err)
	}
	tmpFile.Close()

	if written > maxDownloadSize {
		return "", fmt.Errorf("download too large: exceeded limit of %d bytes", maxDownloadSize)
	}

	// Extract the azcopy binary from the archive
	if runtime.GOOS == "linux" {
		err = extractFromTarGz(tmpPath, binaryName, destPath)
	} else {
		err = extractFromZip(tmpPath, binaryName, destPath)
	}
	if err != nil {
		return "", err
	}

	// Set executable permission on Unix
	if runtime.GOOS != "windows" {
		if err := os.Chmod(destPath, 0755); err != nil {
			return "", fmt.Errorf("failed to set executable permission: %w", err)
		}
	}

	return destPath, nil
}

// extractFromZip finds the azcopy binary inside a zip archive and extracts it to destPath.
func extractFromZip(archivePath, binaryName, destPath string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open zip archive: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		// The archive contains e.g. azcopy_windows_amd64_10.32.0/azcopy.exe
		if strings.HasSuffix(f.Name, "/"+binaryName) || f.Name == binaryName {
			src, err := f.Open()
			if err != nil {
				return fmt.Errorf("failed to read %s from archive: %w", f.Name, err)
			}
			defer src.Close()

			dst, err := os.Create(destPath)
			if err != nil {
				return fmt.Errorf("failed to create %s: %w", destPath, err)
			}
			defer dst.Close()

			if _, err := io.Copy(dst, src); err != nil {
				return fmt.Errorf("failed to extract %s: %w", binaryName, err)
			}
			return nil
		}
	}

	return fmt.Errorf("%s not found in zip archive", binaryName)
}

// extractFromTarGz finds the azcopy binary inside a tar.gz archive and extracts it to destPath.
func extractFromTarGz(archivePath, binaryName, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("failed to open gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar entry: %w", err)
		}

		// The archive contains e.g. azcopy_linux_amd64_10.32.0/azcopy
		name := filepath.Base(hdr.Name)
		if name == binaryName && hdr.Typeflag == tar.TypeReg {
			dst, err := os.Create(destPath)
			if err != nil {
				return fmt.Errorf("failed to create %s: %w", destPath, err)
			}
			defer dst.Close()

			if _, err := io.Copy(dst, tr); err != nil {
				return fmt.Errorf("failed to extract %s: %w", binaryName, err)
			}
			return nil
		}
	}

	return fmt.Errorf("%s not found in tar.gz archive", binaryName)
}
