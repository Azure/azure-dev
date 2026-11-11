// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package fireworks

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// DownloadResult describes a completed local model download, without source URLs.
type DownloadResult struct {
	Directory string `json:"directory" table:"Directory"`
	Files     int    `json:"files" table:"Files"`
	Bytes     int64  `json:"bytes" table:"Bytes"`
}

// ValidateModel rejects resource paths and empty model IDs before authentication.
func ValidateModel(model string) error {
	if !resourceIDPattern.MatchString(model) {
		return fmt.Errorf("--model must be a Fireworks model ID, not a model resource path")
	}
	return nil
}

// ValidateDownloadDirectory requires a new directory to avoid overwriting user files.
func ValidateDownloadDirectory(directory string) error {
	if strings.TrimSpace(directory) == "" {
		return fmt.Errorf("--output-dir must be non-empty")
	}
	if _, err := os.Lstat(directory); err == nil {
		return fmt.Errorf("--output-dir %q already exists; choose a new directory", directory)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check --output-dir: %w", err)
	}
	parent, err := os.Stat(filepath.Dir(filepath.Clean(directory)))
	if err != nil {
		return fmt.Errorf("check --output-dir parent (it must already exist): %w", err)
	}
	if !parent.IsDir() {
		return fmt.Errorf("--output-dir parent must be an existing directory")
	}
	return nil
}

// GetDownloadEndpoints requests the provider's signed file URLs without bypassing export restrictions.
func (c *Client) GetDownloadEndpoints(ctx context.Context, account, model string) (map[string]string, error) {
	if err := ValidateAccount(account); err != nil {
		return nil, err
	}
	if err := ValidateModel(model); err != nil {
		return nil, err
	}
	var response struct {
		FilenameToSignedURLs map[string]string `json:"filenameToSignedUrls"`
	}
	if err := c.get(ctx, "/v1/accounts/"+account+"/models/"+model+":getDownloadEndpoint", nil, &response); err != nil {
		return nil, err
	}
	if len(response.FilenameToSignedURLs) == 0 {
		return nil, fmt.Errorf("Fireworks returned no downloadable files for this model")
	}
	return response.FilenameToSignedURLs, nil
}

// DownloadModel streams every exported file to a new local directory.
func (c *Client) DownloadModel(ctx context.Context, account, model, directory string) (*DownloadResult, error) {
	if err := ValidateDownloadDirectory(directory); err != nil {
		return nil, err
	}
	files, err := c.GetDownloadEndpoints(ctx, account, model)
	if err != nil {
		return nil, err
	}
	// Storage requests must not share provider authentication or a whole-file timeout.
	return downloadFiles(ctx, files, directory, &http.Client{})
}

func validateDownloadFiles(files map[string]string) error {
	if len(files) == 0 {
		return fmt.Errorf("Fireworks returned no downloadable files for this model")
	}
	for name, source := range files {
		if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "\\:\x00") {
			return fmt.Errorf("Fireworks returned an unsafe download filename %q", name)
		}
		if _, err := filepath.Localize(name); err != nil {
			return fmt.Errorf("Fireworks returned an unsupported download filename %q", name)
		}
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if _, exists := files[parent]; exists {
				return fmt.Errorf("Fireworks returned conflicting download filenames %q and %q", parent, name)
			}
		}
		parsed, err := url.Parse(source)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" ||
			parsed.User != nil || parsed.Fragment != "" {
			return fmt.Errorf("Fireworks returned an invalid download URL for %q; direct HTTPS URLs are required", name)
		}
	}
	return nil
}

func downloadFiles(
	ctx context.Context, files map[string]string, directory string, httpClient *http.Client,
) (*DownloadResult, error) {
	if err := validateDownloadFiles(files); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve download directory: %w", err)
	}
	if err := os.Mkdir(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create download directory (its parent must exist): %w", err)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, fmt.Errorf("open download directory: %w", err)
	}
	defer root.Close()

	transferClient := *httpClient
	transferClient.Jar = nil
	transferClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	result := &DownloadResult{Directory: absolute}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		n, err := downloadFile(ctx, &transferClient, root, name, files[name])
		if err != nil {
			return nil, fmt.Errorf(
				"%w; %d completed file(s) remain in %q; retry with a new --output-dir", err, result.Files, absolute)
		}
		result.Files++
		result.Bytes += n
	}
	return result, nil
}

func downloadFile(
	ctx context.Context, httpClient *http.Client, root *os.Root, name, source string,
) (written int64, resultErr error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return 0, fmt.Errorf("create download request for %q", name)
	}
	req.Header.Set("Accept-Encoding", "identity")
	//nolint:gosec // Export URLs are validated as HTTPS; no provider credentials or redirects are used.
	resp, err := httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, fmt.Errorf("download %q: %w", name, ctx.Err())
		}
		// Transport errors can contain signed URLs; do not include them in output.
		return 0, fmt.Errorf("download %q: source request failed; check connectivity and retry", name)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download %q: HTTP %d", name, resp.StatusCode)
	}
	localName, err := filepath.Localize(name)
	if err != nil {
		return 0, fmt.Errorf("resolve download filename %q: %w", name, err)
	}
	if err := root.MkdirAll(filepath.Dir(localName), 0o700); err != nil {
		return 0, fmt.Errorf("create download subdirectory: %w", err)
	}
	tempName := filepath.Join(filepath.Dir(localName), ".azd-download-"+rand.Text()+".partial")
	file, err := root.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create temporary download file: %w", err)
	}
	defer func() {
		if err := root.Remove(tempName); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove incomplete download file: %w", err))
		}
	}()
	written, copyErr := io.Copy(file, resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		if ctx.Err() != nil {
			return 0, fmt.Errorf("download %q: %w", name, errors.Join(ctx.Err(), closeErr))
		}
		// Do not echo storage errors or response bodies that could disclose signed URLs.
		return 0, errors.Join(fmt.Errorf("download %q: file transfer failed", name), closeErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("close download file %q: %w", name, closeErr)
	}
	if _, err := root.Lstat(localName); err == nil {
		return 0, fmt.Errorf("download filename %q conflicts with an existing file", name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("check download filename %q: %w", name, err)
	}
	if err := root.Rename(tempName, localName); err != nil {
		return 0, fmt.Errorf("complete download file %q: %w", name, err)
	}
	return written, nil
}
