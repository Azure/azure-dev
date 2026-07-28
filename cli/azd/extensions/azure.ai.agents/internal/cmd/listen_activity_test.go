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
