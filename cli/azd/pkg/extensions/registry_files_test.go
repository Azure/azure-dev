// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// collectValidationErrors returns only the error-level issues from a registry
// validation result. Extension-level errors are prefixed with the owning extension
// id; registry-level errors (which have no owning extension) are included as-is.
// Warnings are intentionally excluded so callers can assert on hard failures only.
func collectValidationErrors(result *RegistryValidationResult) []string {
	var errs []string
	for _, issue := range result.Issues {
		if issue.Severity == ValidationError {
			errs = append(errs, issue.Message)
		}
	}
	for _, ext := range result.Extensions {
		for _, issue := range ext.Issues {
			if issue.Severity == ValidationError {
				errs = append(errs, fmt.Sprintf("[%s] %s", ext.Id, issue.Message))
			}
		}
	}
	return errs
}

func TestDevRegistryFileIsValid(t *testing.T) {
	registryPath := filepath.Join("..", "..", "extensions", "registry.dev.json")
	data, err := os.ReadFile(registryPath)
	require.NoError(t, err)

	var registry Registry
	require.NoError(t, json.Unmarshal(data, &registry))
	require.Equal(t, CurrentRegistrySchemaVersion, registry.SchemaVersion)

	result := ValidateRegistry(&registry, false)
	require.True(t, result.Valid, "registry.dev.json failed validation: %+v", result)
}

// TestRegistryFileIsValid gates the production registry on every pull request that
// touches registry.json (via the ext-registry-ci workflow) and on the merged main
// branch. It fails only on validation errors (never warnings), so it blocks a
// registry update only when a declared dependency constraint can never be
// satisfied by a published version, e.g. a microsoft.foundry meta-package pin or an
// azure.ai.* child dependency left dangling by a coordinated multi-extension bump.
// A dependency whose id is absent from the registry is a warning (it may come from
// another source) and does not fail the gate.
func TestRegistryFileIsValid(t *testing.T) {
	registryPath := filepath.Join("..", "..", "extensions", "registry.json")
	data, err := os.ReadFile(registryPath)
	require.NoError(t, err)

	var registry Registry
	require.NoError(t, json.Unmarshal(data, &registry))

	result := ValidateRegistry(&registry, false)
	if !result.Valid {
		errs := collectValidationErrors(result)
		t.Fatalf("registry.json failed validation with %d error(s):\n  - %s",
			len(errs), strings.Join(errs, "\n  - "))
	}
}
