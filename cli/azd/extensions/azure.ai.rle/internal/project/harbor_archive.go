// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const maxHarborExpandedBytes = 256 << 20
const maxHarborArchiveEntries = 10000

var harborArtifactName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// HarborArchive identifies validated archive bytes and their environment-only build context.
type HarborArchive struct {
	BundleSHA256       string
	BuildContextSHA256 string
}

func readHarborArchive(filename string) ([]byte, error) {
	if !strings.HasSuffix(filename, ".tar.gz") {
		return nil, fmt.Errorf("Harbor task archive %q must use .tar.gz", filename)
	}
	//nolint:gosec // Explicit user-selected archive; reads are capped and no members are extracted.
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxHarborBundleBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > MaxHarborBundleBytes {
		return nil, fmt.Errorf("Harbor archive %q must be nonempty and at most %d bytes", filename, MaxHarborBundleBytes)
	}
	return raw, nil
}

// InspectHarborArchive validates a complete root-layout task archive without extracting it.
func InspectHarborArchive(filename string) (HarborArchive, error) {
	raw, err := readHarborArchive(filename)
	if err != nil {
		return HarborArchive{}, err
	}
	result, err := inspectHarborArchiveBytes(raw)
	if err != nil {
		return HarborArchive{}, fmt.Errorf("invalid Harbor archive %q: %w", filename, err)
	}
	return result, nil
}

func inspectHarborArchiveBytes(raw []byte) (HarborArchive, error) {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return HarborArchive{}, err
	}
	defer gz.Close()
	// Bound tar headers/padding as well as file payloads, including any gzip trailer data.
	limited := &io.LimitedReader{R: gz, N: maxHarborExpandedBytes + maxHarborArchiveEntries*1024 + 1}
	tr := tar.NewReader(limited)
	names := map[string]bool{}
	regular := map[string]bool{}
	var contextFiles []string
	contextEntries := map[string][4]any{}
	var expanded int64
	for count := 0; ; count++ {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return HarborArchive{}, err
		}
		if count >= maxHarborArchiveEntries {
			return HarborArchive{}, fmt.Errorf("archive exceeds %d entries", maxHarborArchiveEntries)
		}
		name := strings.TrimPrefix(header.Name, "./")
		if (name == "" || name == ".") && header.Typeflag == tar.TypeDir {
			continue
		}
		if name == "" || name == "." || path.IsAbs(name) || strings.ContainsAny(name, `\:`) ||
			slices.Contains(strings.Split(name, "/"), "..") || header.Mode&0o6000 != 0 ||
			(header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir) {
			return HarborArchive{}, fmt.Errorf("unsafe archive member %q", header.Name)
		}
		name = path.Clean(name)
		if names[name] {
			return HarborArchive{}, fmt.Errorf("duplicate archive member %q", name)
		}
		names[name] = true
		if header.Typeflag == tar.TypeDir {
			if name == "environment" || strings.HasPrefix(name, "environment/") {
				contextFiles = append(contextFiles, name)
				contextEntries[name] = [4]any{name, header.Mode & 0o777, 0, "directory"}
			}
			continue
		}
		regular[name] = true
		if header.Size < 0 || header.Size > maxHarborExpandedBytes-expanded {
			return HarborArchive{}, fmt.Errorf("expanded archive exceeds %d bytes", maxHarborExpandedBytes)
		}
		expanded += header.Size
		h := sha256.New()
		var config bytes.Buffer
		var destination io.Writer = h
		if name == "task.toml" {
			destination = io.MultiWriter(h, &config)
		}
		if _, err := io.CopyN(destination, tr, header.Size); err != nil {
			return HarborArchive{}, err
		}
		if name == "task.toml" {
			if err := validateHarborConfig(config.Bytes()); err != nil {
				return HarborArchive{}, err
			}
		}
		if strings.HasPrefix(name, "environment/") {
			contextFiles = append(contextFiles, name)
			contextEntries[name] = [4]any{name, header.Mode & 0o777, header.Size, hex.EncodeToString(h.Sum(nil))}
		}
	}
	for _, required := range []string{"task.toml", "instruction.md", "environment/Dockerfile", "tests/test.sh"} {
		if !regular[required] {
			return HarborArchive{}, fmt.Errorf("missing regular file %s at archive root", required)
		}
	}
	for _, compose := range []string{"docker-compose.yaml", "docker-compose.yml", "compose.yaml", "compose.yml"} {
		if names["environment/"+compose] {
			return HarborArchive{}, fmt.Errorf("multi-container tasks are not supported")
		}
	}
	for name := range names {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if regular[parent] {
				return HarborArchive{}, fmt.Errorf("archive member %q is nested under a regular file", name)
			}
		}
	}
	if _, err := io.Copy(io.Discard, limited); err != nil {
		return HarborArchive{}, err
	}
	if limited.N <= 0 {
		return HarborArchive{}, fmt.Errorf("expanded tar stream exceeds its size limit")
	}
	slices.Sort(contextFiles)
	entries := make([][4]any, 0, len(contextFiles))
	for _, name := range contextFiles {
		entries = append(entries, contextEntries[name])
	}
	contextJSON, err := json.Marshal(entries)
	if err != nil {
		return HarborArchive{}, err
	}
	bundleHash, contextHash := sha256.Sum256(raw), sha256.Sum256(contextJSON)
	return HarborArchive{
		BundleSHA256: hex.EncodeToString(bundleHash[:]), BuildContextSHA256: hex.EncodeToString(contextHash[:]),
	}, nil
}

// ReadHarborArtifactBundle revalidates and returns the exact full archive, never a repackaged bundle.
func ReadHarborArtifactBundle(manifestPath string, task TaskArtifact) ([]byte, error) {
	raw, err := readHarborArchive(artifactBundlePath(manifestPath, task.LocalBundlePath))
	if err != nil {
		return nil, err
	}

	info, err := inspectHarborArchiveBytes(raw)
	if err != nil {
		return nil, err
	}
	if task.CLI == nil || info.BundleSHA256 != task.CLI.BundleSHA256 {
		return nil, fmt.Errorf("archive for task %q changed after preparation; prepare it again", task.TaskName)
	}
	return raw, nil
}

func artifactBundlePath(manifestPath, bundlePath string) string {
	bundlePath = filepath.FromSlash(bundlePath)
	if filepath.IsAbs(bundlePath) {
		return bundlePath
	}
	return filepath.Join(filepath.Dir(manifestPath), bundlePath)
}
