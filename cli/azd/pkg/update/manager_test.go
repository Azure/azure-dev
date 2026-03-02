// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/installer"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

func TestParseDailyBuildNumber(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    int
		wantErr bool
	}{
		{"standard daily", "1.24.0-beta.1-daily.5935787", 5935787, false},
		{"simple daily", "1.0.0-daily.100", 100, false},
		{"large build number", "2.0.0-beta.2-daily.9999999", 9999999, false},
		{"with commit suffix",
			"1.4.9-beta.1-daily.5000000 (commit aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa)", 5000000, false},
		{"no daily suffix", "1.23.6", 0, true},
		{"dev version", "0.0.0-dev.0", 0, true},
		{"empty string", "", 0, true},
		{"daily but no number", "1.0.0-daily.", 0, true},
		{"daily with non-numeric", "1.0.0-daily.abc", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDailyBuildNumber(tt.version)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestBuildDownloadURL(t *testing.T) {
	m := NewManager(nil)

	tests := []struct {
		name    string
		channel Channel
		wantErr bool
		// We check that the URL contains these substrings
		contains []string
	}{
		{
			name:    "stable",
			channel: ChannelStable,
			contains: []string{
				blobBaseURL + "/stable/",
				fmt.Sprintf("azd-%s-%s", runtime.GOOS, runtime.GOARCH),
			},
		},
		{
			name:    "daily",
			channel: ChannelDaily,
			contains: []string{
				blobBaseURL + "/daily/",
				fmt.Sprintf("azd-%s-%s", runtime.GOOS, runtime.GOARCH),
			},
		},
		{
			name:    "invalid channel",
			channel: Channel("nightly"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.buildDownloadURL(tt.channel)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			for _, s := range tt.contains {
				require.Contains(t, got, s)
			}
		})
	}
}

func TestArchiveExtension(t *testing.T) {
	ext := archiveExtension()
	if runtime.GOOS == "linux" {
		require.Equal(t, ".tar.gz", ext)
	} else {
		require.Equal(t, ".zip", ext)
	}
}

func TestPackageManagerUninstallCmd(t *testing.T) {
	tests := []struct {
		name        string
		installedBy installer.InstallType
		want        string
	}{
		{"brew", installer.InstallTypeBrew, "brew uninstall azd"},
		{"winget", installer.InstallTypeWinget, "winget uninstall Microsoft.Azd"},
		{"choco", installer.InstallTypeChoco, "choco uninstall azd"},
		{"unknown", installer.InstallTypeUnknown, "your package manager"},
		{"script", installer.InstallTypeSh, "your package manager"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, PackageManagerUninstallCmd(tt.installedBy))
		})
	}
}

func TestBuildVersionInfoFromCache_Stable(t *testing.T) {
	m := NewManager(nil)

	tests := []struct {
		name      string
		version   string
		hasUpdate bool
	}{
		// Dev build (0.0.0-dev.0) is always less than any release
		{"newer version available", "999.0.0", true},
		// In semver, 0.0.0 > 0.0.0-dev.0 (pre-release has lower precedence)
		// so even 0.0.0 is considered an update from a dev build
		{"release beats pre-release", "0.0.0", true},
		// A pre-release that equals the current version is not an update
		{"same pre-release version", "0.0.0-dev.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &CacheFile{
				Channel: "stable",
				Version: tt.version,
			}
			info, err := m.buildVersionInfoFromCache(cache, ChannelStable)
			require.NoError(t, err)
			require.Equal(t, tt.hasUpdate, info.HasUpdate)
			require.Equal(t, tt.version, info.Version)
			require.Equal(t, ChannelStable, info.Channel)
		})
	}
}

func TestBuildVersionInfoFromCache_Daily(t *testing.T) {
	m := NewManager(nil)

	// Dev build (0.0.0-dev.0) can't parse a daily build number,
	// so it always assumes update available
	cache := &CacheFile{
		Channel:     "daily",
		Version:     "1.24.0-beta.1-daily.5935787",
		BuildNumber: 5935787,
	}

	info, err := m.buildVersionInfoFromCache(cache, ChannelDaily)
	require.NoError(t, err)
	require.True(t, info.HasUpdate, "dev build should always see daily update available")
	require.Equal(t, ChannelDaily, info.Channel)
	require.Equal(t, 5935787, info.BuildNumber)
}

func TestBuildVersionInfoFromCache_InvalidVersion(t *testing.T) {
	m := NewManager(nil)
	cache := &CacheFile{
		Channel: "stable",
		Version: "not-a-version",
	}

	_, err := m.buildVersionInfoFromCache(cache, ChannelStable)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse")
}

func TestCheckForUpdate_StableHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "999.0.0")
	}))
	defer server.Close()

	// Override the default client transport to redirect requests to test server
	origTransport := http.DefaultTransport
	http.DefaultTransport = &urlRewriteTransport{
		base:      origTransport,
		targetURL: server.URL,
	}
	defer func() { http.DefaultTransport = origTransport }()

	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	m := NewManager(nil)
	cfg := &UpdateConfig{Channel: ChannelStable}

	info, err := m.CheckForUpdate(context.Background(), cfg, true)
	require.NoError(t, err)
	require.Equal(t, "999.0.0", info.Version)
	require.Equal(t, ChannelStable, info.Channel)
	require.True(t, info.HasUpdate)
}

func TestCheckForUpdate_DailyHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "1.24.0-beta.1-daily.9999999")
	}))
	defer server.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &urlRewriteTransport{
		base:      origTransport,
		targetURL: server.URL,
	}
	defer func() { http.DefaultTransport = origTransport }()

	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	m := NewManager(nil)
	cfg := &UpdateConfig{Channel: ChannelDaily}

	info, err := m.CheckForUpdate(context.Background(), cfg, true)
	require.NoError(t, err)
	require.Equal(t, "1.24.0-beta.1-daily.9999999", info.Version)
	require.Equal(t, 9999999, info.BuildNumber)
	require.Equal(t, ChannelDaily, info.Channel)
	require.True(t, info.HasUpdate)
}

func TestCheckForUpdate_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	origTransport := http.DefaultTransport
	http.DefaultTransport = &urlRewriteTransport{
		base:      origTransport,
		targetURL: server.URL,
	}
	defer func() { http.DefaultTransport = origTransport }()

	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	m := NewManager(nil)
	cfg := &UpdateConfig{Channel: ChannelStable}

	_, err := m.CheckForUpdate(context.Background(), cfg, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
}

func TestCheckForUpdate_UsesCache(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	// Pre-populate cache with a future expiry
	cache := &CacheFile{
		Channel:   "stable",
		Version:   "888.0.0",
		ExpiresOn: "2099-01-01T00:00:00Z",
	}
	require.NoError(t, SaveCache(cache))

	m := NewManager(nil)
	cfg := &UpdateConfig{Channel: ChannelStable}

	// ignoreCache=false should use the cache (no HTTP call needed)
	info, err := m.CheckForUpdate(context.Background(), cfg, false)
	require.NoError(t, err)
	require.Equal(t, "888.0.0", info.Version)
	require.True(t, info.HasUpdate)
}

func TestCheckForUpdate_InvalidChannel(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	m := NewManager(nil)
	cfg := &UpdateConfig{Channel: Channel("nightly")}

	_, err := m.CheckForUpdate(context.Background(), cfg, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported channel")
}

func TestUpdateViaPackageManager_Success(t *testing.T) {
	mockRunner := mockexec.NewMockCommandRunner()
	mockRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "brew upgrade azd")
	}).Respond(exec.NewRunResult(0, "Updated azd", ""))

	m := NewManager(mockRunner)
	var buf bytes.Buffer

	err := m.updateViaPackageManager(context.Background(), "brew", []string{"upgrade", "azd"}, &buf)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "Updating azd via brew")
}

func TestUpdateViaPackageManager_Failure(t *testing.T) {
	mockRunner := mockexec.NewMockCommandRunner()
	mockRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "brew upgrade azd")
	}).Respond(exec.NewRunResult(1, "", "Error: no such formula"))

	m := NewManager(mockRunner)
	var buf bytes.Buffer

	err := m.updateViaPackageManager(context.Background(), "brew", []string{"upgrade", "azd"}, &buf)
	require.Error(t, err)

	var updateErr *UpdateError
	require.ErrorAs(t, err, &updateErr)
	require.Equal(t, CodePackageManagerFailed, updateErr.Code)
}

func TestUpdateViaPackageManager_CommandError(t *testing.T) {
	mockRunner := mockexec.NewMockCommandRunner()
	mockRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).SetError(fmt.Errorf("command not found: brew"))

	m := NewManager(mockRunner)
	var buf bytes.Buffer

	err := m.updateViaPackageManager(context.Background(), "brew", []string{"upgrade", "azd"}, &buf)
	require.Error(t, err)

	var updateErr *UpdateError
	require.ErrorAs(t, err, &updateErr)
	require.Equal(t, CodePackageManagerFailed, updateErr.Code)
}

func TestVerifyCodeSignature_NilRunner(t *testing.T) {
	m := NewManager(nil)
	err := m.verifyCodeSignature(context.Background(), "/some/binary", io.Discard)
	require.NoError(t, err, "should skip when no command runner")
}

func TestExtractFromZip(t *testing.T) {
	tempDir := t.TempDir()

	// Create a zip archive containing a fake "azd" binary
	archivePath := filepath.Join(tempDir, "test.zip")
	binaryContent := []byte("#!/bin/sh\necho hello")

	zipFile, err := os.Create(archivePath)
	require.NoError(t, err)

	zw := zip.NewWriter(zipFile)
	fw, err := zw.Create("azd")
	require.NoError(t, err)
	_, err = fw.Write(binaryContent)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, zipFile.Close())

	// Extract
	destPath := filepath.Join(tempDir, "extracted-azd")
	err = extractFromZip(archivePath, "azd", destPath)
	require.NoError(t, err)

	// Verify content
	extracted, err := os.ReadFile(destPath)
	require.NoError(t, err)
	require.Equal(t, binaryContent, extracted)
}

func TestExtractFromZip_BinaryNotFound(t *testing.T) {
	tempDir := t.TempDir()

	archivePath := filepath.Join(tempDir, "empty.zip")
	zipFile, err := os.Create(archivePath)
	require.NoError(t, err)

	zw := zip.NewWriter(zipFile)
	fw, err := zw.Create("other-file.txt")
	require.NoError(t, err)
	_, err = fw.Write([]byte("not the binary"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, zipFile.Close())

	destPath := filepath.Join(tempDir, "extracted")
	err = extractFromZip(archivePath, "azd", destPath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found in archive")
}

func TestExtractFromTarGz(t *testing.T) {
	tempDir := t.TempDir()

	archivePath := filepath.Join(tempDir, "test.tar.gz")
	binaryContent := []byte("#!/bin/sh\necho hello from tar")

	// Create tar.gz
	f, err := os.Create(archivePath)
	require.NoError(t, err)

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name: "azd",
		Mode: 0o755,
		Size: int64(len(binaryContent)),
	}
	require.NoError(t, tw.WriteHeader(hdr))
	_, err = tw.Write(binaryContent)
	require.NoError(t, err)

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	require.NoError(t, f.Close())

	// Extract
	destPath := filepath.Join(tempDir, "extracted-azd")
	err = extractFromTarGz(archivePath, "azd", destPath)
	require.NoError(t, err)

	extracted, err := os.ReadFile(destPath)
	require.NoError(t, err)
	require.Equal(t, binaryContent, extracted)
}

func TestExtractBinary_ChoosesFormat(t *testing.T) {
	tempDir := t.TempDir()
	binaryContent := []byte("binary data")

	// Create a zip
	archivePath := filepath.Join(tempDir, "test.zip")
	zipFile, err := os.Create(archivePath)
	require.NoError(t, err)
	zw := zip.NewWriter(zipFile)
	fw, err := zw.Create("azd")
	require.NoError(t, err)
	_, err = fw.Write(binaryContent)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, zipFile.Close())

	destPath := filepath.Join(tempDir, "out-azd")
	err = extractBinary(archivePath, "azd", destPath)
	require.NoError(t, err)

	extracted, err := os.ReadFile(destPath)
	require.NoError(t, err)
	require.Equal(t, binaryContent, extracted)
}

func TestStagedBinaryPath(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	path, err := StagedBinaryPath()
	require.NoError(t, err)
	require.Contains(t, path, "staging")

	binaryName := "azd"
	if runtime.GOOS == "windows" {
		binaryName = "azd.exe"
	}
	require.True(t, strings.HasSuffix(path, binaryName))
}

func TestHasStagedUpdate_NoFile(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	require.False(t, HasStagedUpdate())
}

func TestHasStagedUpdate_WithFile(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	path, err := StagedBinaryPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("fake binary"), 0o755)) //nolint:gosec

	require.True(t, HasStagedUpdate())
}

func TestCleanStagedUpdate(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	// Stage a fake binary
	path, err := StagedBinaryPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("fake binary"), 0o755)) //nolint:gosec
	require.True(t, HasStagedUpdate())

	// Clean it
	CleanStagedUpdate()
	require.False(t, HasStagedUpdate())
}

func TestAppliedMarkerLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	// No marker initially
	_, err := ReadAppliedMarker()
	require.Error(t, err)

	// Write marker
	WriteAppliedMarker("1.22.0")

	// Read marker
	version, err := ReadAppliedMarker()
	require.NoError(t, err)
	require.Equal(t, "1.22.0", version)

	// Remove marker
	RemoveAppliedMarker()
	_, err = ReadAppliedMarker()
	require.Error(t, err)
}

func TestProgressReader(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 100)
	reader := bytes.NewReader(content)

	var output bytes.Buffer
	pr := &progressReader{
		reader: reader,
		total:  100,
		writer: &output,
	}

	buf := make([]byte, 10)
	totalRead := 0
	for {
		n, err := pr.Read(buf)
		totalRead += n
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}

	require.Equal(t, 100, totalRead)
	// Should have reported at least some progress percentages
	require.Contains(t, output.String(), "%")
}

func TestCopyFile(t *testing.T) {
	tempDir := t.TempDir()

	src := filepath.Join(tempDir, "source")
	dst := filepath.Join(tempDir, "dest")
	content := []byte("hello world")

	require.NoError(t, os.WriteFile(src, content, 0o600))
	require.NoError(t, copyFile(src, dst))

	copied, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, content, copied)
}

func TestUpdateError(t *testing.T) {
	inner := fmt.Errorf("connection refused")
	ue := newUpdateError(CodeDownloadFailed, inner)

	require.Equal(t, "connection refused", ue.Error())
	require.Equal(t, CodeDownloadFailed, ue.Code)
	require.ErrorIs(t, ue, inner)

	ue2 := newUpdateErrorf(CodeDownloadFailed, "hash mismatch: expected %s", "abc123")
	require.Contains(t, ue2.Error(), "hash mismatch")
	require.Equal(t, CodeDownloadFailed, ue2.Code)
}

func TestErrNeedsElevation(t *testing.T) {
	require.NotNil(t, ErrNeedsElevation)
	require.Contains(t, ErrNeedsElevation.Error(), "elevation")
}

func TestDownloadFile(t *testing.T) {
	content := []byte("downloaded binary content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	destPath := filepath.Join(tempDir, "downloaded")

	m := NewManager(nil)
	err := m.downloadFile(context.Background(), server.URL+"/azd.zip", destPath, io.Discard)
	require.NoError(t, err)

	got, err := os.ReadFile(destPath)
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestDownloadFile_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	destPath := filepath.Join(tempDir, "downloaded")

	m := NewManager(nil)
	err := m.downloadFile(context.Background(), server.URL+"/missing.zip", destPath, io.Discard)
	require.Error(t, err)
	require.Contains(t, err.Error(), "404")
}

// urlRewriteTransport rewrites all outgoing request URLs to point at a test server.
type urlRewriteTransport struct {
	base      http.RoundTripper
	targetURL string
}

func (t *urlRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite the request URL to the test server, preserving path
	newURL := t.targetURL + req.URL.Path
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, newURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	return t.base.RoundTrip(newReq)
}
