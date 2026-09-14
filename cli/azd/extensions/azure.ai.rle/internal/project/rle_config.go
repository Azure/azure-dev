// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/pelletier/go-toml/v2"
)

const (
	RleConfigFile        = "rle.toml"
	DefaultRleVersion    = "1.0.0"
	maxAgentNameLength   = 256
	maxAgentVersionLen   = 128
	maxHarnessBaseURLLen = 2048
)

type RleType string

const (
	RleTypeGym     RleType = "Gym"
	RleTypeHarness RleType = "Harness"
)

type RleSubtype string

const (
	RleSubtypeOpenEnv     RleSubtype = "OpenEnv"
	RleSubtypeHostedAgent RleSubtype = "HostedAgent"
	RleSubtypeBYOH        RleSubtype = "BYOH"
)

// RleConfig is the host-agnostic source configuration for one immutable RLE release.
type RleConfig struct {
	Rle RleManifest `toml:"rle"`
}

// RleManifest uses the control-plane field names so the manifest maps directly to an RLE release.
type RleManifest struct {
	Name         string     `toml:"name"`
	Version      string     `toml:"version"`
	Type         RleType    `toml:"type"`
	Subtype      RleSubtype `toml:"subtype"`
	AgentName    *string    `toml:"agentName,omitempty"`
	AgentVersion *string    `toml:"agentVersion,omitempty"`
	BaseURL      *string    `toml:"baseUrl,omitempty"`
}

var semanticVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type semanticVersion struct {
	major int64
	minor int64
	patch int64
}

func (v semanticVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

// LoadRleConfig reads and validates rle.toml from dir.
func LoadRleConfig(dir string) (RleConfig, error) {
	path := filepath.Join(dir, RleConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return RleConfig{}, missingRleConfigError()
		}
		return RleConfig{}, fmt.Errorf("read %s: %w", RleConfigFile, err)
	}

	var config RleConfig
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&config); err != nil {
		return RleConfig{}, invalidRleConfigError(fmt.Sprintf("Could not parse %s: %v", RleConfigFile, err))
	}
	return NormalizeRleConfig(config)
}

// WriteRleConfig validates config and writes its canonical representation to dir.
func WriteRleConfig(dir string, config RleConfig) error {
	normalized, err := NormalizeRleConfig(config)
	if err != nil {
		return err
	}
	data, err := toml.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", RleConfigFile, err)
	}
	return os.WriteFile(filepath.Join(dir, RleConfigFile), append(data, '\n'), 0644)
}

// NormalizeRleConfig validates the manifest against the RLE control-plane type contract.
func NormalizeRleConfig(config RleConfig) (RleConfig, error) {
	manifest := config.Rle

	name, err := ValidateEnvironmentName(strings.TrimSpace(manifest.Name))
	if err != nil {
		return RleConfig{}, localError(
			err.Error(),
			"rle_invalid_environment_name",
			"Use snake_case starting with a letter, for example code_rl.",
		)
	}
	manifest.Name = name

	version, err := NormalizeRleVersion(manifest.Version)
	if err != nil {
		return RleConfig{}, err
	}
	manifest.Version = version

	manifest.Type, err = normalizeRleType(manifest.Type)
	if err != nil {
		return RleConfig{}, err
	}
	manifest.Subtype, err = normalizeRleSubtype(manifest.Subtype)
	if err != nil {
		return RleConfig{}, err
	}

	switch manifest.Type {
	case RleTypeGym:
		if manifest.Subtype != RleSubtypeOpenEnv {
			return RleConfig{}, localError(
				"Only the OpenEnv subtype is supported when rle.type is Gym.",
				"rle_manifest_type_configuration_invalid",
				`Set rle.type = "Gym" and rle.subtype = "OpenEnv".`,
			)
		}
		if manifest.AgentName != nil || manifest.AgentVersion != nil || manifest.BaseURL != nil {
			return RleConfig{}, localError(
				"agentName, agentVersion, and baseUrl are allowed only when rle.type is Harness.",
				"rle_manifest_type_configuration_invalid",
				"Remove harness configuration from this Gym: OpenEnv manifest.",
			)
		}
	case RleTypeHarness:
		switch manifest.Subtype {
		case RleSubtypeHostedAgent:
			if manifest.BaseURL != nil {
				return RleConfig{}, localError(
					"baseUrl must be omitted when rle.subtype is HostedAgent.",
					"rle_manifest_type_configuration_invalid",
					"Remove baseUrl or set rle.subtype to BYOH.",
				)
			}
			agentName, err := normalizeRequiredAgentField(
				manifest.AgentName,
				"agentName",
				maxAgentNameLength,
				"rle_agent_name_required",
			)
			if err != nil {
				return RleConfig{}, err
			}
			agentVersion, err := normalizeRequiredAgentField(
				manifest.AgentVersion,
				"agentVersion",
				maxAgentVersionLen,
				"rle_agent_version_required",
			)
			if err != nil {
				return RleConfig{}, err
			}
			if !isValidHostedAgentVersion(agentVersion) {
				return RleConfig{}, localError(
					"agentVersion must be a positive integer or a 'draft-{positive-unix-timestamp}' value.",
					"rle_agent_version_invalid",
					`Use a value such as "12" or "draft-1767225600".`,
				)
			}
			manifest.AgentName = &agentName
			manifest.AgentVersion = &agentVersion
		case RleSubtypeBYOH:
			if manifest.AgentName != nil || manifest.AgentVersion != nil {
				return RleConfig{}, localError(
					"agentName and agentVersion must be omitted when rle.subtype is BYOH.",
					"rle_manifest_type_configuration_invalid",
					"Remove the Hosted Agent fields or set rle.subtype to HostedAgent.",
				)
			}
			baseURL, err := normalizeHarnessBaseURL(manifest.BaseURL)
			if err != nil {
				return RleConfig{}, err
			}
			manifest.BaseURL = &baseURL
		default:
			return RleConfig{}, localError(
				"Only the HostedAgent and BYOH subtypes are supported when rle.type is Harness.",
				"rle_manifest_type_configuration_invalid",
				`Set rle.subtype to "HostedAgent" or "BYOH".`,
			)
		}
	default:
		return RleConfig{}, localError(
			"rle.type must be Gym or Harness.",
			"rle_manifest_type_invalid",
			`Set rle.type to "Gym" or "Harness".`,
		)
	}

	config.Rle = manifest
	return config, nil
}

// NormalizeRleVersion validates the semantic-version identity accepted by the RLE control plane.
func NormalizeRleVersion(value string) (string, error) {
	version, err := parseSemanticVersion(value)
	if err != nil {
		return "", localError(
			"RLE version must be a semantic version in the format major.minor.patch.",
			"rle_manifest_version_invalid",
			fmt.Sprintf("Use a version such as %s.", DefaultRleVersion),
		)
	}
	return version.String(), nil
}

// VersionBumpForManifestVersion returns the service version bump needed to create desired after current.
// An empty current version means the manifest must declare the service's initial version, 1.0.0.
func VersionBumpForManifestVersion(current string, desired string) (string, error) {
	desiredVersion, err := parseSemanticVersion(desired)
	if err != nil {
		return "", fmt.Errorf("parse desired RLE version: %w", err)
	}
	if strings.TrimSpace(current) == "" {
		if desiredVersion.String() != DefaultRleVersion {
			return "", localError(
				fmt.Sprintf("A new RLE environment must start at version %s, but rle.toml declares %s.", DefaultRleVersion, desiredVersion),
				"rle_manifest_initial_version_invalid",
				fmt.Sprintf("Set rle.version to %s for a new environment.", DefaultRleVersion),
			)
		}
		return "Major", nil
	}

	currentVersion, err := parseSemanticVersion(current)
	if err != nil {
		return "", fmt.Errorf("parse current RLE version %q: %w", current, err)
	}
	if desiredVersion.major == currentVersion.major+1 &&
		desiredVersion.minor == 0 &&
		desiredVersion.patch == 0 {
		return "Major", nil
	}
	if desiredVersion.major == currentVersion.major &&
		desiredVersion.minor == currentVersion.minor+1 &&
		desiredVersion.patch == 0 {
		return "Minor", nil
	}
	if desiredVersion.major == currentVersion.major &&
		desiredVersion.minor == currentVersion.minor &&
		desiredVersion.patch == currentVersion.patch+1 {
		return "Patch", nil
	}
	return "", localError(
		fmt.Sprintf(
			"rle.version %s is not the next major, minor, or patch version after the deployed version %s.",
			desiredVersion,
			currentVersion,
		),
		"rle_manifest_version_not_next",
		"Update rle.version to the next major, minor, or patch release before publishing.",
	)
}

func missingRleConfigError() error {
	return localError(
		fmt.Sprintf("%s is required in the current folder.", RleConfigFile),
		"rle_manifest_missing",
		"Run azd ai rle init, or add a valid rle.toml before running this command.",
	)
}

func invalidRleConfigError(message string) error {
	return localError(
		message,
		"rle_manifest_invalid",
		"Fix rle.toml so it declares one valid RLE identity and type configuration.",
	)
}

func normalizeRleType(value RleType) (RleType, error) {
	switch strings.ToLower(strings.TrimSpace(string(value))) {
	case "gym":
		return RleTypeGym, nil
	case "harness":
		return RleTypeHarness, nil
	default:
		return "", localError(
			"rle.type must be Gym or Harness.",
			"rle_manifest_type_invalid",
			`Set rle.type to "Gym" or "Harness".`,
		)
	}
}

func normalizeRleSubtype(value RleSubtype) (RleSubtype, error) {
	switch strings.ToLower(strings.TrimSpace(string(value))) {
	case "openenv":
		return RleSubtypeOpenEnv, nil
	case "hostedagent":
		return RleSubtypeHostedAgent, nil
	case "byoh":
		return RleSubtypeBYOH, nil
	default:
		return "", localError(
			"rle.subtype must be OpenEnv, HostedAgent, or BYOH.",
			"rle_manifest_subtype_invalid",
			`Set rle.subtype to "OpenEnv", "HostedAgent", or "BYOH".`,
		)
	}
}

func normalizeRequiredAgentField(value *string, fieldName string, maximumLength int, requiredCode string) (string, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "", localError(
			fmt.Sprintf("%s is required when rle.subtype is HostedAgent.", fieldName),
			requiredCode,
			fmt.Sprintf("Add %s to rle.toml.", fieldName),
		)
	}
	normalized := strings.TrimSpace(*value)
	if utf16Length(normalized) > maximumLength {
		return "", localError(
			fmt.Sprintf("%s must be at most %d characters.", fieldName, maximumLength),
			"rle_manifest_field_too_long",
			fmt.Sprintf("Shorten %s in rle.toml.", fieldName),
		)
	}
	return normalized, nil
}

func normalizeHarnessBaseURL(value *string) (string, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "", localError(
			"baseUrl is required when rle.subtype is BYOH.",
			"rle_harness_base_url_required",
			"Add an absolute HTTPS baseUrl to rle.toml.",
		)
	}
	normalized := strings.TrimSpace(*value)
	if utf16Length(normalized) > maxHarnessBaseURLLen {
		return "", localError(
			fmt.Sprintf("baseUrl must be at most %d characters.", maxHarnessBaseURLLen),
			"rle_harness_base_url_invalid",
			"Use a shorter absolute HTTPS baseUrl without credentials, query, or fragment.",
		)
	}
	parsed, err := url.ParseRequestURI(normalized)
	if err != nil ||
		!parsed.IsAbs() ||
		!strings.EqualFold(parsed.Scheme, "https") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.ForceQuery ||
		parsed.Fragment != "" ||
		strings.Contains(normalized, "#") ||
		parsed.Opaque != "" {
		return "", localError(
			"baseUrl must be an absolute HTTPS URL without credentials, query, or fragment.",
			"rle_harness_base_url_invalid",
			"Use a URL such as https://harness.example.com/rle/.",
		)
	}
	parsed.Scheme = "https"
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Port() == "443" {
		parsed.Host = parsed.Hostname()
		if strings.Contains(parsed.Host, ":") {
			parsed.Host = "[" + parsed.Host + "]"
		}
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String(), nil
}

func isValidHostedAgentVersion(value string) bool {
	if strings.HasPrefix(value, "draft-") {
		return isPositiveInt64(strings.TrimPrefix(value, "draft-"))
	}
	return isPositiveInt64(value)
}

func isPositiveInt64(value string) bool {
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed > 0
}

func parseSemanticVersion(value string) (semanticVersion, error) {
	value = strings.TrimSpace(value)
	matches := semanticVersionPattern.FindStringSubmatch(value)
	if matches == nil {
		return semanticVersion{}, fmt.Errorf("invalid semantic version %q", value)
	}
	parts := make([]int64, 3)
	for index := 0; index < 3; index++ {
		part, err := strconv.ParseInt(matches[index+1], 10, 64)
		if err != nil || part < 0 || part > math.MaxInt32 {
			return semanticVersion{}, fmt.Errorf("invalid semantic version %q", value)
		}
		parts[index] = part
	}
	return semanticVersion{major: parts[0], minor: parts[1], patch: parts[2]}, nil
}

func utf16Length(value string) int {
	length := 0
	for _, r := range value {
		if r > 0xFFFF {
			length += 2
		} else {
			length++
		}
	}
	return length
}

func localError(message string, code string, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: suggestion,
	}
}
