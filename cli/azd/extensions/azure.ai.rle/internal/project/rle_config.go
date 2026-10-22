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
	maxHarnessInvocationURLLen      = 2048
	maxRleModelNameLength           = 256
	maxRleRendererNameLength        = 256
	maxRleModelResponseFieldLength  = 128
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
//
// Model, Train, and Rollout are reusable, version-scoped defaults declared at the top
// level ([model], [train], [rollout]) rather than nested under a "defaults" table: they
// are what train and (eventually) eval actually consume, not metadata about the manifest.
type RleConfig struct {
	SchemaVersion *string             `toml:"schema_version,omitempty"`
	Rle           RleManifest         `toml:"rle"`
	Harness       RleHarnessManifest  `toml:"harness"`
	Model         *RleModelDefaults   `toml:"model,omitempty"`
	Train         *RleTrainDefaults   `toml:"train,omitempty"`
	Rollout       *RleRolloutDefaults `toml:"rollout,omitempty"`
}

// RleManifest uses the control-plane field names so the [rle] table maps directly to an RLE release.
// It carries pure identity only -- name and version -- plus ImageTag below; everything about
// how RLE calls the environment lives in [harness] instead.
type RleManifest struct {
	Name    string `toml:"name"`
	Version string `toml:"version"`

	// ImageTag pins the harness container independently of Version. A Harness
	// environment can be republished against the same, unchanged image, so the
	// two move apart. The CLI does not build or push images -- publish takes the
	// image path as an argument -- but the field has to be declared, because the
	// manifest is parsed in strict mode and an environment whose build scripts
	// pin a tag would otherwise fail every command with a schema error.
	ImageTag *string `toml:"imageTag,omitempty"`
}

// RleHarnessManifest is the [harness] table: how RLE calls this environment. Type and Subtype
// apply to every environment (Gym included); AgentName/AgentVersion/InvocationURL are
// Harness-type-only identity fields, left unset (and rejected) for Gym.
type RleHarnessManifest struct {
	Type    RleType    `toml:"type"`
	Subtype RleSubtype `toml:"subtype"`

	AgentName     *string `toml:"agent_name,omitempty"`
	AgentVersion  *string `toml:"agent_version,omitempty"`
	InvocationURL *string `toml:"invocation_url,omitempty"`
}

// RleModelDefaults contains the optional model and renderer selection.
type RleModelDefaults struct {
	Name         *string `toml:"name,omitempty" json:"name,omitempty"`
	RendererName *string `toml:"renderer_name,omitempty" json:"renderer_name,omitempty"`
}

// RleTrainDefaults contains the version-scoped recipe training defaults.
type RleTrainDefaults struct {
	Grpo *RleGrpoDefaults `toml:"grpo,omitempty" json:"grpo,omitempty"`
}

// RleRolloutDefaults contains the version-scoped rollout defaults consumed by RLE's
// server-owned policy loop. Sampling and payload-size ceilings (completion tokens,
// sequence length, vocab size, timeouts, response size) are not settable here: they are
// hardcoded/configured server-side and may be exposed in the manifest later once there is
// user feedback on which ones are actually needed per environment.
type RleRolloutDefaults struct {
	GymOpenEnv *RleGymOpenEnvDefaults `toml:"gym_openenv,omitempty" json:"gym_openenv,omitempty"`
}

// RleGymOpenEnvDefaults is the Gym/OpenEnv contract between an environment and RLE's
// server-owned policy loop.
type RleGymOpenEnvDefaults struct {
	// ModelResponseField names the action property that receives the model completion's
	// content verbatim. It must be a property the environment already declares in its own
	// action model, so there is no default.
	ModelResponseField *string `toml:"model_response_field,omitempty" json:"model_response_field,omitempty"`
}

// RleGrpoDefaults retains recipe-level GRPO settings without aliasing Training Jobs batch size.
type RleGrpoDefaults struct {
	LearningRate   *float64 `toml:"learning_rate,omitempty" json:"learning_rate,omitempty"`
	GroupSize      *int     `toml:"group_size,omitempty" json:"group_size,omitempty"`
	GroupsPerBatch *int     `toml:"groups_per_batch,omitempty" json:"groups_per_batch,omitempty"`
	LoraRank       *int     `toml:"lora_rank,omitempty" json:"lora_rank,omitempty"`
	NumEpochs      *int     `toml:"num_epochs,omitempty" json:"num_epochs,omitempty"`
	MaxSteps       *int     `toml:"max_steps,omitempty" json:"max_steps,omitempty"`
	EvalEvery      *int     `toml:"eval_every,omitempty" json:"eval_every,omitempty"`
	SaveEvery      *int     `toml:"save_every,omitempty" json:"save_every,omitempty"`

	RemoveConstantRewardGroups *bool `toml:"remove_constant_reward_groups,omitempty" json:"remove_constant_reward_groups,omitempty"`

	SamplingSeed *int `toml:"sampling_seed,omitempty" json:"sampling_seed,omitempty"`

	MinTrainableGroupMembers *int `toml:"min_trainable_group_members,omitempty" json:"min_trainable_group_members,omitempty"`
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

// ReadRleConfig reads rle.toml from dir without normalizing it.
func ReadRleConfig(dir string) (RleConfig, error) {
	path := filepath.Join(dir, RleConfigFile)
	// #nosec G304 -- dir is the selected project directory and the file name is fixed.
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
	return config, nil
}

// LoadRleConfig reads and validates rle.toml from dir.
func LoadRleConfig(dir string) (RleConfig, error) {
	config, err := ReadRleConfig(dir)
	if err != nil {
		return RleConfig{}, err
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
	// #nosec G306 -- rle.toml is a project manifest intended to be readable by project tooling.
	return os.WriteFile(filepath.Join(dir, RleConfigFile), append(data, '\n'), 0644)
}

// NormalizeRleConfig validates the manifest against the RLE control-plane type contract.
func NormalizeRleConfig(config RleConfig) (RleConfig, error) {
	manifest := config.Rle
	harness := config.Harness

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

	harness.Type, err = normalizeRleType(harness.Type)
	if err != nil {
		return RleConfig{}, err
	}
	harness.Subtype, err = normalizeRleSubtype(harness.Subtype)
	if err != nil {
		return RleConfig{}, err
	}

	switch harness.Type {
	case RleTypeGym:
		if harness.Subtype != RleSubtypeOpenEnv {
			return RleConfig{}, localError(
				"Only the OpenEnv subtype is supported when harness.type is Gym.",
				"rle_manifest_type_configuration_invalid",
				`Set harness.type = "Gym" and harness.subtype = "OpenEnv".`,
			)
		}
		if harness.AgentName != nil || harness.AgentVersion != nil || harness.InvocationURL != nil {
			return RleConfig{}, localError(
				"agent_name, agent_version, and invocation_url are allowed only when harness.type is Harness.",
				"rle_manifest_type_configuration_invalid",
				"Remove harness configuration from this Gym: OpenEnv manifest.",
			)
		}
		harness = RleHarnessManifest{Type: harness.Type, Subtype: harness.Subtype}
	case RleTypeHarness:
		harnessAgentName := harness.AgentName
		harnessAgentVersion := harness.AgentVersion
		harnessInvocationURL := harness.InvocationURL
		switch harness.Subtype {
		case RleSubtypeHostedAgent:
			if harnessInvocationURL != nil {
				return RleConfig{}, localError(
					"invocation_url must be omitted when harness.subtype is HostedAgent.",
					"rle_manifest_type_configuration_invalid",
					"Remove invocation_url or set harness.subtype to BYOH.",
				)
			}
			agentName, err := normalizeRequiredAgentField(
				harnessAgentName,
				"agent_name",
				maxAgentNameLength,
				"rle_agent_name_required",
			)
			if err != nil {
				return RleConfig{}, err
			}
			agentVersion, err := normalizeRequiredAgentField(
				harnessAgentVersion,
				"agent_version",
				maxAgentVersionLen,
				"rle_agent_version_required",
			)
			if err != nil {
				return RleConfig{}, err
			}
			if !isValidHostedAgentVersion(agentVersion) {
				return RleConfig{}, localError(
					"agent_version must be a positive integer or a 'draft-{positive-unix-timestamp}' value.",
					"rle_agent_version_invalid",
					`Use a value such as "12" or "draft-1767225600".`,
				)
			}
			harness = RleHarnessManifest{
				Type:         harness.Type,
				Subtype:      harness.Subtype,
				AgentName:    &agentName,
				AgentVersion: &agentVersion,
			}
		case RleSubtypeBYOH:
			if harnessAgentName != nil || harnessAgentVersion != nil {
				return RleConfig{}, localError(
					"agent_name and agent_version must be omitted when harness.subtype is BYOH.",
					"rle_manifest_type_configuration_invalid",
					"Remove the Hosted Agent fields or set harness.subtype to HostedAgent.",
				)
			}
			invocationURL, err := normalizeHarnessInvocationURL(harnessInvocationURL)
			if err != nil {
				return RleConfig{}, err
			}
			harness = RleHarnessManifest{
				Type:          harness.Type,
				Subtype:       harness.Subtype,
				InvocationURL: &invocationURL,
			}
		default:
			return RleConfig{}, localError(
				"Only the HostedAgent and BYOH subtypes are supported when harness.type is Harness.",
				"rle_manifest_type_configuration_invalid",
				`Set harness.subtype to "HostedAgent" or "BYOH".`,
			)
		}
	default:
		return RleConfig{}, localError(
			"harness.type must be Gym or Harness.",
			"rle_manifest_type_invalid",
			`Set harness.type to "Gym" or "Harness".`,
		)
	}

	schemaVersion, model, train, rollout, err := normalizeRleManifestDefaults(
		config.SchemaVersion,
		config.Model,
		config.Train,
		config.Rollout,
	)
	if err != nil {
		return RleConfig{}, err
	}

	config.SchemaVersion = schemaVersion
	config.Rle = manifest
	config.Harness = harness
	config.Model = model
	config.Train = train
	config.Rollout = rollout
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

func normalizeRleManifestDefaults(
	schemaVersion *string,
	model *RleModelDefaults,
	train *RleTrainDefaults,
	rollout *RleRolloutDefaults,
) (*string, *RleModelDefaults, *RleTrainDefaults, *RleRolloutDefaults, error) {
	normalizedSchemaVersion, err := normalizeRleSchemaVersion(
		schemaVersion,
		model != nil || train != nil || rollout != nil,
	)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	normalizedModel, err := normalizeRleModelDefaults(model)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	normalizedTrain, err := normalizeRleTrainDefaults(train)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	normalizedRollout, err := normalizeRleRolloutDefaults(rollout)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return normalizedSchemaVersion, normalizedModel, normalizedTrain, normalizedRollout, nil
}

func normalizeRleSchemaVersion(value *string, required bool) (*string, error) {
	if value == nil {
		if required {
			return nil, localError(
				"schema_version is required when model, train, or rollout settings are supplied.",
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

func normalizeRleModelDefaults(value *RleModelDefaults) (*RleModelDefaults, error) {
	if value == nil {
		return nil, nil
	}

	name, err := normalizeOptionalRleString(value.Name, "model.name", maxRleModelNameLength)
	if err != nil {
		return nil, err
	}
	rendererName, err := normalizeOptionalRleString(
		value.RendererName,
		"model.renderer_name",
		maxRleRendererNameLength,
	)
	if err != nil {
		return nil, err
	}
	return &RleModelDefaults{Name: name, RendererName: rendererName}, nil
}

func normalizeRleTrainDefaults(value *RleTrainDefaults) (*RleTrainDefaults, error) {
	if value == nil {
		return nil, nil
	}

	grpo, err := normalizeRleGrpoDefaults(value.Grpo)
	if err != nil {
		return nil, err
	}
	return &RleTrainDefaults{Grpo: grpo}, nil
}

func normalizeRleRolloutDefaults(value *RleRolloutDefaults) (*RleRolloutDefaults, error) {
	if value == nil {
		return nil, nil
	}

	gymOpenEnv, err := normalizeRleGymOpenEnvDefaults(value.GymOpenEnv)
	if err != nil {
		return nil, err
	}
	return &RleRolloutDefaults{
		GymOpenEnv: gymOpenEnv,
	}, nil
}

func normalizeRleGymOpenEnvDefaults(value *RleGymOpenEnvDefaults) (*RleGymOpenEnvDefaults, error) {
	if value == nil {
		return nil, nil
	}

	modelResponseField, err := normalizeOptionalRleString(
		value.ModelResponseField,
		"rollout.gym_openenv.model_response_field",
		maxRleModelResponseFieldLength,
	)
	if err != nil {
		return nil, err
	}
	return &RleGymOpenEnvDefaults{ModelResponseField: modelResponseField}, nil
}

func normalizeRleGrpoDefaults(value *RleGrpoDefaults) (*RleGrpoDefaults, error) {
	if value == nil {
		return nil, nil
	}

	for _, parameter := range []struct {
		value *int
		name  string
	}{
		{value.GroupSize, "train.grpo.group_size"},
		{value.GroupsPerBatch, "train.grpo.groups_per_batch"},
		{value.LoraRank, "train.grpo.lora_rank"},
		{value.NumEpochs, "train.grpo.num_epochs"},
		{value.MaxSteps, "train.grpo.max_steps"},
		{value.EvalEvery, "train.grpo.eval_every"},
		{value.SaveEvery, "train.grpo.save_every"},
		{value.SamplingSeed, "train.grpo.sampling_seed"},
		{value.MinTrainableGroupMembers, "train.grpo.min_trainable_group_members"},
	} {
		if err := validatePositiveInt(parameter.value, parameter.name); err != nil {
			return nil, err
		}
	}
	if err := validateFinitePositiveFloat(value.LearningRate, "train.grpo.learning_rate"); err != nil {
		return nil, err
	}
	for _, parameter := range []struct {
		value *float64
		name  string
	}{} {
		if err := validateFiniteRate(parameter.value, parameter.name); err != nil {
			return nil, err
		}
	}
	return &RleGrpoDefaults{
		LearningRate:               cloneFloat64(value.LearningRate),
		GroupSize:                  cloneInt(value.GroupSize),
		GroupsPerBatch:             cloneInt(value.GroupsPerBatch),
		LoraRank:                   cloneInt(value.LoraRank),
		NumEpochs:                  cloneInt(value.NumEpochs),
		MaxSteps:                   cloneInt(value.MaxSteps),
		EvalEvery:                  cloneInt(value.EvalEvery),
		SaveEvery:                  cloneInt(value.SaveEvery),
		RemoveConstantRewardGroups: cloneBool(value.RemoveConstantRewardGroups),
		SamplingSeed:               cloneInt(value.SamplingSeed),
		MinTrainableGroupMembers:   cloneInt(value.MinTrainableGroupMembers),
	}, nil
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

func validateFiniteRate(value *float64, fieldName string) error {
	if value == nil || (!math.IsNaN(*value) && !math.IsInf(*value, 0) && *value > 0 && *value <= 1) {
		return nil
	}
	return localError(
		fmt.Sprintf("%s must be a finite value greater than zero and at most one.", fieldName),
		"rle_manifest_default_invalid",
		fmt.Sprintf("Set %s to a finite value in (0, 1].", fieldName),
	)
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneBool(value *bool) *bool {
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
			"harness.type must be Gym or Harness.",
			"rle_manifest_type_invalid",
			`Set harness.type to "Gym" or "Harness".`,
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
			"harness.subtype must be OpenEnv, HostedAgent, or BYOH.",
			"rle_manifest_subtype_invalid",
			`Set harness.subtype to "OpenEnv", "HostedAgent", or "BYOH".`,
		)
	}
}

func normalizeRequiredAgentField(value *string, fieldName string, maximumLength int, requiredCode string) (string, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "", localError(
			fmt.Sprintf("%s is required when harness.subtype is HostedAgent.", fieldName),
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

func normalizeHarnessInvocationURL(value *string) (string, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "", localError(
			"invocation_url is required when harness.subtype is BYOH.",
			"rle_harness_invocation_url_required",
			"Add an absolute HTTPS invocation_url to rle.toml.",
		)
	}
	normalized := strings.TrimSpace(*value)
	if utf16Length(normalized) > maxHarnessInvocationURLLen {
		return "", localError(
			fmt.Sprintf("invocation_url must be at most %d characters.", maxHarnessInvocationURLLen),
			"rle_harness_invocation_url_invalid",
			"Use a shorter absolute HTTPS invocation_url without credentials, query, or fragment.",
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
			"invocation_url must be an absolute HTTPS URL without credentials, query, or fragment.",
			"rle_harness_invocation_url_invalid",
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
	if after, ok := strings.CutPrefix(value, "draft-"); ok {
		return isPositiveInt64(after)
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
	for index := range 3 {
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
