// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build mage

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSharedDependency = "example.com/shared"

func TestDependencyVersionAligned(t *testing.T) {
	azdDir := newDependencyVersionTestRepo(t, "v1.2.0", "v1.2.0", nil)

	report, err := analyzeDependencyVersions(azdDir)
	if err != nil {
		t.Fatalf("analyze dependency versions: %v", err)
	}
	if len(report.Mismatches) != 0 {
		t.Fatalf("expected no mismatches, got %#v", report.Mismatches)
	}
	if len(report.StaleOverrides) != 0 {
		t.Fatalf("expected no stale overrides, got %#v", report.StaleOverrides)
	}
}

func TestDependencyVersionIgnoresIndirectRequirements(t *testing.T) {
	azdDir := newDependencyVersionTestRepo(t, "v1.2.0", "v1.1.0", nil)
	writeTestFile(t, filepath.Join(azdDir, "extensions", "test.extension", "go.mod"), `module example.com/extension

go 1.26.0

require example.com/shared v1.1.0 // indirect
`)

	report, err := analyzeDependencyVersions(azdDir)
	if err != nil {
		t.Fatalf("analyze dependency versions: %v", err)
	}
	if len(report.Mismatches) != 0 {
		t.Fatalf("expected indirect requirement to be ignored, got %#v", report.Mismatches)
	}
}

func TestDependencyVersionReportsMismatch(t *testing.T) {
	azdDir := newDependencyVersionTestRepo(t, "v1.2.0", "v1.1.0", nil)

	var output bytes.Buffer
	err := checkDependencyVersions(azdDir, &output)
	if err == nil {
		t.Fatal("expected dependency version validation to fail")
	}
	if !strings.Contains(output.String(), "example.com/shared uses v1.1.0, but azd core requires v1.2.0") {
		t.Fatalf("unexpected output:\n%s", output.String())
	}
}

func TestDependencyVersionAllowsExactOverride(t *testing.T) {
	override := dependencyVersionOverride{
		Module:     "extensions/test.extension",
		Dependency: testSharedDependency,
		Version:    "v1.1.0",
		Reason:     "The extension temporarily requires the older API.",
		Issue:      "https://github.com/Azure/azure-dev/issues/9297",
	}
	azdDir := newDependencyVersionTestRepo(t, "v1.2.0", "v1.1.0", []dependencyVersionOverride{override})

	report, err := analyzeDependencyVersions(azdDir)
	if err != nil {
		t.Fatalf("analyze dependency versions: %v", err)
	}
	if len(report.Mismatches) != 0 || len(report.StaleOverrides) != 0 {
		t.Fatalf("expected override to allow mismatch, got %#v", report)
	}
}

func TestDependencyVersionReportsStaleOverride(t *testing.T) {
	override := dependencyVersionOverride{
		Module:     "extensions/test.extension",
		Dependency: testSharedDependency,
		Version:    "v1.1.0",
		Reason:     "The extension temporarily requires the older API.",
		Issue:      "https://github.com/Azure/azure-dev/issues/9297",
	}
	azdDir := newDependencyVersionTestRepo(t, "v1.2.0", "v1.2.0", []dependencyVersionOverride{override})

	report, err := analyzeDependencyVersions(azdDir)
	if err != nil {
		t.Fatalf("analyze dependency versions: %v", err)
	}
	if len(report.StaleOverrides) != 1 {
		t.Fatalf("expected one stale override, got %#v", report.StaleOverrides)
	}
}

func TestDependencyVersionRejectsInvalidOverrideIssue(t *testing.T) {
	override := dependencyVersionOverride{
		Module:     "extensions/test.extension",
		Dependency: testSharedDependency,
		Version:    "v1.1.0",
		Reason:     "The extension temporarily requires the older API.",
		Issue:      "https://example.com/issues/1",
	}
	azdDir := newDependencyVersionTestRepo(t, "v1.2.0", "v1.1.0", []dependencyVersionOverride{override})

	_, err := analyzeDependencyVersions(azdDir)
	if err == nil || !strings.Contains(err.Error(), "must link to an Azure/azure-dev issue") {
		t.Fatalf("expected invalid issue error, got %v", err)
	}
}

func TestDependencyVersionRejectsTrailingConfigContent(t *testing.T) {
	azdDir := newDependencyVersionTestRepo(t, "v1.2.0", "v1.2.0", nil)
	writeTestFile(
		t,
		filepath.Join(azdDir, dependencyVersionConfigFile),
		`{"overrides": []} {"overrides": []}`,
	)

	_, err := analyzeDependencyVersions(azdDir)
	if err == nil || !strings.Contains(err.Error(), "unexpected trailing content") {
		t.Fatalf("expected trailing content error, got %v", err)
	}
}

func TestDependencyVersionSyncsMismatch(t *testing.T) {
	azdDir := newDependencyVersionTestRepo(t, "v1.2.0", "v1.1.0", nil)
	sharedDir := filepath.Join(filepath.Dir(azdDir), "shared")
	writeTestFile(t, filepath.Join(sharedDir, "go.mod"), `module example.com/shared

go 1.26.0
`)
	writeTestFile(t, filepath.Join(sharedDir, "shared.go"), "package shared\n")

	extensionDir := filepath.Join(azdDir, "extensions", "test.extension")
	writeTestFile(t, filepath.Join(extensionDir, "main.go"), `package extension

import _ "example.com/shared"
`)
	writeTestFile(t, filepath.Join(extensionDir, "go.mod"), `module example.com/extension

go 1.26.0

require example.com/shared v1.1.0

replace example.com/shared => ../../../shared
`)

	var output bytes.Buffer
	if err := syncDependencyVersions(azdDir, &output); err != nil {
		t.Fatalf("sync dependency versions: %v\n%s", err, output.String())
	}

	mod, err := loadGoModFile(filepath.Join(extensionDir, "go.mod"))
	if err != nil {
		t.Fatalf("load synchronized go.mod: %v", err)
	}
	if len(mod.Require) != 1 || mod.Require[0].Version != "v1.2.0" {
		t.Fatalf("expected synchronized v1.2.0 requirement, got %#v", mod.Require)
	}
}

func newDependencyVersionTestRepo(
	t *testing.T,
	coreVersion string,
	extensionVersion string,
	overrides []dependencyVersionOverride,
) string {
	t.Helper()
	azdDir := filepath.Join(t.TempDir(), "cli", "azd")
	writeTestFile(t, filepath.Join(azdDir, "go.mod"), `module example.com/core

go 1.26.0

require example.com/shared `+coreVersion+`
`)
	writeTestFile(t, filepath.Join(azdDir, "extensions", "test.extension", "go.mod"), `module example.com/extension

go 1.26.0

require example.com/shared `+extensionVersion+`
`)

	config, err := json.Marshal(dependencyVersionConfig{Overrides: overrides})
	if err != nil {
		t.Fatalf("marshal dependency version config: %v", err)
	}
	writeTestFile(t, filepath.Join(azdDir, dependencyVersionConfigFile), string(config))
	return azdDir
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
