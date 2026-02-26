// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// validArtifacts creates a minimal valid artifact set for tests.
func validArtifacts() map[string]ExtensionArtifact {
	return map[string]ExtensionArtifact{
		"linux/amd64": {
			URL:      "https://example.com/ext-linux-amd64",
			Checksum: ExtensionChecksum{Algorithm: "sha256", Value: "abc123"},
		},
	}
}

func TestValidateExtensions_ValidRegistry(t *testing.T) {
	exts := []*ExtensionMetadata{
		{
			Id:          "publisher.extension",
			DisplayName: "Test Extension",
			Description: "A test extension",
			Versions: []ExtensionVersion{
				{
					Version:      "1.0.0",
					Capabilities: []CapabilityType{CustomCommandCapability},
					Artifacts: map[string]ExtensionArtifact{
						"linux/amd64": {
							URL:      "https://example.com/ext-linux-amd64",
							Checksum: ExtensionChecksum{Algorithm: "sha256", Value: "abc123"},
						},
						"darwin/arm64": {
							URL:      "https://example.com/ext-darwin-arm64",
							Checksum: ExtensionChecksum{Algorithm: "sha256", Value: "def456"},
						},
					},
				},
			},
		},
	}

	result := ValidateExtensions(exts, false)
	require.True(t, result.Valid)
	require.Len(t, result.Extensions, 1)
	require.True(t, result.Extensions[0].Valid)
	require.Empty(t, result.Extensions[0].Issues)
}

func TestValidateExtensions_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name     string
		ext      ExtensionMetadata
		expected []string
	}{
		{
			name: "missing id",
			ext: ExtensionMetadata{
				DisplayName: "Test", Description: "Test",
				Versions: []ExtensionVersion{{Version: "1.0.0", Artifacts: validArtifacts()}},
			},
			expected: []string{"missing or empty required field 'id'"},
		},
		{
			name: "missing displayName",
			ext: ExtensionMetadata{
				Id: "pub.ext", Description: "Test",
				Versions: []ExtensionVersion{{Version: "1.0.0", Artifacts: validArtifacts()}},
			},
			expected: []string{"missing or empty required field 'displayName'"},
		},
		{
			name: "missing description",
			ext: ExtensionMetadata{
				Id: "pub.ext", DisplayName: "Test",
				Versions: []ExtensionVersion{{Version: "1.0.0", Artifacts: validArtifacts()}},
			},
			expected: []string{"missing or empty required field 'description'"},
		},
		{
			name:     "missing versions",
			ext:      ExtensionMetadata{Id: "pub.ext", DisplayName: "Test", Description: "Test"},
			expected: []string{"missing or empty required field 'versions'"},
		},
		{
			name: "empty versions",
			ext: ExtensionMetadata{
				Id: "pub.ext", DisplayName: "Test", Description: "Test",
				Versions: []ExtensionVersion{},
			},
			expected: []string{"missing or empty required field 'versions'"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateExtensions([]*ExtensionMetadata{&tt.ext}, false)
			require.False(t, result.Valid)
			require.False(t, result.Extensions[0].Valid)

			for _, expectedMsg := range tt.expected {
				found := false
				for _, issue := range result.Extensions[0].Issues {
					if issue.Message == expectedMsg && issue.Severity == ValidationError {
						found = true
						break
					}
				}
				require.True(t, found, "expected error message not found: %s", expectedMsg)
			}
		})
	}
}

func TestValidateExtension_InvalidExtensionId(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		valid bool
	}{
		{"valid two segments", "publisher.extension", true},
		{"valid three segments", "publisher.category.extension", true},
		{"valid with hyphens", "my-publisher.my-extension", true},
		{"invalid single segment", "extension", false},
		{"invalid starts with dot", ".extension", false},
		{"invalid ends with dot", "extension.", false},
		{"invalid double dots", "publisher..extension", false},
		{"invalid special chars", "publisher.ext@nsion", false},
		{"invalid spaces", "publisher.ext ension", false},
		{"invalid uppercase", "Publisher.Extension", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ext := &ExtensionMetadata{
				Id:          tt.id,
				DisplayName: "Test",
				Description: "Test",
				Versions:    []ExtensionVersion{{Version: "1.0.0", Artifacts: validArtifacts()}},
			}

			result := validateExtension(ext, false)
			if tt.valid {
				hasIdError := false
				for _, issue := range result.Issues {
					if issue.Severity == ValidationError &&
						(issue.Message == "missing or empty required field 'id'" ||
							len(issue.Message) > 20 && issue.Message[:20] == "invalid extension ID") {
						hasIdError = true
					}
				}
				require.False(t, hasIdError, "unexpected ID validation error for '%s'", tt.id)
			} else {
				require.False(t, result.Valid, "expected validation to fail for ID '%s'", tt.id)
			}
		})
	}
}

func TestValidateExtension_InvalidSemver(t *testing.T) {
	tests := []struct {
		name    string
		version string
		valid   bool
	}{
		{"valid basic", "1.0.0", true},
		{"valid with prerelease", "1.0.0-beta.1", true},
		{"valid with alpha", "2.3.4-alpha", true},
		{"valid with build metadata", "1.0.0+build", true},
		{"invalid missing patch", "1.0", false},
		{"invalid with v prefix", "v1.0.0", false},
		{"invalid text", "latest", false},
		{"empty version", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ext := &ExtensionMetadata{
				Id:          "pub.ext",
				DisplayName: "Test",
				Description: "Test",
				Versions:    []ExtensionVersion{{Version: tt.version, Artifacts: validArtifacts()}},
			}

			result := validateExtension(ext, false)
			hasVersionError := false
			for _, issue := range result.Issues {
				if issue.Severity == ValidationError &&
					(strings.Contains(issue.Message, "semver") || strings.Contains(issue.Message, "'version'")) {
					hasVersionError = true
				}
			}

			if tt.valid {
				require.False(t, hasVersionError, "unexpected version error for '%s'", tt.version)
			} else {
				require.True(t, hasVersionError, "expected version error for '%s'", tt.version)
			}
		})
	}
}

func TestValidateExtension_InvalidCapabilities(t *testing.T) {
	ext := &ExtensionMetadata{
		Id:          "pub.ext",
		DisplayName: "Test",
		Description: "Test",
		Versions: []ExtensionVersion{
			{
				Version:      "1.0.0",
				Capabilities: []CapabilityType{"custom-commands", "invalid-capability"},
				Artifacts:    validArtifacts(),
			},
		},
	}

	result := validateExtension(ext, false)
	require.False(t, result.Valid)

	found := false
	for _, issue := range result.Issues {
		if issue.Severity == ValidationError && strings.Contains(issue.Message, "unknown capability 'invalid-capability'") {
			found = true
		}
	}
	require.True(t, found, "expected unknown capability error")
}

func TestValidateExtension_InvalidPlatforms(t *testing.T) {
	ext := &ExtensionMetadata{
		Id:          "pub.ext",
		DisplayName: "Test",
		Description: "Test",
		Versions: []ExtensionVersion{
			{
				Version: "1.0.0",
				Artifacts: map[string]ExtensionArtifact{
					"linux/amd64": {
						URL:      "https://example.com/ext",
						Checksum: ExtensionChecksum{Algorithm: "sha256", Value: "abc"},
					},
					"freebsd/amd64": {
						URL:      "https://example.com/ext",
						Checksum: ExtensionChecksum{Algorithm: "sha256", Value: "abc"},
					},
				},
			},
		},
	}

	result := validateExtension(ext, false)
	require.False(t, result.Valid)

	found := false
	for _, issue := range result.Issues {
		if issue.Severity == ValidationError && strings.Contains(issue.Message, "unknown platform 'freebsd/amd64'") {
			found = true
		}
	}
	require.True(t, found, "expected unknown platform error")
}

func TestValidateExtension_MissingArtifactURL(t *testing.T) {
	ext := &ExtensionMetadata{
		Id:          "pub.ext",
		DisplayName: "Test",
		Description: "Test",
		Versions: []ExtensionVersion{
			{
				Version: "1.0.0",
				Artifacts: map[string]ExtensionArtifact{
					"linux/amd64": {
						Checksum: ExtensionChecksum{Algorithm: "sha256", Value: "abc"},
					},
				},
			},
		},
	}

	result := validateExtension(ext, false)
	require.False(t, result.Valid)

	found := false
	for _, issue := range result.Issues {
		if issue.Severity == ValidationError && strings.Contains(issue.Message, "missing required field 'url'") {
			found = true
		}
	}
	require.True(t, found, "expected missing URL error")
}

func TestValidateExtension_MissingChecksum_NonStrict(t *testing.T) {
	ext := &ExtensionMetadata{
		Id:          "pub.ext",
		DisplayName: "Test",
		Description: "Test",
		Versions: []ExtensionVersion{
			{
				Version: "1.0.0",
				Artifacts: map[string]ExtensionArtifact{
					"linux/amd64": {
						URL: "https://example.com/ext",
					},
				},
			},
		},
	}

	result := validateExtension(ext, false)
	require.True(t, result.Valid, "missing checksum should be a warning in non-strict mode")

	found := false
	for _, issue := range result.Issues {
		if issue.Severity == ValidationWarning && strings.Contains(issue.Message, "missing checksum") {
			found = true
		}
	}
	require.True(t, found, "expected missing checksum warning")
}

func TestValidateExtension_MissingChecksum_Strict(t *testing.T) {
	ext := &ExtensionMetadata{
		Id:          "pub.ext",
		DisplayName: "Test",
		Description: "Test",
		Versions: []ExtensionVersion{
			{
				Version: "1.0.0",
				Artifacts: map[string]ExtensionArtifact{
					"linux/amd64": {
						URL: "https://example.com/ext",
					},
				},
			},
		},
	}

	result := validateExtension(ext, true)
	require.False(t, result.Valid, "missing checksum should be an error in strict mode")

	found := false
	for _, issue := range result.Issues {
		if issue.Severity == ValidationError && strings.Contains(issue.Message, "missing required checksum") {
			found = true
		}
	}
	require.True(t, found, "expected missing checksum error in strict mode")
}

func TestValidateExtension_ChecksumAlgorithmValidation(t *testing.T) {
	t.Run("missing algorithm with value", func(t *testing.T) {
		ext := &ExtensionMetadata{
			Id:          "pub.ext",
			DisplayName: "Test",
			Description: "Test",
			Versions: []ExtensionVersion{
				{
					Version: "1.0.0",
					Artifacts: map[string]ExtensionArtifact{
						"linux/amd64": {
							URL:      "https://example.com/ext",
							Checksum: ExtensionChecksum{Value: "abc123"},
						},
					},
				},
			},
		}

		result := validateExtension(ext, false)
		require.False(t, result.Valid)
		found := false
		for _, issue := range result.Issues {
			if strings.Contains(issue.Message, "missing algorithm") {
				found = true
			}
		}
		require.True(t, found, "expected missing algorithm error")
	})

	t.Run("unsupported algorithm", func(t *testing.T) {
		ext := &ExtensionMetadata{
			Id:          "pub.ext",
			DisplayName: "Test",
			Description: "Test",
			Versions: []ExtensionVersion{
				{
					Version: "1.0.0",
					Artifacts: map[string]ExtensionArtifact{
						"linux/amd64": {
							URL:      "https://example.com/ext",
							Checksum: ExtensionChecksum{Algorithm: "md5", Value: "abc123"},
						},
					},
				},
			},
		}

		result := validateExtension(ext, false)
		require.False(t, result.Valid)
		found := false
		for _, issue := range result.Issues {
			if strings.Contains(issue.Message, "unsupported checksum algorithm") {
				found = true
			}
		}
		require.True(t, found, "expected unsupported algorithm error")
	})
}

func TestValidateExtension_RequireArtifactsOrDependencies(t *testing.T) {
	ext := &ExtensionMetadata{
		Id:          "pub.ext",
		DisplayName: "Test",
		Description: "Test",
		Versions: []ExtensionVersion{
			{Version: "1.0.0"},
		},
	}

	result := validateExtension(ext, false)
	require.False(t, result.Valid)

	found := false
	for _, issue := range result.Issues {
		if strings.Contains(issue.Message, "must define at least one artifact or dependency") {
			found = true
		}
	}
	require.True(t, found, "expected artifacts/dependencies requirement error")
}

func TestValidateExtension_DependenciesOnly(t *testing.T) {
	ext := &ExtensionMetadata{
		Id:          "pub.ext",
		DisplayName: "Test",
		Description: "Test",
		Versions: []ExtensionVersion{
			{
				Version:      "1.0.0",
				Dependencies: []ExtensionDependency{{Id: "other.ext", Version: ">=1.0.0"}},
			},
		},
	}

	result := validateExtension(ext, false)
	for _, issue := range result.Issues {
		require.False(t, strings.Contains(issue.Message, "must define at least one artifact or dependency"),
			"extension with dependencies should not require artifacts")
	}
}

func TestValidateExtensions_NilExtensionEntry(t *testing.T) {
	result := ValidateExtensions([]*ExtensionMetadata{nil}, false)
	require.False(t, result.Valid)
	require.Len(t, result.Extensions, 1)
	require.False(t, result.Extensions[0].Valid)
}

func TestValidateExtension_LatestVersionUseSemver(t *testing.T) {
	ext := &ExtensionMetadata{
		Id:          "pub.ext",
		DisplayName: "Test",
		Description: "Test",
		Versions: []ExtensionVersion{
			{Version: "2.0.0", Artifacts: validArtifacts()},
			{Version: "1.0.0", Artifacts: validArtifacts()},
			{Version: "1.5.0", Artifacts: validArtifacts()},
		},
	}

	result := validateExtension(ext, false)
	require.Equal(t, "2.0.0", result.LatestVersion, "should pick highest semver, not last element")
}

func TestValidateExtension_AllValidCapabilities(t *testing.T) {
	ext := &ExtensionMetadata{
		Id:          "pub.ext",
		DisplayName: "Test",
		Description: "Test",
		Versions: []ExtensionVersion{
			{
				Version: "1.0.0",
				Capabilities: []CapabilityType{
					CustomCommandCapability,
					LifecycleEventsCapability,
					McpServerCapability,
					ServiceTargetProviderCapability,
					FrameworkServiceProviderCapability,
					MetadataCapability,
				},
				Artifacts: validArtifacts(),
			},
		},
	}

	result := validateExtension(ext, false)
	require.True(t, result.Valid)
	require.Empty(t, errorsOnly(result.Issues))
}

func TestValidateExtension_AllValidPlatforms(t *testing.T) {
	artifacts := map[string]ExtensionArtifact{}
	for _, p := range ValidPlatforms {
		artifacts[p] = ExtensionArtifact{
			URL:      "https://example.com/" + p,
			Checksum: ExtensionChecksum{Algorithm: "sha256", Value: "abc"},
		}
	}

	ext := &ExtensionMetadata{
		Id:          "pub.ext",
		DisplayName: "Test",
		Description: "Test",
		Versions: []ExtensionVersion{
			{Version: "1.0.0", Artifacts: artifacts},
		},
	}

	result := validateExtension(ext, false)
	require.True(t, result.Valid)
	require.Empty(t, errorsOnly(result.Issues))
}

// Helper functions

func errorsOnly(issues []ValidationIssue) []ValidationIssue {
	var result []ValidationIssue
	for _, issue := range issues {
		if issue.Severity == ValidationError {
			result = append(result, issue)
		}
	}
	return result
}
