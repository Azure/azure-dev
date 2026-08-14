// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
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

func TestTeamsSetupGuideContent(t *testing.T) {
	const msaAppID = "11111111-2222-3333-4444-555555555555"
	content := teamsSetupGuideContent("echo-agent", "echo-agent-bot-uai", msaAppID)

	// The bot id is the one value the user must not get wrong: it has to be
	// carried verbatim into the Teams manifest bots[].botId.
	if !strings.Contains(content, `"botId": "`+msaAppID+`"`) {
		t.Fatalf("guide must set bots[].botId to the msaAppId; got:\n%s", content)
	}

	// The guide must point at the official Microsoft Learn docs, not any
	// sample-specific script.
	for _, link := range []string{
		"learn.microsoft.com/microsoftteams/platform/concepts/build-and-test/apps-package",
		"learn.microsoft.com/microsoftteams/platform/concepts/deploy-and-publish/apps-upload",
		"dev.teams.microsoft.com/apps",
	} {
		if !strings.Contains(content, link) {
			t.Errorf("guide missing official doc link %q", link)
		}
	}
	// The guide must give the concrete sideload step, not just link out.
	if !strings.Contains(content, "Upload a custom app") {
		t.Errorf("guide missing the concrete sideload step")
	}
	if strings.Contains(content, "package-teams-app.ps1") {
		t.Errorf("guide must not reference sample-specific scripts")
	}
}

func TestNoTeamsSetupGuideCreated(t *testing.T) {
	root := t.TempDir()
	proj := &azdext.ProjectConfig{Path: root}
	svc := &azdext.ServiceConfig{Name: "echo-agent", RelativePath: "src"}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o750); err != nil {
		t.Fatal(err)
	}

	path := writeTeamsSetupGuide(proj, svc, "echo-agent", "echo-agent-bot-uai", "app-id")
	if path != "" {
		t.Fatalf("expected no guide path, got %q", path)
	}

	guidePath := filepath.Join(root, "src", teamsSetupGuideFile)
	if _, err := os.Stat(guidePath); !os.IsNotExist(err) {
		t.Fatalf("guide should not be created at %q: err=%v", guidePath, err)
	}
}
