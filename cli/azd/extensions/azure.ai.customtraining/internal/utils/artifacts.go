// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"strings"

	"azure.ai.customtraining/pkg/models"
)

// CollectArtifactPrefixes extracts unique first-level folder prefixes from artifact paths.
// Job artifacts have two top-level folders: "outputs/" and "user_logs/".
// This yields at most 2 prefix/contentinfo API calls.
func CollectArtifactPrefixes(artifacts []models.Artifact) []string {
	seen := make(map[string]bool)
	var prefixes []string

	for _, a := range artifacts {
		parts := strings.SplitN(a.Path, "/", 2)
		prefix := parts[0] + "/"

		if !seen[prefix] {
			seen[prefix] = true
			prefixes = append(prefixes, prefix)
		}
	}

	return prefixes
}
