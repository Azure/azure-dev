// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package project

import (
	"errors"
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

	// RleSkillsPath contains the project skills copied into every initialized
	// Gym/OpenEnv project so compatible agents can assist with authoring.
	RleSkillsPath        = ".agents/skills"
	rleGymSkillDirectory = "rle-gym-openenv"

	// rleSampleCatalogFile is the name of the catalog file, relative to a
	// directory holding sample directories, that controls which of them are
	// visible from the CLI. Samples with no entry in the catalog default to
	// visible, and the file itself is optional.
	rleSampleCatalogFile = "catalog.toml"

	// rleHarnessSamplesPath is the rle-samples repo path holding self-contained,
	// fully-working harness samples, grouped by harness subtype and then by
	// sample name, each sample with its own agent/ and rle/ subdirectories.
	rleHarnessSamplesPath = "examples/harness"
)

// rleHarnessSubtypeDirs maps each harness subtype to its directory under
// rleHarnessSamplesPath. Subtypes without an entry have no working sample
// available yet.
var rleHarnessSubtypeDirs = map[RleSubtype]string{
	RleSubtypeBYOH:        "byoh",
	RleSubtypeHostedAgent: "hosted-agent",
}

// rleHarnessSampleContentDirs are the subdirectories every harness sample
// contains. Finding them directly under a subtype directory identifies the
// pre-catalog layout, in which the subtype directory was itself the one and
// only sample. The samples repo ref this CLI clones floats, so it can be
// either layout at any time and both have to keep working.
var rleHarnessSampleContentDirs = []string{"agent", "rle"}
var renameRleSkillPath = os.Rename

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
	tempDir, err := cloneRleSamplesRepository(repoURL, repoRef)
	if err != nil {
		return nil, err
	}

	sampleNames, err := listRleSampleDirs(tempDir, repoRef, rleGymSamplesPath)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, err
	}
	if !options.ShowHiddenSamples {
		visibility, err := loadRleSampleCatalogVisibility(tempDir, repoRef, rleGymSamplesPath)
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

func cloneRleSamplesRepository(repoURL string, repoRef string) (string, error) {
	tempDir, err := os.MkdirTemp("", "azd-rle-samples-*")
	if err != nil {
		return "", err
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
		return "", err
	}
	return tempDir, nil
}

// InstallRleSkills adds or updates the canonical RLE project skills under dest.
func InstallRleSkills(dest string) ([]string, error) {
	return installRleSkills(rleSamplesRepoURL, rleSamplesRepoRef, dest)
}

func installRleSkills(repoURL string, repoRef string, dest string) ([]string, error) {
	tempDir, err := cloneRleSamplesRepository(repoURL, repoRef)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()
	if _, err := runGitCommand("-C", tempDir, "sparse-checkout", "set", RleSkillsPath); err != nil {
		return nil, err
	}
	skillsSourceDir := filepath.Join(tempDir, filepath.FromSlash(RleSkillsPath))
	return installRleSkillsFromDirectory(skillsSourceDir, dest)
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
	if _, err := runGitCommand(
		"-C",
		c.repoDir,
		"sparse-checkout",
		"set",
		sourcePath,
		RleSkillsPath,
	); err != nil {
		return "", err
	}
	sourceDir := filepath.Join(c.repoDir, filepath.FromSlash(sourcePath))
	skillsSourceDir := filepath.Join(c.repoDir, filepath.FromSlash(RleSkillsPath))
	return copyRleGymSample(sourceDir, skillsSourceDir, folderName, dest, force)
}

func (c *RleSampleCatalog) Close() error {
	return os.RemoveAll(c.repoDir)
}

// RleHarnessSampleCatalog is a checked-out set of fully-working harness samples
// (each with both an agent/ implementation and an rle/ wrapper) for one harness
// subtype. It is the only source a Harness init scaffolds from, so every
// Harness environment starts from something that runs end to end out of the
// box.
//
// An empty SampleNames() means the samples repo still uses the pre-catalog
// layout, where the subtype directory held agent/ and rle/ directly and was
// therefore the only sample available for that subtype.
type RleHarnessSampleCatalog struct {
	repoDir     string
	subtypePath string
	sampleNames []string
}

// LoadRleHarnessSampleCatalog fetches the fully-working harness samples
// (agent + rle) available for the given subtype.
func LoadRleHarnessSampleCatalog(
	subtype RleSubtype,
	options RleSampleCatalogOptions,
) (*RleHarnessSampleCatalog, error) {
	return loadRleHarnessSampleCatalog(rleSamplesRepoURL, rleSamplesRepoRef, subtype, options)
}

func loadRleHarnessSampleCatalog(
	repoURL string,
	repoRef string,
	subtype RleSubtype,
	options RleSampleCatalogOptions,
) (*RleHarnessSampleCatalog, error) {
	subtypeDirName, ok := rleHarnessSubtypeDirs[subtype]
	if !ok {
		return nil, &azdext.LocalError{
			Message:    fmt.Sprintf("No working RLE harness sample is available for subtype %q.", subtype),
			Code:       "rle_harness_sample_not_found",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Choose --subtype HostedAgent or --subtype BYOH.",
		}
	}
	tempDir, err := os.MkdirTemp("", "azd-rle-harness-sample-*")
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
	subtypePath := filepath.ToSlash(filepath.Join(rleHarnessSamplesPath, subtypeDirName))
	childDirs, err := listRleSampleDirs(tempDir, repoRef, subtypePath)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, &azdext.LocalError{
			Message:    fmt.Sprintf("RLE harness sample source %q was not found.", subtypePath),
			Code:       "rle_harness_sample_source_not_found",
			Category:   azdext.LocalErrorCategoryInternal,
			Suggestion: "Run azd ai rle init again to refresh the sample.",
		}
	}

	var sampleNames []string
	if !isLegacyFlatHarnessLayout(childDirs) {
		sampleNames = childDirs
		if !options.ShowHiddenSamples {
			visibility, err := loadRleSampleCatalogVisibility(tempDir, repoRef, subtypePath)
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
				Message: fmt.Sprintf(
					"The RLE samples repository does not contain any %s harness samples.",
					subtype,
				),
				Code:     "rle_harness_samples_empty",
				Category: azdext.LocalErrorCategoryUser,
				Suggestion: fmt.Sprintf(
					"Add a sample directory under %s in %s, then retry.",
					subtypePath,
					strings.TrimSuffix(rleSamplesRepoURL, ".git"),
				),
			}
		}
	}
	return &RleHarnessSampleCatalog{
		repoDir:     tempDir,
		subtypePath: subtypePath,
		sampleNames: sampleNames,
	}, nil
}

// SampleNames returns the sample names available for the subtype, or an empty
// slice when the samples repo still uses the pre-catalog layout and the subtype
// therefore offers a single, unnamed sample.
func (c *RleHarnessSampleCatalog) SampleNames() []string {
	return slices.Clone(c.sampleNames)
}

// Copy copies the named sample's agent/ and rle/ subdirectories into
// dest/folderName. Uses the same non-empty-directory/--force semantics as
// RleSampleCatalog.Copy. sampleName must be empty when SampleNames() is empty.
func (c *RleHarnessSampleCatalog) Copy(
	sampleName string,
	folderName string,
	dest string,
	force bool,
) (string, error) {
	folderName, err := ValidateEnvironmentName(folderName)
	if err != nil {
		return "", err
	}
	sourcePath := c.subtypePath
	switch {
	case len(c.sampleNames) == 0 && sampleName != "":
		return "", &azdext.LocalError{
			Message: fmt.Sprintf(
				"RLE harness sample %q was not found: %s holds a single unnamed sample.",
				sampleName,
				c.subtypePath,
			),
			Code:       "rle_harness_sample_not_found",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Omit --sample, or upgrade to a samples repository that groups samples by name.",
		}
	case len(c.sampleNames) > 0:
		if !slices.Contains(c.sampleNames, sampleName) {
			return "", &azdext.LocalError{
				Message:    fmt.Sprintf("RLE harness sample %q was not found.", sampleName),
				Code:       "rle_harness_sample_not_found",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: fmt.Sprintf("Choose one of the available samples: %s.", strings.Join(c.sampleNames, ", ")),
			}
		}
		sourcePath = filepath.ToSlash(filepath.Join(c.subtypePath, sampleName))
	}
	if _, err := runGitCommand("-C", c.repoDir, "sparse-checkout", "set", sourcePath); err != nil {
		return "", err
	}
	sourceDir := filepath.Join(c.repoDir, filepath.FromSlash(sourcePath))
	return copyRleSample(sourceDir, folderName, dest, force)
}

func (c *RleHarnessSampleCatalog) Close() error {
	return os.RemoveAll(c.repoDir)
}

// isLegacyFlatHarnessLayout reports whether a subtype directory holds a sample's
// own content directories rather than named sample directories.
func isLegacyFlatHarnessLayout(childDirs []string) bool {
	for _, contentDir := range rleHarnessSampleContentDirs {
		if !slices.Contains(childDirs, contentDir) {
			return false
		}
	}
	return true
}

// loadRleSampleCatalogVisibility reads samplesPath's catalog.toml, if present, and
// returns a map of sample name to its declared visibility. Samples without an entry are
// omitted from the map, and callers should treat them as visible by default.
func loadRleSampleCatalogVisibility(repoDir string, repoRef string, samplesPath string) (map[string]bool, error) {
	catalogPath := filepath.ToSlash(filepath.Join(samplesPath, rleSampleCatalogFile))
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

func listRleSampleDirs(repoDir string, repoRef string, samplesPath string) ([]string, error) {
	output, err := runGitCommand(
		"-C",
		repoDir,
		"ls-tree",
		"-d",
		"--name-only",
		repoRef+":"+samplesPath,
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
	if err := validateRleSampleSource(sourceDir); err != nil {
		return "", err
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

func copyRleGymSample(
	sourceDir string,
	skillsSourceDir string,
	folderName string,
	dest string,
	force bool,
) (string, error) {
	if err := validateRleSkillsSource(skillsSourceDir); err != nil {
		return "", err
	}
	sessionDir, err := copyRleSample(sourceDir, folderName, dest, force)
	if err != nil {
		return "", err
	}
	if _, err := installRleSkillsFromDirectory(skillsSourceDir, sessionDir); err != nil {
		return "", err
	}
	return sessionDir, nil
}

func validateRleSampleSource(sourceDir string) error {
	sourceInfo, err := os.Stat(sourceDir)
	if os.IsNotExist(err) {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("RLE sample source %q was not found.", sourceDir),
			Code:       "rle_sample_source_not_found",
			Category:   azdext.LocalErrorCategoryInternal,
			Suggestion: "Run azd ai rle init again to refresh the sample list.",
		}
	} else if err != nil {
		return err
	} else if !sourceInfo.IsDir() {
		return fmt.Errorf("RLE sample source %q is not a directory", sourceDir)
	}
	return nil
}

func validateRleSkillsSource(sourceDir string) error {
	sourceInfo, err := os.Stat(sourceDir)
	if os.IsNotExist(err) {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("RLE project skills source %q was not found.", sourceDir),
			Code:       "rle_skills_source_not_found",
			Category:   azdext.LocalErrorCategoryInternal,
			Suggestion: "Ensure the RLE samples repository contains .agents/skills, then retry.",
		}
	} else if err != nil {
		return err
	} else if !sourceInfo.IsDir() {
		return fmt.Errorf("RLE project skills source %q is not a directory", sourceDir)
	}
	skillFile := filepath.Join(sourceDir, rleGymSkillDirectory, "SKILL.md")
	skillFileInfo, err := os.Stat(skillFile)
	if os.IsNotExist(err) {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("RLE Gym/OpenEnv authoring skill file %q was not found.", skillFile),
			Code:       "rle_gym_skill_file_not_found",
			Category:   azdext.LocalErrorCategoryInternal,
			Suggestion: "Ensure the RLE samples repository contains the rle-gym-openenv skill, then retry.",
		}
	} else if err != nil {
		return err
	} else if !skillFileInfo.Mode().IsRegular() {
		return fmt.Errorf("RLE Gym/OpenEnv authoring skill file %q is not a regular file", skillFile)
	}
	return nil
}

func installRleSkillsFromDirectory(sourceDir string, dest string) ([]string, error) {
	if err := validateRleSkillsSource(sourceDir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return nil, err
	}
	skillNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillFile := filepath.Join(sourceDir, entry.Name(), "SKILL.md")
		if info, err := os.Stat(skillFile); err != nil {
			return nil, fmt.Errorf("validate RLE project skill %q: %w", entry.Name(), err)
		} else if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("RLE project skill file %q is not a regular file", skillFile)
		}
		skillNames = append(skillNames, entry.Name())
	}
	slices.Sort(skillNames)

	skillsDestDir := filepath.Join(dest, filepath.FromSlash(RleSkillsPath))
	if err := os.MkdirAll(skillsDestDir, 0750); err != nil {
		return nil, err
	}
	stagingDir, err := os.MkdirTemp(skillsDestDir, ".rle-install-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = os.RemoveAll(stagingDir)
	}()

	stagedSkillsDir := filepath.Join(stagingDir, "skills")
	if err := os.MkdirAll(stagedSkillsDir, 0750); err != nil {
		return nil, err
	}
	for _, skillName := range skillNames {
		stagedSkillDir := filepath.Join(stagedSkillsDir, skillName)
		if err := os.MkdirAll(stagedSkillDir, 0750); err != nil {
			return nil, err
		}
		if err := copyDirectory(filepath.Join(sourceDir, skillName), stagedSkillDir); err != nil {
			return nil, err
		}
	}

	backupDir := filepath.Join(stagingDir, "backup")
	existingSkills := make(map[string]bool, len(skillNames))
	for _, skillName := range skillNames {
		skillDestDir := filepath.Join(skillsDestDir, skillName)
		skillBackupDir := filepath.Join(backupDir, skillName)
		if _, err := os.Stat(skillDestDir); err == nil {
			if err := os.MkdirAll(backupDir, 0750); err != nil {
				return nil, err
			}
			if err := renameRleSkillPath(skillDestDir, skillBackupDir); err != nil {
				return nil, errors.Join(err, restoreRleSkillBackups(skillsDestDir, backupDir, existingSkills))
			}
			existingSkills[skillName] = true
		} else if !os.IsNotExist(err) {
			return nil, errors.Join(err, restoreRleSkillBackups(skillsDestDir, backupDir, existingSkills))
		}
	}

	installedSkills := make([]string, 0, len(skillNames))
	for _, skillName := range skillNames {
		skillDestDir := filepath.Join(skillsDestDir, skillName)
		stagedSkillDir := filepath.Join(stagedSkillsDir, skillName)
		if err := renameRleSkillPath(stagedSkillDir, skillDestDir); err != nil {
			rollbackErr := rollbackRleSkillInstall(skillsDestDir, backupDir, existingSkills, installedSkills)
			return nil, errors.Join(err, rollbackErr)
		}
		installedSkills = append(installedSkills, skillName)
	}
	return skillNames, nil
}

func rollbackRleSkillInstall(
	skillsDestDir string,
	backupDir string,
	existingSkills map[string]bool,
	installedSkills []string,
) error {
	var rollbackErrors []error
	for _, skillName := range installedSkills {
		if err := os.RemoveAll(filepath.Join(skillsDestDir, skillName)); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	if err := restoreRleSkillBackups(skillsDestDir, backupDir, existingSkills); err != nil {
		rollbackErrors = append(rollbackErrors, err)
	}
	return errors.Join(rollbackErrors...)
}

func restoreRleSkillBackups(skillsDestDir string, backupDir string, existingSkills map[string]bool) error {
	var restoreErrors []error
	for skillName := range existingSkills {
		skillDestDir := filepath.Join(skillsDestDir, skillName)
		if err := os.RemoveAll(skillDestDir); err != nil {
			restoreErrors = append(restoreErrors, err)
			continue
		}
		if err := renameRleSkillPath(filepath.Join(backupDir, skillName), skillDestDir); err != nil {
			restoreErrors = append(restoreErrors, err)
		}
	}
	return errors.Join(restoreErrors...)
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
