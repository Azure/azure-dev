// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/pelletier/go-toml/v2"
)

const (
	rleSamplesRepoURL = "https://github.com/sujit-kamireddy/rle-samples.git"
	rleSamplesRepoRef = "main"

	// rleGymSamplesPath is the rle-samples repo path holding self-contained
	// Gym/OpenEnv sample directories, one per sample name.
	rleGymSamplesPath = "examples/gym/openenv"

	// rleGymSampleCatalogFile is the name of the catalog file, relative to
	// rleGymSamplesPath, that controls which Gym/OpenEnv samples are visible
	// from the CLI. Samples with no entry in the catalog default to visible.
	rleGymSampleCatalogFile = "catalog.toml"
)

// RleSampleCatalogOptions controls how the Gym/OpenEnv sample catalog is loaded.
type RleSampleCatalogOptions struct {
	// ShowHiddenSamples bypasses the catalog's visibility filter, revealing
	// every sample directory present in the samples repo. Intended for
	// internal testing of a sample before it is marked visible.
	ShowHiddenSamples bool
}

// rleSampleCatalogManifest is the schema of the samples repo's catalog.toml file.
type rleSampleCatalogManifest struct {
	Samples []rleSampleCatalogEntry `toml:"sample"`
}

type rleSampleCatalogEntry struct {
	Name    string `toml:"name"`
	Visible *bool  `toml:"visible,omitempty"`
}

type RleSampleCatalog struct {
	repoDir     string
	sampleNames []string
}

func createRleSessionDir(name string, dest string, force bool) (string, error) {
	sessionDir := filepath.Join(dest, name)
	if entries, err := os.ReadDir(sessionDir); err == nil && len(entries) > 0 && !force {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Directory %q already exists and is not empty.", sessionDir),
			Code:       "rle_session_exists",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use --force to overwrite generated files, or choose a different environment name.",
		}
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if force {
		if err := os.RemoveAll(sessionDir); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(sessionDir, 0750); err != nil {
		return "", err
	}
	return sessionDir, nil
}

func LoadRleSampleCatalog(options RleSampleCatalogOptions) (*RleSampleCatalog, error) {
	return loadRleSampleCatalog(rleSamplesRepoURL, rleSamplesRepoRef, options)
}

func loadRleSampleCatalog(repoURL string, repoRef string, options RleSampleCatalogOptions) (*RleSampleCatalog, error) {
	tempDir, err := os.MkdirTemp("", "azd-rle-samples-*")
	if err != nil {
		return nil, err
	}
	if _, err := runGitCommand(
		"clone",
		"--depth", "1",
		"--filter=blob:none",
		"--sparse",
		"--branch", repoRef,
		"--single-branch",
		repoURL,
		tempDir,
	); err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, err
	}

	sampleNames, err := listRleSamples(tempDir, repoRef)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, err
	}
	if !options.ShowHiddenSamples {
		visibility, err := loadRleSampleCatalogVisibility(tempDir, repoRef)
		if err != nil {
			_ = os.RemoveAll(tempDir)
			return nil, err
		}
		sampleNames = slices.DeleteFunc(sampleNames, func(name string) bool {
			visible, ok := visibility[name]
			return ok && !visible
		})
	}
	if len(sampleNames) == 0 {
		_ = os.RemoveAll(tempDir)
		return nil, &azdext.LocalError{
			Message:  "The RLE samples repository does not contain any sample environments.",
			Code:     "rle_samples_empty",
			Category: azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf(
				"Add sample directories to %s, then retry.",
				strings.TrimSuffix(rleSamplesRepoURL, ".git"),
			),
		}
	}
	return &RleSampleCatalog{
		repoDir:     tempDir,
		sampleNames: sampleNames,
	}, nil
}

func (c *RleSampleCatalog) SampleNames() []string {
	return slices.Clone(c.sampleNames)
}

func (c *RleSampleCatalog) Copy(sampleName string, folderName string, dest string, force bool) (string, error) {
	folderName, err := ValidateEnvironmentName(folderName)
	if err != nil {
		return "", err
	}
	sourcePath := filepath.ToSlash(filepath.Join(rleGymSamplesPath, sampleName))
	if _, err := runGitCommand("-C", c.repoDir, "sparse-checkout", "set", sourcePath); err != nil {
		return "", err
	}
	sourceDir := filepath.Join(c.repoDir, filepath.FromSlash(sourcePath))
	return copyRleSample(sourceDir, folderName, dest, force)
}

func (c *RleSampleCatalog) Close() error {
	return os.RemoveAll(c.repoDir)
}

// loadRleSampleCatalogVisibility reads the samples repo's catalog.toml, if present, and
// returns a map of sample name to its declared visibility. Samples without an entry are
// omitted from the map, and callers should treat them as visible by default.
func loadRleSampleCatalogVisibility(repoDir string, repoRef string) (map[string]bool, error) {
	catalogPath := filepath.ToSlash(filepath.Join(rleGymSamplesPath, rleGymSampleCatalogFile))
	output, err := runGitCommand("-C", repoDir, "show", repoRef+":"+catalogPath)
	if err != nil {
		// The catalog file is optional; treat any samples as visible when it is absent.
		return nil, nil
	}
	var manifest rleSampleCatalogManifest
	if err := toml.Unmarshal(output, &manifest); err != nil {
		return nil, &azdext.LocalError{
			Message:    fmt.Sprintf("Failed to parse RLE sample catalog %q: %v", catalogPath, err),
			Code:       "rle_sample_catalog_invalid",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Fix the catalog.toml file in the RLE samples repository, then retry.",
		}
	}
	visibility := make(map[string]bool, len(manifest.Samples))
	for _, sample := range manifest.Samples {
		visible := true
		if sample.Visible != nil {
			visible = *sample.Visible
		}
		visibility[sample.Name] = visible
	}
	return visibility, nil
}

func listRleSamples(repoDir string, repoRef string) ([]string, error) {
	output, err := runGitCommand(
		"-C",
		repoDir,
		"ls-tree",
		"-d",
		"--name-only",
		repoRef+":"+rleGymSamplesPath,
	)
	if err != nil {
		return nil, err
	}
	sampleNames := strings.Fields(string(output))
	sampleNames = slices.DeleteFunc(sampleNames, func(name string) bool {
		return strings.HasPrefix(name, ".")
	})
	slices.Sort(sampleNames)
	return sampleNames, nil
}

func copyRleSample(sourceDir string, folderName string, dest string, force bool) (string, error) {
	sourceInfo, err := os.Stat(sourceDir)
	if os.IsNotExist(err) {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("RLE sample source %q was not found.", sourceDir),
			Code:       "rle_sample_source_not_found",
			Category:   azdext.LocalErrorCategoryInternal,
			Suggestion: "Run azd ai rle init again to refresh the sample list.",
		}
	} else if err != nil {
		return "", err
	} else if !sourceInfo.IsDir() {
		return "", fmt.Errorf("RLE sample source %q is not a directory", sourceDir)
	}
	sessionDir, err := createRleSessionDir(folderName, dest, force)
	if err != nil {
		return "", err
	}
	if err := copyDirectory(sourceDir, sessionDir); err != nil {
		return "", err
	}
	return sessionDir, nil
}

func runGitCommand(args ...string) ([]byte, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, &azdext.LocalError{
			Message:    "Could not find \"git\" on PATH.",
			Code:       "rle_git_not_found",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Install Git, then retry azd ai rle init.",
		}
	}
	process := exec.Command("git", args...) //nolint:gosec
	process.Env = os.Environ()
	output, err := process.CombinedOutput()
	if err != nil {
		return nil, &azdext.LocalError{
			Message:    fmt.Sprintf("Failed to download RLE samples: %v", err),
			Code:       "rle_samples_download_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: strings.TrimSpace(string(output)),
		}
	}
	return output, nil
}

func copyDirectory(sourceDir string, destDir string) error {
	sourceInfo, err := os.Stat(sourceDir)
	if err != nil {
		return err
	}
	if !sourceInfo.IsDir() {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("RLE source path %q is not a directory.", sourceDir),
			Code:       "rle_source_path_not_directory",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use a source directory when initializing an RLE environment.",
		}
	}
	return filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		relativePath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if relativePath == "." {
			return nil
		}
		targetPath := filepath.Join(destDir, relativePath)
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0750)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // path comes from the checked-out sample directory walk.
		if err != nil {
			return err
		}
		// targetPath is derived from the trusted source sample walk.
		return os.WriteFile(targetPath, data, info.Mode().Perm()) //nolint:gosec
	})
}
