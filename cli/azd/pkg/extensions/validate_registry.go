// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ValidPlatforms defines the valid os/arch combinations for extension artifacts.
var ValidPlatforms = []string{
	"windows/amd64",
	"windows/arm64",
	"darwin/amd64",
	"darwin/arm64",
	"linux/amd64",
	"linux/arm64",
}

// ValidCapabilities defines the valid capability types for extensions.
var ValidCapabilities = []CapabilityType{
	CustomCommandCapability,
	LifecycleEventsCapability,
	McpServerCapability,
	ServiceTargetProviderCapability,
	FrameworkServiceProviderCapability,
	MetadataCapability,
}

// semverRegex validates strict semver format: MAJOR.MINOR.PATCH with optional pre-release suffix.
var semverRegex = regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9]+(\.[a-zA-Z0-9]+)*)?$`)

// extensionIdRegex validates extension ID format: dot-separated segments, each alphanumeric with hyphens.
var extensionIdRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+$`)

// ValidationSeverity represents the severity of a validation error.
type ValidationSeverity string

const (
	// ValidationError is a validation error that prevents the registry from being valid.
	ValidationError ValidationSeverity = "error"
	// ValidationWarning is a validation warning that does not prevent the registry from being valid.
	ValidationWarning ValidationSeverity = "warning"
)

// ValidationIssue represents a single validation finding.
type ValidationIssue struct {
	// Severity is the severity of the validation issue (error or warning).
	Severity ValidationSeverity `json:"severity"`
	// Message describes the validation issue.
	Message string `json:"message"`
}

// ExtensionValidationResult represents the validation result for a single extension.
type ExtensionValidationResult struct {
	// Id is the extension ID (may be empty if missing).
	Id string `json:"id"`
	// DisplayName is the extension display name.
	DisplayName string `json:"displayName"`
	// LatestVersion is the latest version string found.
	LatestVersion string `json:"latestVersion"`
	// Capabilities lists the capabilities of the latest version.
	Capabilities []CapabilityType `json:"capabilities"`
	// Platforms lists the platforms of the latest version.
	Platforms []string `json:"platforms"`
	// Issues contains all validation issues found.
	Issues []ValidationIssue `json:"issues"`
	// Valid is true if no errors were found.
	Valid bool `json:"valid"`
}

// RegistryValidationResult represents the validation result for an entire registry file.
type RegistryValidationResult struct {
	// Extensions contains validation results for each extension.
	Extensions []ExtensionValidationResult `json:"extensions"`
	// Valid is true if all extensions are valid.
	Valid bool `json:"valid"`
}

// ValidateRegistryJSON validates the raw JSON bytes of a registry.json file.
func ValidateRegistryJSON(data []byte, strict bool) (*RegistryValidationResult, error) {
	var registry Registry

	// Determine the JSON structure and parse accordingly
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) > 0 && trimmed[0] == '[' {
		// Array of extensions
		var extensions []*ExtensionMetadata
		if err := json.Unmarshal(data, &extensions); err != nil {
			return nil, fmt.Errorf("invalid registry format: failed to parse as extension array: %w", err)
		}
		registry.Extensions = extensions
	} else {
		// Try as Registry object (with "extensions" wrapper)
		if err := json.Unmarshal(data, &registry); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}

		// If no "extensions" field, try as a single extension
		if registry.Extensions == nil {
			var single ExtensionMetadata
			if err := json.Unmarshal(data, &single); err != nil {
				return nil, fmt.Errorf("invalid registry format: expected object with 'extensions' array, "+
					"an array of extensions, or a single extension object: %w", err)
			}
			registry.Extensions = []*ExtensionMetadata{&single}
		}
	}

	if len(registry.Extensions) == 0 {
		return nil, fmt.Errorf("registry contains no extensions")
	}

	result := &RegistryValidationResult{
		Valid: true,
	}

	for _, ext := range registry.Extensions {
		extResult := validateExtension(ext, strict)
		result.Extensions = append(result.Extensions, extResult)
		if !extResult.Valid {
			result.Valid = false
		}
	}

	return result, nil
}

// validateExtension validates a single extension metadata entry.
func validateExtension(ext *ExtensionMetadata, strict bool) ExtensionValidationResult {
	result := ExtensionValidationResult{
		Id:          ext.Id,
		DisplayName: ext.DisplayName,
		Valid:       true,
	}

	// Required fields
	if ext.Id == "" {
		result.addError("missing or empty required field 'id'")
	} else if !extensionIdRegex.MatchString(ext.Id) {
		result.addError(fmt.Sprintf("invalid extension ID format '%s': must be dot-separated segments "+
			"(e.g. 'publisher.extension' or 'publisher.category.extension')", ext.Id))
	}

	if ext.DisplayName == "" {
		result.addError("missing or empty required field 'displayName'")
	}

	if ext.Description == "" {
		result.addError("missing or empty required field 'description'")
	}

	if len(ext.Versions) == 0 {
		result.addError("missing or empty required field 'versions'")
		return result
	}

	// Validate each version
	for i, ver := range ext.Versions {
		validateVersion(&result, i, &ver, strict)
	}

	// Set latest version info from the last version entry
	latestIdx := len(ext.Versions) - 1
	latestVer := ext.Versions[latestIdx]
	result.LatestVersion = latestVer.Version
	result.Capabilities = latestVer.Capabilities
	if latestVer.Artifacts != nil {
		for platform := range latestVer.Artifacts {
			result.Platforms = append(result.Platforms, platform)
		}
	}

	return result
}

// validateVersion validates a single version entry within an extension.
func validateVersion(result *ExtensionValidationResult, index int, ver *ExtensionVersion, strict bool) {
	prefix := fmt.Sprintf("versions[%d]", index)

	// Validate semver format
	if ver.Version == "" {
		result.addError(fmt.Sprintf("%s: missing required field 'version'", prefix))
	} else if !semverRegex.MatchString(ver.Version) {
		result.addError(fmt.Sprintf("%s: invalid semver format '%s' "+
			"(expected MAJOR.MINOR.PATCH with optional pre-release suffix)", prefix, ver.Version))
	}

	// Validate capabilities
	for _, cap := range ver.Capabilities {
		if !isValidCapability(cap) {
			result.addError(fmt.Sprintf("%s: unknown capability '%s' (valid: %s)",
				prefix, cap, strings.Join(capabilityStrings(), ", ")))
		}
	}

	// Validate artifacts
	if ver.Artifacts != nil {
		for platform, artifact := range ver.Artifacts {
			artifactPrefix := fmt.Sprintf("%s.artifacts[%s]", prefix, platform)

			if !isValidPlatform(platform) {
				result.addError(fmt.Sprintf("%s: unknown platform '%s' (valid: %s)",
					artifactPrefix, platform, strings.Join(ValidPlatforms, ", ")))
			}

			if artifact.URL == "" {
				result.addError(fmt.Sprintf("%s: missing required field 'url'", artifactPrefix))
			}

			if artifact.Checksum.Value == "" {
				if strict {
					result.addError(fmt.Sprintf("%s: missing required checksum", artifactPrefix))
				} else {
					result.addWarning(fmt.Sprintf("%s: missing checksum (recommended for integrity verification)",
						artifactPrefix))
				}
			}
		}
	}
}

func (r *ExtensionValidationResult) addError(msg string) {
	r.Issues = append(r.Issues, ValidationIssue{
		Severity: ValidationError,
		Message:  msg,
	})
	r.Valid = false
}

func (r *ExtensionValidationResult) addWarning(msg string) {
	r.Issues = append(r.Issues, ValidationIssue{
		Severity: ValidationWarning,
		Message:  msg,
	})
}

func isValidCapability(cap CapabilityType) bool {
	for _, valid := range ValidCapabilities {
		if cap == valid {
			return true
		}
	}
	return false
}

func isValidPlatform(platform string) bool {
	for _, valid := range ValidPlatforms {
		if platform == valid {
			return true
		}
	}
	return false
}

func capabilityStrings() []string {
	result := make([]string, len(ValidCapabilities))
	for i, cap := range ValidCapabilities {
		result[i] = string(cap)
	}
	return result
}
