// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// appendDeployWarning adds text to the note of the last artifact, so it shows in the deploy output
// instead of subprocess stderr, which the host discards. Activity-only agents produce no artifacts,
// so a resource artifact for the component carries the warning instead.
func appendDeployWarning(result *azdext.ServiceDeployResult, componentID, warning string) {
	if result == nil || strings.TrimSpace(warning) == "" {
		return
	}
	if len(result.Artifacts) == 0 && componentID != "" {
		result.Artifacts = append(result.Artifacts, &azdext.Artifact{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_RESOURCE,
			Location:     componentID,
			LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
			Metadata: map[string]string{
				"label":     "Application Insights",
				"clickable": "false",
			},
		})
	}
	for i := len(result.Artifacts) - 1; i >= 0; i-- {
		artifact := result.Artifacts[i]
		if artifact == nil {
			continue
		}
		if artifact.Metadata == nil {
			artifact.Metadata = map[string]string{}
		}
		if existing := artifact.Metadata["note"]; existing != "" {
			artifact.Metadata["note"] = existing + "\n\nWarning: " + warning
		} else {
			artifact.Metadata["note"] = "Warning: " + warning
		}
		return
	}
}
