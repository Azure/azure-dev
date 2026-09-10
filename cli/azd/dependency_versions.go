// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build mage

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const dependencyVersionConfigFile = "dependency-versions.json"

var dependencyIssuePattern = regexp.MustCompile(
	`^https://github\.com/Azure/azure-dev/issues/[1-9][0-9]*$`,
)

type dependencyVersionConfig struct {
	Overrides []dependencyVersionOverride `json:"overrides"`
}

type dependencyVersionOverride struct {
	Module     string `json:"module"`
	Dependency string `json:"dependency"`
	Version    string `json:"version"`
	Reason     string `json:"reason"`
	Issue      string `json:"issue"`
}

type goModFile struct {
	Require []goModRequirement
}

type goModRequirement struct {
	Path     string
	Version  string
	Indirect bool
}

type dependencyVersionMismatch struct {
	ModulePath   string
	GoModPath    string
	Dependency   string
	CoreVersion  string
	FoundVersion string
	Override     *dependencyVersionOverride
}

type dependencyVersionReport struct {
	Mismatches     []dependencyVersionMismatch
	StaleOverrides []dependencyVersionOverride
}

// CheckDependencyVersions verifies that dependencies directly required by both azd core and a
// first-party extension use the same version. Temporary exceptions must be declared in
// dependency-versions.json.
//
// Usage: mage checkDependencyVersions
func CheckDependencyVersions() error {
	azdDir, err := dependencyVersionAzdDir()
	if err != nil {
		return err
	}
	return checkDependencyVersions(azdDir, os.Stdout)
}

func checkDependencyVersions(azdDir string, w io.Writer) error {
	report, err := analyzeDependencyVersions(azdDir)
	if err != nil {
		return err
	}
	printDependencyVersionReport(w, report)
	if len(report.Mismatches) > 0 || len(report.StaleOverrides) > 0 {
		return errors.New("dependency version validation failed")
	}

	fmt.Fprintln(w, "All shared direct dependency versions match azd core.")
	return nil
}

// SyncDependencyVersions updates unapproved extension dependency mismatches to the versions
// selected by azd core, then runs go mod tidy in each changed extension module.
//
// Usage: mage syncDependencyVersions
func SyncDependencyVersions() error {
	azdDir, err := dependencyVersionAzdDir()
	if err != nil {
		return err
	}
	return syncDependencyVersions(azdDir, os.Stdout)
}

func syncDependencyVersions(azdDir string, w io.Writer) error {
	report, err := analyzeDependencyVersions(azdDir)
	if err != nil {
		return err
	}
	if len(report.StaleOverrides) > 0 {
		printDependencyVersionReport(w, report)
		return errors.New("remove stale dependency overrides before synchronizing")
	}

	changedModules := map[string]bool{}
	for _, mismatch := range report.Mismatches {
		moduleDir := filepath.Dir(filepath.Join(azdDir, filepath.FromSlash(mismatch.GoModPath)))
		requirement := mismatch.Dependency + "@" + mismatch.CoreVersion
		if err := runDependencyGoCommand(moduleDir, w, "mod", "edit", "-require="+requirement); err != nil {
			return fmt.Errorf("updating %s in %s: %w", mismatch.Dependency, mismatch.ModulePath, err)
		}
		changedModules[moduleDir] = true
		fmt.Fprintf(
			w,
			"Updated %s in %s: %s -> %s\n",
			mismatch.Dependency,
			mismatch.ModulePath,
			mismatch.FoundVersion,
			mismatch.CoreVersion,
		)
	}

	moduleDirs := slices.Sorted(maps.Keys(changedModules))
	for _, moduleDir := range moduleDirs {
		if err := runDependencyGoCommand(moduleDir, w, "mod", "tidy"); err != nil {
			return fmt.Errorf("running go mod tidy in %s: %w", moduleDir, err)
		}
	}

	if len(changedModules) == 0 {
		fmt.Fprintln(w, "No dependency versions needed synchronization.")
		return nil
	}

	finalReport, err := analyzeDependencyVersions(azdDir)
	if err != nil {
		return err
	}
	printDependencyVersionReport(w, finalReport)
	if len(finalReport.Mismatches) > 0 || len(finalReport.StaleOverrides) > 0 {
		return errors.New("dependency versions remain inconsistent after synchronization")
	}

	fmt.Fprintf(w, "Synchronized dependency versions in %d extension module(s).\n", len(changedModules))
	return nil
}

func dependencyVersionAzdDir() (string, error) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(repoRoot, "cli", "azd"), nil
}

func analyzeDependencyVersions(azdDir string) (*dependencyVersionReport, error) {
	config, err := loadDependencyVersionConfig(azdDir)
	if err != nil {
		return nil, err
	}
	overrides, err := validateDependencyVersionOverrides(config.Overrides)
	if err != nil {
		return nil, err
	}

	coreMod, err := loadGoModFile(filepath.Join(azdDir, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("reading azd core go.mod: %w", err)
	}
	coreVersions := map[string]string{}
	for _, requirement := range coreMod.Require {
		if !requirement.Indirect {
			coreVersions[requirement.Path] = requirement.Version
		}
	}

	goModPaths, err := extensionGoModPaths(azdDir)
	if err != nil {
		return nil, err
	}

	report := &dependencyVersionReport{}
	usedOverrides := map[string]bool{}
	for _, goModPath := range goModPaths {
		mod, err := loadGoModFile(filepath.Join(azdDir, filepath.FromSlash(goModPath)))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", goModPath, err)
		}
		modulePath := filepath.ToSlash(filepath.Dir(goModPath))
		for _, requirement := range mod.Require {
			if requirement.Indirect {
				continue
			}
			coreVersion, managed := coreVersions[requirement.Path]
			if !managed || requirement.Version == coreVersion {
				continue
			}

			key := dependencyOverrideKey(modulePath, requirement.Path)
			override := overrides[key]
			if override != nil && override.Version == requirement.Version {
				usedOverrides[key] = true
				continue
			}
			report.Mismatches = append(report.Mismatches, dependencyVersionMismatch{
				ModulePath:   modulePath,
				GoModPath:    goModPath,
				Dependency:   requirement.Path,
				CoreVersion:  coreVersion,
				FoundVersion: requirement.Version,
				Override:     override,
			})
		}
	}

	for key, override := range overrides {
		if !usedOverrides[key] {
			report.StaleOverrides = append(report.StaleOverrides, *override)
		}
	}
	sortDependencyVersionReport(report)
	return report, nil
}

func loadDependencyVersionConfig(azdDir string) (*dependencyVersionConfig, error) {
	path := filepath.Join(azdDir, dependencyVersionConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dependencyVersionConfigFile, err)
	}

	var config dependencyVersionConfig
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", dependencyVersionConfigFile, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return nil, fmt.Errorf("parsing %s: unexpected trailing content: %w", dependencyVersionConfigFile, err)
	}
	return &config, nil
}

func validateDependencyVersionOverrides(
	entries []dependencyVersionOverride,
) (map[string]*dependencyVersionOverride, error) {
	overrides := make(map[string]*dependencyVersionOverride, len(entries))
	for i := range entries {
		override := &entries[i]
		override.Module = filepath.ToSlash(filepath.Clean(override.Module))
		if override.Module == "." || strings.HasPrefix(override.Module, "../") ||
			!strings.HasPrefix(override.Module, "extensions/") {
			return nil, fmt.Errorf("override module %q must be under extensions/", override.Module)
		}
		if override.Dependency == "" || override.Version == "" || override.Reason == "" || override.Issue == "" {
			return nil, fmt.Errorf(
				"override for %s in %s must include dependency, version, reason, and issue",
				override.Dependency,
				override.Module,
			)
		}
		if !dependencyIssuePattern.MatchString(override.Issue) {
			return nil, fmt.Errorf(
				"override for %s in %s must link to an Azure/azure-dev issue",
				override.Dependency,
				override.Module,
			)
		}
		key := dependencyOverrideKey(override.Module, override.Dependency)
		if _, exists := overrides[key]; exists {
			return nil, fmt.Errorf("duplicate dependency override for %s in %s", override.Dependency, override.Module)
		}
		overrides[key] = override
	}
	return overrides, nil
}

func extensionGoModPaths(azdDir string) ([]string, error) {
	extensionsDir := filepath.Join(azdDir, "extensions")
	entries, err := os.ReadDir(extensionsDir)
	if err != nil {
		return nil, fmt.Errorf("reading extensions directory: %w", err)
	}

	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		goModPath := filepath.Join(extensionsDir, entry.Name(), "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			paths = append(paths, filepath.ToSlash(filepath.Join("extensions", entry.Name(), "go.mod")))
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("checking %s: %w", goModPath, err)
		}
	}
	slices.Sort(paths)
	return paths, nil
}

func loadGoModFile(path string) (*goModFile, error) {
	cmd := exec.Command("go", "mod", "edit", "-json", path)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}

	var mod goModFile
	if err := json.Unmarshal(output, &mod); err != nil {
		return nil, fmt.Errorf("decoding go mod edit output: %w", err)
	}
	return &mod, nil
}

func runDependencyGoCommand(dir string, w io.Writer, args ...string) error {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go %s failed: %w", strings.Join(args, " "), err)
	}
	return nil
}

func printDependencyVersionReport(w io.Writer, report *dependencyVersionReport) {
	for _, mismatch := range report.Mismatches {
		message := fmt.Sprintf(
			"%s uses %s, but azd core requires %s",
			mismatch.Dependency,
			mismatch.FoundVersion,
			mismatch.CoreVersion,
		)
		if mismatch.Override != nil {
			message += fmt.Sprintf("; the configured override only allows %s", mismatch.Override.Version)
		}
		fmt.Fprintf(w, "ERROR: %s: %s\n", mismatch.GoModPath, message)
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			fmt.Fprintf(w, "::error file=cli/azd/%s::%s\n", mismatch.GoModPath, message)
		}
	}
	for _, override := range report.StaleOverrides {
		message := fmt.Sprintf(
			"stale override for %s in %s; remove or update it",
			override.Dependency,
			override.Module,
		)
		fmt.Fprintf(w, "ERROR: %s\n", message)
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			fmt.Fprintf(w, "::error file=cli/azd/%s::%s\n", dependencyVersionConfigFile, message)
		}
	}
	if len(report.Mismatches) > 0 {
		fmt.Fprintln(w, "Run 'cd cli/azd && mage syncDependencyVersions' to align unapproved mismatches.")
	}
}

func sortDependencyVersionReport(report *dependencyVersionReport) {
	slices.SortFunc(report.Mismatches, func(a, b dependencyVersionMismatch) int {
		if result := strings.Compare(a.ModulePath, b.ModulePath); result != 0 {
			return result
		}
		return strings.Compare(a.Dependency, b.Dependency)
	})
	slices.SortFunc(report.StaleOverrides, func(a, b dependencyVersionOverride) int {
		if result := strings.Compare(a.Module, b.Module); result != 0 {
			return result
		}
		return strings.Compare(a.Dependency, b.Dependency)
	})
}

func dependencyOverrideKey(modulePath, dependency string) string {
	return modulePath + "\x00" + dependency
}
