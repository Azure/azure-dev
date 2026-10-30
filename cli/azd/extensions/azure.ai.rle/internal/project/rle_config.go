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
	"slices"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/pelletier/go-toml/v2"
)

const (
	RleConfigFile                   = "rle.toml"
	DefaultRleVersion               = "1.0.0"
	CurrentRleManifestSchemaVersion = "1.0.0"
	maxHarnessEndpointLen           = 2048
	maxRleModelNameLength           = 256
	maxRleRendererNameLength        = 256
	maxRleModelResponseFieldLength  = 128
)

// HarnessType is the [harness] type: who drives the rollout loop.
type HarnessType string

const (
	// HarnessTypeGym is RLE's own rollout loop. It is the default when type is omitted.
	HarnessTypeGym HarnessType = "gym"
	// HarnessTypeBYOH is a caller-owned harness that RLE calls at harness.endpoint.
	HarnessTypeBYOH HarnessType = "byoh"
	// HarnessTypePackage is a named harness package (for example terminus-2 or opencode) that RLE
	// runs inside the task sandbox. It requires task.format = "harbor".
	HarnessTypePackage HarnessType = "package"
)

// TaskFormat is the [task] format: how the taskset is authored.
type TaskFormat string

const (
	// TaskFormatJSON is the default task format.
	TaskFormatJSON TaskFormat = "json"
	// TaskFormatHarbor is the Harbor task format (task.toml, instruction.md, environment/, tests/).
	TaskFormatHarbor TaskFormat = "harbor"
)

// HarnessProtocol is the wire protocol RLE uses to call a byoh harness endpoint.
type HarnessProtocol string

const (
	// HarnessProtocolResponses calls a Foundry hosted agent through the Responses API.
	HarnessProtocolResponses HarnessProtocol = "responses"
	// HarnessProtocolRleInvocation is RLE's own BYOH POST/poll contract.
	HarnessProtocolRleInvocation HarnessProtocol = "rle-invocation"
)

// RleConfig is the host-agnostic source configuration for one immutable RLE release.
//
// Model, Train, and Rollout are reusable, version-scoped defaults declared at the top
// level ([model], [train], [rollout]) rather than nested under a "defaults" table: they
// are what train and (eventually) eval actually consume, not metadata about the manifest.
type RleConfig struct {
	SchemaVersion *string             `toml:"schema_version,omitempty"`
	Rle           RleManifest         `toml:"rle"`
	Task          *RleTaskManifest    `toml:"task,omitempty"`
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

// RleTaskManifest is the [task] table and mirrors the RLE service's task object.
type RleTaskManifest struct {
	Format TaskFormat `toml:"format,omitempty"`
}

// RleHarnessManifest is the [harness] table and mirrors the RLE service's harness object.
// Endpoint and Protocol are byoh-only and must be omitted for gym. When Protocol is omitted
// for byoh, the service resolves it: responses for the caller's own Foundry project agent
// endpoint, otherwise rle-invocation.
type RleHarnessManifest struct {
	Type     HarnessType     `toml:"type"`
	Endpoint *string         `toml:"endpoint,omitempty"`
	Protocol HarnessProtocol `toml:"protocol,omitempty"`
	// Name selects the harness package and is valid only when Type is package.
	Name string `toml:"name,omitempty"`
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

// RleRolloutDefaults contains rollout configuration for registration and the resolved
// FT job snapshot. It is not a per-request Execute Rollout override.
// Sampling and payload-size ceilings (completion tokens,
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

// LoadRleConfig reads and validates rle.toml from dir.
func LoadRleConfig(dir string) (RleConfig, error) {
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
	if err := rejectLegacyHarnessKeys(data); err != nil {
		return RleConfig{}, err
	}
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

	harness, err = normalizeRleHarness(harness)
	if err != nil {
		return RleConfig{}, err
	}

	task, err := normalizeRleTask(config.Task, harness)
	if err != nil {
		return RleConfig{}, err
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
	config.Task = task
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
		value *int
		name  string
	}{
		{value.EvalEvery, "train.grpo.eval_every"},
		{value.SaveEvery, "train.grpo.save_every"},
		{value.SamplingSeed, "train.grpo.sampling_seed"},
	} {
		if err := validateRleInt(parameter.value, parameter.name, 0); err != nil {
			return nil, err
		}
	}
	if value.MinTrainableGroupMembers != nil && value.GroupSize != nil &&
		*value.MinTrainableGroupMembers > *value.GroupSize {
		return nil, localError(
			"train.grpo.min_trainable_group_members must not exceed train.grpo.group_size.",
			"rle_manifest_default_invalid",
			"Set min_trainable_group_members between 1 and group_size in [train.grpo] in rle.toml.",
		)
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
	return validateRleInt(value, fieldName, 1)
}

func validateRleInt(value *int, fieldName string, minimum int) error {
	if value == nil || (*value >= minimum && *value <= math.MaxInt32) {
		return nil
	}
	return localError(
		fmt.Sprintf("%s must be an integer between %d and %d.", fieldName, minimum, math.MaxInt32),
		"rle_manifest_default_invalid",
		fmt.Sprintf("Set %s in rle.toml to a value in that range.", fieldName),
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
		"Add a valid rle.toml, then run this command from the folder containing it.",
	)
}

func invalidRleConfigError(message string) error {
	return localError(
		message,
		"rle_manifest_invalid",
		"Fix rle.toml so it declares one valid RLE identity and type configuration.",
	)
}

func normalizeRleHarness(harness RleHarnessManifest) (RleHarnessManifest, error) {
	harnessType := harness.Type
	if harnessType == "" {
		harnessType = HarnessTypeGym
	}
	if harness.Name != "" && harnessType != HarnessTypePackage {
		return RleHarnessManifest{}, localError(
			"harness.name is allowed only when harness.type is package.",
			"rle_harness_name_not_supported",
			`Remove name, or set harness.type = "package".`,
		)
	}
	switch harnessType {
	case HarnessTypePackage:
		if strings.TrimSpace(harness.Name) == "" {
			return RleHarnessManifest{}, localError(
				"harness.name is required when harness.type is package.",
				"rle_harness_name_required",
				`Set harness.name to the harness package, for example name = "opencode".`,
			)
		}
		if harness.Endpoint != nil || harness.Protocol != "" {
			return RleHarnessManifest{}, localError(
				"harness.endpoint and harness.protocol are allowed only when harness.type is byoh.",
				"rle_manifest_type_configuration_invalid",
				`Remove endpoint and protocol, or set harness.type = "byoh".`,
			)
		}
		return RleHarnessManifest{Type: HarnessTypePackage, Name: strings.ToLower(strings.TrimSpace(harness.Name))}, nil
	case HarnessTypeGym:
		if harness.Endpoint != nil || harness.Protocol != "" {
			return RleHarnessManifest{}, localError(
				"harness.endpoint and harness.protocol are allowed only when harness.type is byoh.",
				"rle_manifest_type_configuration_invalid",
				`Remove endpoint and protocol, or set harness.type = "byoh".`,
			)
		}
		return RleHarnessManifest{Type: HarnessTypeGym}, nil
	case HarnessTypeBYOH:
		endpoint, err := normalizeHarnessEndpoint(harness.Endpoint)
		if err != nil {
			return RleHarnessManifest{}, err
		}
		protocol, err := normalizeHarnessProtocol(harness.Protocol)
		if err != nil {
			return RleHarnessManifest{}, err
		}
		return RleHarnessManifest{Type: HarnessTypeBYOH, Endpoint: &endpoint, Protocol: protocol}, nil
	default:
		suggestion := `Set harness.type to "gym", "byoh", or "package".`
		if strings.EqualFold(string(harnessType), "harness") {
			suggestion = `The Harness/HostedAgent/BYOH types were replaced: use type = "byoh" with an endpoint.`
		}
		return RleHarnessManifest{}, localError(
			fmt.Sprintf("harness.type must be gym, byoh, or package (lowercase), got %q.", string(harnessType)),
			"rle_manifest_type_invalid",
			suggestion,
		)
	}
}

func normalizeRleTask(task *RleTaskManifest, harness RleHarnessManifest) (*RleTaskManifest, error) {
	format := TaskFormatJSON
	if task != nil && task.Format != "" {
		format = task.Format
	}
	switch format {
	case TaskFormatJSON:
		if harness.Type == HarnessTypePackage {
			return nil, localError(
				`harness.type = "package" requires task.format = "harbor".`,
				"rle_harness_package_requires_harbor",
				`Add [task] format = "harbor", or use harness.type = "gym" or "byoh".`,
			)
		}
		return nil, nil
	case TaskFormatHarbor:
		if harness.Type != HarnessTypePackage {
			return nil, localError(
				`task.format = "harbor" requires harness.type = "package".`,
				"rle_task_harbor_requires_package",
				`Set [harness] type = "package" and name to a harness package, for example "terminus-2" or "opencode".`,
			)
		}
		return &RleTaskManifest{Format: TaskFormatHarbor}, nil
	}
	return nil, localError(
		fmt.Sprintf("task.format must be json or harbor (lowercase), got %q.", string(task.Format)),
		"rle_task_format_invalid",
		`Set task.format to "json" (default) or "harbor".`,
	)
}

func normalizeHarnessProtocol(value HarnessProtocol) (HarnessProtocol, error) {
	switch value {
	case "", HarnessProtocolResponses, HarnessProtocolRleInvocation:
		return value, nil
	default:
		return "", localError(
			fmt.Sprintf("harness.protocol must be responses or rle-invocation, got %q.", string(value)),
			"rle_harness_protocol_invalid",
			`Set harness.protocol to "responses" or "rle-invocation", or omit it to let the service resolve it.`,
		)
	}
}

// legacyHarnessKeys are the removed [harness] keys, mapped to the migration hint shown when
// one is found. Strict decoding alone would only report "strict mode: fields not in struct".
var legacyHarnessKeys = map[string]string{
	"subtype":        "Remove subtype and set type to gym or byoh.",
	"agent_name":     `Use type = "byoh" with endpoint = "https://<acct>.services.ai.azure.com/api/projects/<proj>/agents/<name>".`,
	"agentName":      `Use type = "byoh" with endpoint = "https://<acct>.services.ai.azure.com/api/projects/<proj>/agents/<name>".`,
	"agent_version":  "Remove agent_version; agent versions are no longer targetable. Use endpoint instead.",
	"agentVersion":   "Remove agentVersion; agent versions are no longer targetable. Use endpoint instead.",
	"invocation_url": "Rename invocation_url to endpoint.",
	"invocationUrl":  "Rename invocationUrl to endpoint.",
	"baseUrl":        "Rename baseUrl to endpoint.",
	"base_url":       "Rename base_url to endpoint.",
}

func rejectLegacyHarnessKeys(data []byte) error {
	var raw struct {
		Harness map[string]any `toml:"harness"`
	}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil // the strict decode reports syntax errors
	}
	keys := make([]string, 0, len(raw.Harness))
	for key := range raw.Harness {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if hint, legacy := legacyHarnessKeys[key]; legacy {
			return localError(
				fmt.Sprintf("harness.%s is no longer supported in %s.", key, RleConfigFile),
				"rle_manifest_legacy_harness_key",
				hint+` The [harness] table is now: type = "gym" | "byoh" | "package", endpoint = "<https url>", protocol = "responses" | "rle-invocation", name = "<package>".`,
			)
		}
	}
	return nil
}

func normalizeHarnessEndpoint(value *string) (string, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "", localError(
			"harness.endpoint is required when harness.type is byoh.",
			"rle_harness_endpoint_required",
			"Add an absolute HTTPS endpoint to the [harness] table in rle.toml.",
		)
	}
	normalized := strings.TrimSpace(*value)
	if utf16Length(normalized) > maxHarnessEndpointLen {
		return "", localError(
			fmt.Sprintf("harness.endpoint must be at most %d characters.", maxHarnessEndpointLen),
			"rle_harness_endpoint_invalid",
			"Use a shorter absolute HTTPS endpoint without credentials, query, or fragment.",
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
			"harness.endpoint must be an absolute HTTPS URL without credentials, query, or fragment.",
			"rle_harness_endpoint_invalid",
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
