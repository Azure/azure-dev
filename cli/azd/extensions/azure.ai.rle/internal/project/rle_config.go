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
	RleConfigFile                   = "rle.toml"
	DefaultRleVersion               = "1.0.0"
	CurrentRleManifestSchemaVersion = "1.0.0"
	maxAgentNameLength              = 256
	maxAgentVersionLen              = 128
	maxHarnessBaseURLLen            = 2048
	maxRleMetadataEntries           = 64
	maxRleMetadataKeyLength         = 128
	maxRleMetadataValueLength       = 1024
	maxRleModelNameLength           = 256
	maxRleRendererNameLength        = 256
	maxRleCheckpointIDLength        = 512
	maxRleSamplerLength             = 128
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
	SchemaVersion *string                 `toml:"schema_version,omitempty"`
	Rle           RleManifest             `toml:"rle"`
	Defaults      *RleEnvironmentDefaults `toml:"defaults,omitempty"`
	Metadata      map[string]string       `toml:"metadata,omitempty"`
}

// RleManifest uses the control-plane field names so the [rle] table maps directly to an RLE release.
type RleManifest struct {
	Name         string     `toml:"name"`
	Version      string     `toml:"version"`
	Type         RleType    `toml:"type"`
	Subtype      RleSubtype `toml:"subtype"`
	AgentName    *string    `toml:"agentName,omitempty"`
	AgentVersion *string    `toml:"agentVersion,omitempty"`
	BaseURL      *string    `toml:"baseUrl,omitempty"`
}

// RleEnvironmentDefaults contains reusable version-scoped training defaults.
type RleEnvironmentDefaults struct {
	Model         *RleModelDefaults         `toml:"model,omitempty" json:"model,omitempty"`
	Seed          *int                      `toml:"seed,omitempty" json:"seed,omitempty"`
	Reinforcement *RleReinforcementDefaults `toml:"reinforcement,omitempty" json:"reinforcement,omitempty"`
	Grpo          *RleGrpoDefaults          `toml:"grpo,omitempty" json:"grpo,omitempty"`
	Loom          *RleLoomDefaults          `toml:"loom,omitempty" json:"loom,omitempty"`
}

// RleModelDefaults contains the optional model and renderer selection.
type RleModelDefaults struct {
	Name         *string `toml:"name,omitempty" json:"name,omitempty"`
	RendererName *string `toml:"renderer_name,omitempty" json:"renderer_name,omitempty"`
}

// RleReinforcementDefaults contains the environment-owned Training Jobs settings.
type RleReinforcementDefaults struct {
	Hyperparameters *RleReinforcementHyperparameters `toml:"hyperparameters,omitempty" json:"hyperparameters,omitempty"`
	MaxEpisodeSteps *int                             `toml:"max_episode_steps,omitempty" json:"max_episode_steps,omitempty"`
}

// RleReinforcementHyperparameters mirrors the Training Jobs reinforcement wire fields.
type RleReinforcementHyperparameters struct {
	NumberOfEpochs         *int     `toml:"n_epochs,omitempty" json:"n_epochs,omitempty"`
	BatchSize              *int     `toml:"batch_size,omitempty" json:"batch_size,omitempty"`
	LearningRateMultiplier *float64 `toml:"learning_rate_multiplier,omitempty" json:"learning_rate_multiplier,omitempty"`
	EvalInterval           *int     `toml:"eval_interval,omitempty" json:"eval_interval,omitempty"`
	EvalSamples            *int     `toml:"eval_samples,omitempty" json:"eval_samples,omitempty"`
	ComputeMultiplier      *float64 `toml:"compute_multiplier,omitempty" json:"compute_multiplier,omitempty"`
	ReasoningEffort        *string  `toml:"reasoning_effort,omitempty" json:"reasoning_effort,omitempty"`
}

// RleGrpoDefaults retains recipe-level GRPO settings without aliasing Training Jobs batch size.
type RleGrpoDefaults struct {
	GroupSize      *int `toml:"group_size,omitempty" json:"group_size,omitempty"`
	GroupsPerBatch *int `toml:"groups_per_batch,omitempty" json:"groups_per_batch,omitempty"`
	MaxSteps       *int `toml:"max_steps,omitempty" json:"max_steps,omitempty"`
}

// RleLoomDefaults contains version-scoped Loom settings.
type RleLoomDefaults struct {
	CheckpointID *string `toml:"checkpoint_id,omitempty" json:"checkpoint_id,omitempty"`
	LoraRank     *int    `toml:"lora_rank,omitempty" json:"lora_rank,omitempty"`
	Sampler      *string `toml:"sampler,omitempty" json:"sampler,omitempty"`
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

	schemaVersion, defaults, metadata, err := normalizeRleManifestMetadata(
		config.SchemaVersion,
		config.Defaults,
		config.Metadata,
	)
	if err != nil {
		return RleConfig{}, err
	}
	config.SchemaVersion = schemaVersion
	config.Rle = manifest
	config.Defaults = defaults
	config.Metadata = metadata
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

// ValidateInitialRleVersion verifies the only explicit version valid when no environment exists.
func ValidateInitialRleVersion(value string) error {
	version, err := NormalizeRleVersion(value)
	if err != nil {
		return err
	}
	if version != DefaultRleVersion {
		return localError(
			fmt.Sprintf("A new RLE environment must start at version %s, but rle.toml declares %s.", DefaultRleVersion, version),
			"rle_manifest_initial_version_invalid",
			fmt.Sprintf("Set rle.version to %s for a new environment.", DefaultRleVersion),
		)
	}
	return nil
}

func normalizeRleManifestMetadata(
	schemaVersion *string,
	defaults *RleEnvironmentDefaults,
	metadata map[string]string,
) (*string, *RleEnvironmentDefaults, map[string]string, error) {
	normalizedSchemaVersion, err := normalizeRleSchemaVersion(
		schemaVersion,
		defaults != nil || metadata != nil,
	)
	if err != nil {
		return nil, nil, nil, err
	}

	normalizedDefaults, err := normalizeRleDefaults(defaults)
	if err != nil {
		return nil, nil, nil, err
	}
	normalizedMetadata, err := normalizeRleMetadata(metadata)
	if err != nil {
		return nil, nil, nil, err
	}
	return normalizedSchemaVersion, normalizedDefaults, normalizedMetadata, nil
}

func normalizeRleSchemaVersion(value *string, required bool) (*string, error) {
	if value == nil {
		if required {
			return nil, localError(
				"schema_version is required when defaults or metadata is supplied.",
				"rle_manifest_schema_version_required",
				fmt.Sprintf("Set root-level schema_version to %q.", CurrentRleManifestSchemaVersion),
			)
		}
		return nil, nil
	}

	normalized := strings.TrimSpace(*value)
	if normalized == "" {
		return nil, localError(
			"schema_version must be a non-empty value.",
			"rle_manifest_schema_version_invalid",
			fmt.Sprintf("Set root-level schema_version to %q.", CurrentRleManifestSchemaVersion),
		)
	}
	if normalized != CurrentRleManifestSchemaVersion {
		return nil, localError(
			fmt.Sprintf("schema_version must be %q.", CurrentRleManifestSchemaVersion),
			"rle_manifest_schema_version_invalid",
			fmt.Sprintf("Set root-level schema_version to %q.", CurrentRleManifestSchemaVersion),
		)
	}
	return &normalized, nil
}

func normalizeRleDefaults(value *RleEnvironmentDefaults) (*RleEnvironmentDefaults, error) {
	if value == nil {
		return nil, nil
	}

	model, err := normalizeRleModelDefaults(value.Model)
	if err != nil {
		return nil, err
	}
	reinforcement, err := normalizeRleReinforcementDefaults(value.Reinforcement)
	if err != nil {
		return nil, err
	}
	grpo, err := normalizeRleGrpoDefaults(value.Grpo)
	if err != nil {
		return nil, err
	}
	loom, err := normalizeRleLoomDefaults(value.Loom)
	if err != nil {
		return nil, err
	}
	return &RleEnvironmentDefaults{
		Model:         model,
		Seed:          cloneInt(value.Seed),
		Reinforcement: reinforcement,
		Grpo:          grpo,
		Loom:          loom,
	}, nil
}

func normalizeRleModelDefaults(value *RleModelDefaults) (*RleModelDefaults, error) {
	if value == nil {
		return nil, nil
	}

	name, err := normalizeOptionalRleString(value.Name, "defaults.model.name", maxRleModelNameLength)
	if err != nil {
		return nil, err
	}
	rendererName, err := normalizeOptionalRleString(
		value.RendererName,
		"defaults.model.renderer_name",
		maxRleRendererNameLength,
	)
	if err != nil {
		return nil, err
	}
	return &RleModelDefaults{Name: name, RendererName: rendererName}, nil
}

func normalizeRleReinforcementDefaults(value *RleReinforcementDefaults) (*RleReinforcementDefaults, error) {
	if value == nil {
		return nil, nil
	}

	if err := validatePositiveInt(value.MaxEpisodeSteps, "defaults.reinforcement.max_episode_steps"); err != nil {
		return nil, err
	}
	hyperparameters, err := normalizeRleReinforcementHyperparameters(value.Hyperparameters)
	if err != nil {
		return nil, err
	}
	return &RleReinforcementDefaults{
		Hyperparameters: hyperparameters,
		MaxEpisodeSteps: cloneInt(value.MaxEpisodeSteps),
	}, nil
}

func normalizeRleReinforcementHyperparameters(
	value *RleReinforcementHyperparameters,
) (*RleReinforcementHyperparameters, error) {
	if value == nil {
		return nil, nil
	}

	for _, parameter := range []struct {
		value *int
		name  string
	}{
		{value.NumberOfEpochs, "defaults.reinforcement.hyperparameters.n_epochs"},
		{value.BatchSize, "defaults.reinforcement.hyperparameters.batch_size"},
		{value.EvalInterval, "defaults.reinforcement.hyperparameters.eval_interval"},
		{value.EvalSamples, "defaults.reinforcement.hyperparameters.eval_samples"},
	} {
		if err := validatePositiveInt(parameter.value, parameter.name); err != nil {
			return nil, err
		}
	}
	for _, parameter := range []struct {
		value *float64
		name  string
	}{
		{value.LearningRateMultiplier, "defaults.reinforcement.hyperparameters.learning_rate_multiplier"},
		{value.ComputeMultiplier, "defaults.reinforcement.hyperparameters.compute_multiplier"},
	} {
		if err := validateFinitePositiveFloat(parameter.value, parameter.name); err != nil {
			return nil, err
		}
	}
	reasoningEffort, err := normalizeReasoningEffort(value.ReasoningEffort)
	if err != nil {
		return nil, err
	}
	return &RleReinforcementHyperparameters{
		NumberOfEpochs:         cloneInt(value.NumberOfEpochs),
		BatchSize:              cloneInt(value.BatchSize),
		LearningRateMultiplier: cloneFloat64(value.LearningRateMultiplier),
		EvalInterval:           cloneInt(value.EvalInterval),
		EvalSamples:            cloneInt(value.EvalSamples),
		ComputeMultiplier:      cloneFloat64(value.ComputeMultiplier),
		ReasoningEffort:        reasoningEffort,
	}, nil
}

func normalizeRleGrpoDefaults(value *RleGrpoDefaults) (*RleGrpoDefaults, error) {
	if value == nil {
		return nil, nil
	}

	for _, parameter := range []struct {
		value *int
		name  string
	}{
		{value.GroupSize, "defaults.grpo.group_size"},
		{value.GroupsPerBatch, "defaults.grpo.groups_per_batch"},
		{value.MaxSteps, "defaults.grpo.max_steps"},
	} {
		if err := validatePositiveInt(parameter.value, parameter.name); err != nil {
			return nil, err
		}
	}
	return &RleGrpoDefaults{
		GroupSize:      cloneInt(value.GroupSize),
		GroupsPerBatch: cloneInt(value.GroupsPerBatch),
		MaxSteps:       cloneInt(value.MaxSteps),
	}, nil
}

func normalizeRleLoomDefaults(value *RleLoomDefaults) (*RleLoomDefaults, error) {
	if value == nil {
		return nil, nil
	}

	if err := validatePositiveInt(value.LoraRank, "defaults.loom.lora_rank"); err != nil {
		return nil, err
	}
	checkpointID, err := normalizeOptionalRleString(value.CheckpointID, "defaults.loom.checkpoint_id", maxRleCheckpointIDLength)
	if err != nil {
		return nil, err
	}
	sampler, err := normalizeOptionalRleString(value.Sampler, "defaults.loom.sampler", maxRleSamplerLength)
	if err != nil {
		return nil, err
	}
	return &RleLoomDefaults{
		CheckpointID: checkpointID,
		LoraRank:     cloneInt(value.LoraRank),
		Sampler:      sampler,
	}, nil
}

func normalizeRleMetadata(value map[string]string) (map[string]string, error) {
	if value == nil {
		return nil, nil
	}
	if len(value) > maxRleMetadataEntries {
		return nil, localError(
			fmt.Sprintf("metadata can contain at most %d entries.", maxRleMetadataEntries),
			"rle_manifest_metadata_invalid",
			"Remove metadata entries before publishing.",
		)
	}

	normalized := make(map[string]string, len(value))
	for key, metadataValue := range value {
		normalizedKey := strings.TrimSpace(key)
		normalizedValue := strings.TrimSpace(metadataValue)
		if normalizedKey == "" {
			return nil, localError(
				"metadata keys must be non-empty.",
				"rle_manifest_metadata_invalid",
				"Use a non-empty metadata key.",
			)
		}
		if normalizedValue == "" {
			return nil, localError(
				"metadata values must be non-empty.",
				"rle_manifest_metadata_invalid",
				"Use a non-empty metadata value.",
			)
		}
		if utf16Length(normalizedKey) > maxRleMetadataKeyLength ||
			utf16Length(normalizedValue) > maxRleMetadataValueLength {
			return nil, localError(
				fmt.Sprintf(
					"metadata keys must be at most %d characters and values at most %d characters.",
					maxRleMetadataKeyLength,
					maxRleMetadataValueLength,
				),
				"rle_manifest_metadata_invalid",
				"Shorten the metadata key or value.",
			)
		}
		if _, exists := normalized[normalizedKey]; exists {
			return nil, localError(
				fmt.Sprintf("metadata contains duplicate key %q after normalization.", normalizedKey),
				"rle_manifest_metadata_invalid",
				"Use unique metadata keys after trimming whitespace.",
			)
		}
		normalized[normalizedKey] = normalizedValue
	}
	return normalized, nil
}

func normalizeReasoningEffort(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}

	normalized := strings.ToLower(strings.TrimSpace(*value))
	if normalized == "" {
		return nil, nil
	}
	switch normalized {
	case "low", "medium", "high":
		return &normalized, nil
	default:
		return nil, localError(
			"defaults.reinforcement.hyperparameters.reasoning_effort must be low, medium, or high.",
			"rle_manifest_default_invalid",
			"Set reasoning_effort to low, medium, or high.",
		)
	}
}

func normalizeOptionalRleString(value *string, fieldName string, maximumLength int) (*string, error) {
	if value == nil {
		return nil, nil
	}

	normalized := strings.TrimSpace(*value)
	if normalized == "" {
		return nil, nil
	}
	if utf16Length(normalized) > maximumLength {
		return nil, localError(
			fmt.Sprintf("%s must be at most %d characters.", fieldName, maximumLength),
			"rle_manifest_default_invalid",
			fmt.Sprintf("Shorten %s in rle.toml.", fieldName),
		)
	}
	return &normalized, nil
}

func validatePositiveInt(value *int, fieldName string) error {
	if value == nil || *value > 0 {
		return nil
	}
	return localError(
		fmt.Sprintf("%s must be greater than zero.", fieldName),
		"rle_manifest_default_invalid",
		fmt.Sprintf("Set %s to a positive value.", fieldName),
	)
}

func validateFinitePositiveFloat(value *float64, fieldName string) error {
	if value == nil || (!math.IsNaN(*value) && !math.IsInf(*value, 0) && *value > 0) {
		return nil
	}
	return localError(
		fmt.Sprintf("%s must be a finite value greater than zero.", fieldName),
		"rle_manifest_default_invalid",
		fmt.Sprintf("Set %s to a finite positive value.", fieldName),
	)
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
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
