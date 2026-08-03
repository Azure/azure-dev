// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestTeamsSetupGuideContent(t *testing.T) {
	const msaAppID = "11111111-2222-3333-4444-555555555555"

	// Fallback (no generated package): the guide must give the manual packaging
	// steps and carry the bot id verbatim into the sample manifest.
	manual := teamsSetupGuideContent("echo-agent", "echo-agent-bot-uai", msaAppID, "")

	// The bot id is the one value the user must not get wrong: it has to be
	// carried verbatim into the Teams manifest bots[].botId.
	if !strings.Contains(manual, `"botId": "`+msaAppID+`"`) {
		t.Fatalf("guide must set bots[].botId to the msaAppId; got:\n%s", manual)
	}

	// The guide must point at the official Microsoft Learn docs, not any
	// sample-specific script.
	for _, link := range []string{
		"learn.microsoft.com/microsoftteams/platform/concepts/build-and-test/apps-package",
		"learn.microsoft.com/microsoftteams/platform/concepts/deploy-and-publish/apps-upload",
		"dev.teams.microsoft.com/apps",
	} {
		if !strings.Contains(manual, link) {
			t.Errorf("guide missing official doc link %q", link)
		}
	}
	// The guide must give the concrete sideload step, not just link out.
	if !strings.Contains(manual, "Upload a custom app") {
		t.Errorf("guide missing the concrete sideload step")
	}
	if strings.Contains(manual, "package-teams-app.ps1") {
		t.Errorf("guide must not reference sample-specific scripts")
	}

	// Generated-package path: the guide must lead with sideloading the generated
	// package (by name) and must NOT ask the user to build a manifest by hand.
	const pkg = "appPackage.zip"
	generated := teamsSetupGuideContent("echo-agent", "echo-agent-bot-uai", msaAppID, pkg)
	if !strings.Contains(generated, pkg) {
		t.Errorf("generated-package guide must reference %q", pkg)
	}
	if !strings.Contains(generated, "Upload a custom app") {
		t.Errorf("generated-package guide missing the concrete sideload step")
	}
	if !strings.Contains(generated, "--scope Personal") {
		t.Errorf("generated-package guide missing the atk sideload command")
	}
	if strings.Contains(generated, "REPLACE-WITH-A-NEW-GUID") {
		t.Errorf("generated-package guide must not include the manual manifest template")
	}
}

func TestWriteTeamsSetupGuide(t *testing.T) {
	root := t.TempDir()
	proj := &azdext.ProjectConfig{Path: root}
	svc := &azdext.ServiceConfig{Name: "echo-agent", RelativePath: "src"}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o750); err != nil {
		t.Fatal(err)
	}

	path := writeTeamsSetupGuide(proj, svc, "echo-agent", "echo-agent-bot-uai", "app-id", "")
	want := filepath.Join(root, "src", teamsSetupGuideFile)
	if path != want {
		t.Fatalf("guide path = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("guide not written: %v", err)
	}
	if !strings.Contains(string(data), "app-id") {
		t.Errorf("written guide missing bot id")
	}
}

func TestCommitTeamsAppPackage_PreservesUnownedFile(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, teamsAppPackageFile)
	marker := filepath.Join(dir, teamsAppPackageMarkerFile)
	if err := os.WriteFile(pkg, []byte("USER-OWNED"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := commitTeamsAppPackage(pkg, marker, []byte("AZD-GENERATED"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty path when leaving an unowned file, got %q", got)
	}
	data, _ := os.ReadFile(pkg)
	if string(data) != "USER-OWNED" {
		t.Errorf("user file was clobbered; content = %q", string(data))
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("marker must not be created for an unowned file")
	}
}

func TestCommitTeamsAppPackage_WritesWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, teamsAppPackageFile)
	marker := filepath.Join(dir, teamsAppPackageMarkerFile)

	got, err := commitTeamsAppPackage(pkg, marker, []byte("AZD-GENERATED"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != pkg {
		t.Errorf("path = %q, want %q", got, pkg)
	}
	data, _ := os.ReadFile(pkg)
	if string(data) != "AZD-GENERATED" {
		t.Errorf("package content = %q", string(data))
	}
	if !teamsAppPackageIsOwned(marker) {
		t.Errorf("ownership marker must be written")
	}
}

func TestCommitTeamsAppPackage_DoesNotUseFixedTempPath(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, teamsAppPackageFile)
	marker := filepath.Join(dir, teamsAppPackageMarkerFile)
	fixedTemp := pkg + ".tmp"
	if err := os.WriteFile(fixedTemp, []byte("DO-NOT-CLOBBER"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := commitTeamsAppPackage(pkg, marker, []byte("AZD-GENERATED"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != pkg {
		t.Errorf("path = %q, want %q", got, pkg)
	}
	data, _ := os.ReadFile(pkg)
	if string(data) != "AZD-GENERATED" {
		t.Errorf("package content = %q", string(data))
	}
	tempData, _ := os.ReadFile(fixedTemp)
	if string(tempData) != "DO-NOT-CLOBBER" {
		t.Errorf("fixed temp path was clobbered; content = %q", string(tempData))
	}
}

func TestCommitTeamsAppPackage_DoesNotUseFixedMarkerTempPath(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, teamsAppPackageFile)
	marker := filepath.Join(dir, teamsAppPackageMarkerFile)
	fixedTemp := marker + ".tmp"
	if err := os.WriteFile(fixedTemp, []byte("DO-NOT-CLOBBER"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := commitTeamsAppPackage(pkg, marker, []byte("AZD-GENERATED"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tempData, _ := os.ReadFile(fixedTemp)
	if string(tempData) != "DO-NOT-CLOBBER" {
		t.Errorf("fixed marker temp path was clobbered; content = %q", string(tempData))
	}
}

func TestTeamsAppPackageIsOwned_RejectsSymlinkMarker(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	marker := filepath.Join(dir, teamsAppPackageMarkerFile)
	if err := os.WriteFile(target, []byte("not an azd marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, marker); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}

	if teamsAppPackageIsOwned(marker) {
		t.Fatal("symlink marker must not mark a package as azd-owned")
	}
}

func TestCommitTeamsAppPackage_OverwritesOwned(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, teamsAppPackageFile)
	marker := filepath.Join(dir, teamsAppPackageMarkerFile)
	if err := os.WriteFile(pkg, []byte("OLD"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("generated by azd\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := commitTeamsAppPackage(pkg, marker, []byte("NEW"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != pkg {
		t.Errorf("path = %q, want %q", got, pkg)
	}
	data, _ := os.ReadFile(pkg)
	if string(data) != "NEW" {
		t.Errorf("owned package should be overwritten; content = %q", string(data))
	}
}

func TestRemoveOwnedTeamsAppPackage_PreservesUnowned(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, teamsAppPackageFile)
	marker := filepath.Join(dir, teamsAppPackageMarkerFile)
	if err := os.WriteFile(pkg, []byte("USER-OWNED"), 0o600); err != nil {
		t.Fatal(err)
	}

	removeOwnedTeamsAppPackage(pkg, marker)

	if _, err := os.Stat(pkg); err != nil {
		t.Errorf("unowned package must be preserved: %v", err)
	}
}

func TestRemoveOwnedTeamsAppPackage_RemovesOwned(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, teamsAppPackageFile)
	marker := filepath.Join(dir, teamsAppPackageMarkerFile)
	if err := os.WriteFile(pkg, []byte("AZD-GENERATED"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("generated by azd\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	removeOwnedTeamsAppPackage(pkg, marker)

	if _, err := os.Stat(pkg); !os.IsNotExist(err) {
		t.Errorf("owned package must be removed")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("marker must be removed")
	}
}
