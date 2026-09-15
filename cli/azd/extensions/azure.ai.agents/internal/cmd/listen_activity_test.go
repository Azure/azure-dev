// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestActivityBotTeardownTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		persistedBotName       string
		persistedResourceGroup string
		persistedOwned         string
		wantBotName            string
		wantResourceGroup      string
		wantTracked            bool
	}{
		{
			name:                   "uses persisted deployment target",
			persistedBotName:       "adopted-bot",
			persistedResourceGroup: "adopted-rg",
		},
		{
			name:                   "uses azd-owned deployment target",
			persistedBotName:       "owned-bot",
			persistedResourceGroup: "owned-rg",
			persistedOwned:         "true",
			wantBotName:            "owned-bot",
			wantResourceGroup:      "owned-rg",
			wantTracked:            true,
		},
		{
			name: "does not delete bot without ownership marker",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			botName, resourceGroup, tracked := activityBotTeardownTarget(
				test.persistedBotName,
				test.persistedResourceGroup,
				test.persistedOwned,
			)

			require.Equal(t, test.wantBotName, botName)
			require.Equal(t, test.wantResourceGroup, resourceGroup)
			require.Equal(t, test.wantTracked, tracked)
		})
	}
}

func TestResolveServiceActivityProfileUsesConfiguredDigitalWorkerType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		activity map[string]any
		want     project.ActivityUseCase
	}{
		{
			name: "simple is the default",
			want: project.ActivityUseCaseSimple,
		},
		{
			name: "digital worker skips simple flow",
			activity: map[string]any{
				"digitalWorkerType": "m365",
				"publish": map[string]any{
					"publishScope": "tenant",
				},
			},
			want: project.ActivityUseCaseDigitalWorker,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			values := map[string]any{
				"kind": "hosted",
				"name": "activity-agent",
				"protocols": []any{
					map[string]any{"protocol": "activity", "version": "2.0.0"},
				},
			}
			if test.activity != nil {
				values["activity"] = test.activity
			}
			props, err := structpb.NewStruct(values)
			require.NoError(t, err)

			profile, err := resolveServiceActivityProfile(&azdext.ServiceConfig{
				Name:                 "activity-agent",
				Host:                 AiAgentHost,
				AdditionalProperties: props,
			}, t.TempDir())
			require.NoError(t, err)
			require.True(t, profile.IsActivity)
			require.Equal(t, test.want, profile.UseCase)
		})
	}
}

func TestResolveServiceActivityProfileUsesConfiguredDigitalWorkerTypeFromFileRef(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	definitionPath := filepath.Join(dir, "agent.yaml")
	referenced := strings.Join([]string{
		"kind: hosted",
		"name: activity-agent",
		"protocols:",
		"  - protocol: activity",
		"    version: 2.0.0",
		"activity:",
		"  digitalWorkerType: m365",
		"  publish:",
		"    publishScope: tenant",
	}, "\n")
	require.NoError(t, os.WriteFile(definitionPath, []byte(referenced), 0600))

	props, err := structpb.NewStruct(map[string]any{"$ref": "./agent.yaml"})
	require.NoError(t, err)

	profile, err := resolveServiceActivityProfile(&azdext.ServiceConfig{
		Name:                 "activity-agent",
		Host:                 AiAgentHost,
		AdditionalProperties: props,
	}, dir)
	require.NoError(t, err)
	require.True(t, profile.IsActivity)
	require.Equal(t, project.ActivityUseCaseDigitalWorker, profile.UseCase)
}

func TestShouldProvisionActivityBotUsesCanonicalUseCaseResolution(t *testing.T) {
	t.Parallel()

	t.Run("simple activity is provisioned", func(t *testing.T) {
		t.Parallel()

		shouldProvision, err := shouldProvisionActivityBot(&azdext.ServiceConfig{
			Name: "activity-agent",
			Host: AiAgentHost,
			AdditionalProperties: mustStruct(t, map[string]any{
				"kind":      "hosted",
				"name":      "activity-agent",
				"protocols": []any{map[string]any{"protocol": "activity", "version": "2.0.0"}},
			}),
		}, t.TempDir())
		require.NoError(t, err)
		require.True(t, shouldProvision)
	})

	t.Run("digital worker skips the legacy bot provisioning path", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		definitionPath := filepath.Join(dir, "agent.yaml")
		definition := strings.Join([]string{
			"kind: hosted",
			"name: activity-agent",
			"protocols:",
			"  - protocol: activity",
			"    version: 2.0.0",
			"activity:",
			"  digitalWorkerType: m365",
			"  publish:",
			"    publishScope: tenant",
		}, "\n")
		require.NoError(t, os.WriteFile(definitionPath, []byte(definition), 0600))

		shouldProvision, err := shouldProvisionActivityBot(&azdext.ServiceConfig{
			Name:                 "activity-agent",
			Host:                 AiAgentHost,
			AdditionalProperties: mustStruct(t, map[string]any{"$ref": "./agent.yaml"}),
		}, dir)
		require.NoError(t, err)
		require.False(t, shouldProvision)
	})
}

func mustStruct(t *testing.T, value map[string]any) *structpb.Struct {
	t.Helper()
	st, err := structpb.NewStruct(value)
	require.NoError(t, err)
	return st
}

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

func TestNoTeamsSetupGuideCreated(t *testing.T) {
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

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read setup guide %q: %v", path, err)
	}
	if !strings.Contains(string(contents), "echo-agent-bot-uai") {
		t.Fatalf("setup guide must contain the bot name; got:\n%s", contents)
	}
}

func TestWriteTeamsSetupGuide_SkipsSharedAgentSourceDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o750); err != nil {
		t.Fatal(err)
	}
	svc := &azdext.ServiceConfig{Name: "agent-a", Host: AiAgentHost, RelativePath: "src"}
	proj := &azdext.ProjectConfig{
		Path: root,
		Services: map[string]*azdext.ServiceConfig{
			"agent-a": svc,
			"agent-b": {Name: "agent-b", Host: AiAgentHost, RelativePath: "src"},
		},
	}

	path := writeTeamsSetupGuide(proj, svc, "agent-a", "bot-a", "app-id", "")

	if path != "" {
		t.Errorf("guide path = %q, want empty for shared source directory", path)
	}
	if _, err := os.Stat(filepath.Join(root, "src", teamsSetupGuideFile)); !os.IsNotExist(err) {
		t.Errorf("setup guide must not be written for a shared source directory")
	}
}

func TestWriteTeamsAppPackage_SkipsSharedAgentSourceDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o750); err != nil {
		t.Fatal(err)
	}
	svc := &azdext.ServiceConfig{Name: "agent-a", Host: AiAgentHost, RelativePath: "src"}
	proj := &azdext.ProjectConfig{
		Path: root,
		Services: map[string]*azdext.ServiceConfig{
			"agent-a": svc,
			"agent-b": {Name: "agent-b", Host: AiAgentHost, RelativePath: "src"},
		},
	}

	path := writeTeamsAppPackage(t.Context(), nil, proj, svc, "agent-a", "sub", "rg", "bot-a")

	if path != "" {
		t.Errorf("package path = %q, want empty for shared source directory", path)
	}
	if _, err := os.Stat(filepath.Join(root, "src", teamsAppPackageFile)); !os.IsNotExist(err) {
		t.Errorf("Teams app package must not be written for a shared source directory")
	}
}

func TestWarnLegacySimpleTeamsArtifactsForDigitalWorker(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o750); err != nil {
		t.Fatal(err)
	}
	svc := &azdext.ServiceConfig{Name: "agent-a", Host: AiAgentHost, RelativePath: "src"}
	proj := &azdext.ProjectConfig{Path: root, Services: map[string]*azdext.ServiceConfig{"agent-a": svc}}

	for _, name := range []string{teamsAppPackageFile, teamsAppPackageMarkerFile, teamsSetupGuideFile} {
		require.NoError(t, os.WriteFile(filepath.Join(root, "src", name), []byte("legacy"), 0o600))
	}

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()

	warnLegacySimpleTeamsArtifacts(proj, svc)
	_ = w.Close()
	out, err := io.ReadAll(r)
	require.NoError(t, err)

	text := string(out)
	require.Contains(t, text, "digital worker")
	require.Contains(t, text, teamsAppPackageFile)
	require.Contains(t, text, teamsSetupGuideFile)
	require.Contains(t, text, "review and remove them manually")
}

func TestHasSharedTeamsArtifactDestination(t *testing.T) {
	sharedSvc := &azdext.ServiceConfig{Name: "agent-a", Host: AiAgentHost, RelativePath: "."}
	proj := &azdext.ProjectConfig{
		Services: map[string]*azdext.ServiceConfig{
			"agent-a": sharedSvc,
			"agent-b": {Name: "agent-b", Host: AiAgentHost, RelativePath: ""},
			"web":     {Name: "web", Host: "containerapp", RelativePath: "."},
		},
	}

	if !hasSharedTeamsArtifactDestination(proj, sharedSvc) {
		t.Fatal("expected shared artifact destination for agent services with the same source directory")
	}

	uniqueSvc := &azdext.ServiceConfig{Name: "agent-c", Host: AiAgentHost, RelativePath: "src"}
	proj.Services["agent-c"] = uniqueSvc
	if hasSharedTeamsArtifactDestination(proj, uniqueSvc) {
		t.Fatal("unique agent source directory must not be treated as shared")
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
	if !teamsAppPackageIsOwned(pkg, marker) {
		t.Errorf("ownership marker must be written")
	}
}

func TestCommitTeamsAppPackage_RollsBackNewPackageWhenMarkerWriteFails(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, teamsAppPackageFile)
	marker := filepath.Join(dir, "missing", teamsAppPackageMarkerFile)

	got, err := commitTeamsAppPackage(pkg, marker, []byte("AZD-GENERATED"))
	if err == nil {
		t.Fatal("expected marker write error")
	}
	if got != "" {
		t.Errorf("path = %q, want empty path on failure", got)
	}
	if _, err := os.Stat(pkg); !os.IsNotExist(err) {
		t.Errorf("new package must be rolled back when marker write fails")
	}
}

func TestCommitTeamsAppPackage_RestoresOwnedPackageWhenMarkerWriteFails(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, teamsAppPackageFile)
	marker := filepath.Join(dir, teamsAppPackageMarkerFile)
	if err := os.WriteFile(pkg, []byte("OLD"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(teamsAppPackageMarkerContent([]byte("OLD"))), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWriteMarker := writeTeamsAppMarkerAtomically
	writeTeamsAppMarkerAtomically = func(string, []byte) error {
		return errors.New("marker write failed")
	}
	t.Cleanup(func() {
		writeTeamsAppMarkerAtomically = oldWriteMarker
	})

	got, err := commitTeamsAppPackage(pkg, marker, []byte("NEW"))
	if err == nil {
		t.Fatal("expected marker write error")
	}
	if got != "" {
		t.Errorf("path = %q, want empty path on failure", got)
	}
	data, _ := os.ReadFile(pkg)
	if string(data) != "OLD" {
		t.Errorf("owned package must be restored after marker write fails; content = %q", string(data))
	}
	if !teamsAppPackageIsOwned(pkg, marker) {
		t.Errorf("restored package must still match the ownership marker")
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

	if teamsAppPackageIsOwned(filepath.Join(dir, teamsAppPackageFile), marker) {
		t.Fatal("symlink marker must not mark a package as azd-owned")
	}
}

func TestTeamsAppPackageIsOwned_RejectsLegacyMarkerWithoutDigest(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, teamsAppPackageFile)
	marker := filepath.Join(dir, teamsAppPackageMarkerFile)
	if err := os.WriteFile(pkg, []byte("AZD-GENERATED"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("generated by azd\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if teamsAppPackageIsOwned(pkg, marker) {
		t.Fatal("legacy marker without digest must not mark a package as azd-owned")
	}
}

func TestCommitTeamsAppPackage_PreservesCustomizedGeneratedPackage(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, teamsAppPackageFile)
	marker := filepath.Join(dir, teamsAppPackageMarkerFile)
	if err := os.WriteFile(pkg, []byte("AZD-GENERATED-CUSTOMIZED"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(teamsAppPackageMarkerContent([]byte("AZD-GENERATED"))), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := commitTeamsAppPackage(pkg, marker, []byte("NEW"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty path when leaving a customized package, got %q", got)
	}
	data, _ := os.ReadFile(pkg)
	if string(data) != "AZD-GENERATED-CUSTOMIZED" {
		t.Errorf("customized package was clobbered; content = %q", string(data))
	}
}

func TestCommitTeamsAppPackage_OverwritesOwned(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, teamsAppPackageFile)
	marker := filepath.Join(dir, teamsAppPackageMarkerFile)
	if err := os.WriteFile(pkg, []byte("OLD"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(teamsAppPackageMarkerContent([]byte("OLD"))), 0o600); err != nil {
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
	if !teamsAppPackageIsOwned(pkg, marker) {
		t.Errorf("marker must be updated to the new package digest")
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
	if err := os.WriteFile(marker, []byte(teamsAppPackageMarkerContent([]byte("AZD-GENERATED"))), 0o600); err != nil {
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

func TestRemoveOwnedTeamsAppPackage_KeepsMarkerWhenPackageRemoveFails(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, teamsAppPackageFile)
	marker := filepath.Join(dir, teamsAppPackageMarkerFile)
	if err := os.WriteFile(pkg, []byte("AZD-GENERATED"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(teamsAppPackageMarkerContent([]byte("AZD-GENERATED"))), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRemove := removeTeamsAppFile
	removeTeamsAppFile = func(path string) error {
		if path == pkg {
			return errors.New("package remove failed")
		}
		return oldRemove(path)
	}
	t.Cleanup(func() {
		removeTeamsAppFile = oldRemove
	})

	removeOwnedTeamsAppPackage(pkg, marker)

	if _, err := os.Stat(pkg); err != nil {
		t.Errorf("package must remain when removal fails: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker must remain when package removal fails: %v", err)
	}
}

func TestRemoveOwnedTeamsAppPackage_PreservesCustomizedGeneratedPackage(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, teamsAppPackageFile)
	marker := filepath.Join(dir, teamsAppPackageMarkerFile)
	if err := os.WriteFile(pkg, []byte("AZD-GENERATED-CUSTOMIZED"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(teamsAppPackageMarkerContent([]byte("AZD-GENERATED"))), 0o600); err != nil {
		t.Fatal(err)
	}

	removeOwnedTeamsAppPackage(pkg, marker)

	if _, err := os.Stat(pkg); err != nil {
		t.Errorf("customized package must be preserved: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker for customized package must be preserved: %v", err)
	}
}
