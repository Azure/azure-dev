// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteAgentDeployPreviewEffectiveChangesOnce(t *testing.T) {
	t.Parallel()
	result := &DirectDeployPreviewResult{
		Name: "agent", CurrentVersion: "2", Operation: "create_version", HasChanges: true,
		Changes: []DeployPreviewChangeGroup{
			{Group: "metadata", Changes: []DeployPreviewChange{
				{Field: "metadata.tags", Kind: "modify", Before: []any{"Old"}, After: []any{"Test"}},
			}},
			{Group: "environmentVariables", Changes: []DeployPreviewChange{
				{Field: "ANY_THING", Kind: "add", After: "[redacted]", Sensitive: true},
				{Field: "CHANGE", Kind: "modify", Before: "[redacted]", After: "[redacted]", Sensitive: true},
				{Field: "REMOVE", Kind: "remove", Before: "[redacted]", Sensitive: true},
			}},
		},
		SourceConflicts: []DeployPreviewSourceConflict{{
			Source: "agent.manifest.yaml",
			Differences: []DeployPreviewChangeGroup{
				{Group: "metadata", Changes: []DeployPreviewChange{
					{Field: "metadata.tags", Kind: "modify", Before: []any{"Test"}, After: []any{"Old"}},
				}},
				{Group: "environmentVariables", Changes: []DeployPreviewChange{
					{Field: "CHANGE", Kind: "modify", Before: "[redacted]", After: "[redacted]", Sensitive: true},
				}},
			},
		}},
		Image: &DeployPreviewImage{Mode: "code", Known: true},
	}
	var out bytes.Buffer
	require.NoError(t, WriteDeploymentPreview(&out, result))
	for _, name := range []string{"metadata.tags", "ANY_THING", "CHANGE", "REMOVE"} {
		assert.Equal(t, 1, strings.Count(out.String(), name), "%s must appear once in the remote change list", name)
	}
	assert.Contains(t, out.String(), "+ ANY_THING")
	assert.Contains(t, out.String(), "~ CHANGE")
	assert.Contains(t, out.String(), "- REMOVE")
	assert.NotContains(t, out.String(), "Container image plan")
	assert.NotContains(t, out.String(), "This source compared with the remote")
	assert.Contains(t, out.String(), "agent.manifest.yaml")
}

func TestWriteAgentDeployPreviewImageVisibility(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		image   *DeployPreviewImage
		changes []DeployPreviewChangeGroup
		visible bool
	}{
		{name: "code no image change", image: &DeployPreviewImage{Mode: "code", Known: true}},
		{name: "unchanged prebuilt", image: &DeployPreviewImage{
			Mode: "prebuilt", Known: true, Image: "registry.example.com/agent:v1",
		}},
		{name: "build with known image", image: &DeployPreviewImage{
			Mode: "build", Build: true, Push: true, Known: true, Image: "registry.example.com/agent:v1",
		}, visible: true},
		{name: "pending build", image: &DeployPreviewImage{Mode: "build", Build: true, Push: true}, visible: true},
		{name: "changed prebuilt", image: &DeployPreviewImage{
			Mode: "prebuilt", Known: true, Image: "registry.example.com/agent:v2",
		}, changes: []DeployPreviewChangeGroup{{Group: "containerImage", Changes: []DeployPreviewChange{
			{Field: "image", Kind: "modify", Before: "registry.example.com/agent:v1", After: "registry.example.com/agent:v2"},
		}}}, visible: true},
		{name: "unknown prebuilt", image: &DeployPreviewImage{Mode: "prebuilt"}, visible: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := &DirectDeployPreviewResult{Name: "agent", Image: tt.image, Changes: tt.changes}
			var out bytes.Buffer
			require.NoError(t, WriteDeploymentPreview(&out, result))
			assert.Equal(t, tt.visible, strings.Contains(out.String(), "Container image plan"))
			out.Reset()
			data, err := json.Marshal(result)
			require.NoError(t, err)
			var decoded DirectDeployPreviewResult
			require.NoError(t, json.Unmarshal(data, &decoded))
			assert.Equal(t, tt.image, decoded.Image, "JSON must retain structured image planning data")
		})
	}
}
