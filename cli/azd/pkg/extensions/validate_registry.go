// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
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

// validChecksumAlgorithms defines the supported checksum algorithms.
var validChecksumAlgorithms = []string{"sha256", "sha512"}

// extensionIdRegex validates extension ID format: dot-separated lowercase segments with hyphens.
var extensionIdRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

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

// ValidateExtensions validates a slice of parsed extension metadata.
func ValidateExtensions(exts []*ExtensionMetadata, strict bool) *RegistryValidationResult {
	result := &RegistryValidationResult{
		Valid: true,
	}

	for _, ext := range exts {
		var extResult ExtensionValidationResult
		if ext == nil {
			extResult = ExtensionValidationResult{
				Issues: []ValidationIssue{{Severity: ValidationError, Message: "null extension entry"}},
				Valid:  false,
			}
		} else {
			extResult = validateExtension(ext, strict)
		}
		result.Extensions = append(result.Extensions, extResult)
		if !extResult.Valid {
			result.Valid = false
		}
	}

	return result
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

	return ValidateExtensions(registry.Extensions, strict), nil
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
		result.addError(fmt.Sprintf("invalid extension ID format '%s': must be dot-separated lowercase segments "+
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

	// Find latest version using semver ordering
	latestVer := findLatestVersion(ext.Versions)
	if latestVer != nil {
		result.LatestVersion = latestVer.Version
		result.Capabilities = latestVer.Capabilities
		if latestVer.Artifacts != nil {
			for platform := range latestVer.Artifacts {
				result.Platforms = append(result.Platforms, platform)
			}
		}
	}

	return result
}

// findLatestVersion finds the latest version using semver ordering, preferring stable over pre-release.
func findLatestVersion(versions []ExtensionVersion) *ExtensionVersion {
	var latest *ExtensionVersion
	var latestSemver *semver.Version

	for i := range versions {
		v, err := semver.NewVersion(versions[i].Version)
		if err != nil {
			continue
		}

		if latestSemver == nil || v.GreaterThan(latestSemver) {
			latest = &versions[i]
			latestSemver = v
		}
	}

	// If no valid semver found, fall back to last element
	if latest == nil && len(versions) > 0 {
		latest = &versions[len(versions)-1]
	}

	return latest
}

// validateVersion validates a single version entry within an extension.
func validateVersion(result *ExtensionValidationResult, index int, ver *ExtensionVersion, strict bool) {
	prefix := fmt.Sprintf("versions[%d]", index)

	// Validate semver format using the semver package
	if ver.Version == "" {
		result.addError(fmt.Sprintf("%s: missing required field 'version'", prefix))
	} else if _, err := semver.StrictNewVersion(ver.Version); err != nil {
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

	// Enforce that each version has at least one artifact or dependency
	hasArtifacts := len(ver.Artifacts) > 0
	hasDependencies := len(ver.Dependencies) > 0
	if !hasArtifacts && !hasDependencies {
		result.addError(fmt.Sprintf("%s: version must define at least one artifact or dependency", prefix))
	}

	// Validate artifacts
	if hasArtifacts {
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
			} else if artifact.Checksum.Algorithm == "" {
				result.addError(fmt.Sprintf("%s: checksum value present but missing algorithm "+
					"(supported: %s)", artifactPrefix, strings.Join(validChecksumAlgorithms, ", ")))
			} else if !isValidChecksumAlgorithm(artifact.Checksum.Algorithm) {
				result.addError(fmt.Sprintf("%s: unsupported checksum algorithm '%s' "+
					"(supported: %s)", artifactPrefix, artifact.Checksum.Algorithm,
					strings.Join(validChecksumAlgorithms, ", ")))
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

func isValidChecksumAlgorithm(alg string) bool {
	for _, valid := range validChecksumAlgorithms {
		if alg == valid {
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
