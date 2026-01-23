// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCreateFineTuningRequestConfig_ValidMinimal(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata", "valid_minimal.yaml"))
	require.NoError(t, err)
	require.NotNil(t, config)

	require.Equal(t, "gpt-4o-mini", config.BaseModel)
	require.Equal(t, "local:./data/training.jsonl", config.TrainingFile)
}

func TestParseCreateFineTuningRequestConfig_ValidComplete(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata", "valid_complete.yaml"))
	require.NoError(t, err)
	require.NotNil(t, config)

	require.Equal(t, "gpt-4o-mini", config.BaseModel)
	require.Equal(t, "local:./data/training.jsonl", config.TrainingFile)
	require.NotNil(t, config.ValidationFile)
	require.Equal(t, "local:./data/validation.jsonl", *config.ValidationFile)
	require.NotNil(t, config.Suffix)
	require.Equal(t, "my-custom-model", *config.Suffix)
	require.NotNil(t, config.Seed)
	require.Equal(t, int64(42), *config.Seed)
	require.NotNil(t, config.Metadata)
	require.Equal(t, "test-project", config.Metadata["project"])
	require.Equal(t, "supervised", config.Method.Type)
	require.NotNil(t, config.Method.Supervised)
}

func TestParseCreateFineTuningRequestConfig_ValidDPO(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata", "valid_dpo.yaml"))
	require.NoError(t, err)
	require.NotNil(t, config)

	require.Equal(t, "gpt-4", config.BaseModel)
	require.Equal(t, "dpo", config.Method.Type)
	require.NotNil(t, config.Method.DPO)
	require.NotNil(t, config.Method.DPO.Hyperparameters)
}

func TestParseCreateFineTuningRequestConfig_ValidReinforcement(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata", "valid_reinforcement.yaml"))
	require.NoError(t, err)
	require.NotNil(t, config)

	require.Equal(t, "gpt-4", config.BaseModel)
	require.Equal(t, "reinforcement", config.Method.Type)
	require.NotNil(t, config.Method.Reinforcement)
	require.NotNil(t, config.Method.Reinforcement.Grader)
}

func TestParseCreateFineTuningRequestConfig_ValidWithIntegrations(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata", "valid_with_integrations.yaml"))
	require.NoError(t, err)
	require.NotNil(t, config)

	require.Len(t, config.Integrations, 1)
	require.Equal(t, "wandb", config.Integrations[0].Type)
	require.NotNil(t, config.Integrations[0].Config)
}

func TestParseCreateFineTuningRequestConfig_InvalidMissingModel(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata", "invalid_missing_model.yaml"))
	require.Error(t, err)
	require.Nil(t, config)
	require.Contains(t, err.Error(), "model is required")
}

func TestParseCreateFineTuningRequestConfig_InvalidMissingTrainingFile(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata", "invalid_missing_training_file.yaml"))
	require.Error(t, err)
	require.Nil(t, config)
	require.Contains(t, err.Error(), "training_file is required")
}

func TestParseCreateFineTuningRequestConfig_InvalidMethodType(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata", "invalid_method_type.yaml"))
	require.Error(t, err)
	require.Nil(t, config)
	require.Contains(t, err.Error(), "invalid method type")
}

func TestParseCreateFineTuningRequestConfig_InvalidSuffixTooLong(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata", "invalid_suffix_too_long.yaml"))
	require.Error(t, err)
	require.Nil(t, config)
	require.Contains(t, err.Error(), "suffix exceeds maximum length")
}

func TestParseCreateFineTuningRequestConfig_InvalidIntegrationMissingType(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata", "invalid_integration_missing_type.yaml"))
	require.Error(t, err)
	require.Nil(t, config)
	require.Contains(t, err.Error(), "integration type is required")
}

func TestParseCreateFineTuningRequestConfig_InvalidIntegrationMissingConfig(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata", "invalid_integration_missing_config.yaml"))
	require.Error(t, err)
	require.Nil(t, config)
	require.Contains(t, err.Error(), "requires 'config' block")
}

func TestParseCreateFineTuningRequestConfig_InvalidSupervisedMissingConfig(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata", "invalid_supervised_missing_config.yaml"))
	require.Error(t, err)
	require.Nil(t, config)
	require.Contains(t, err.Error(), "supervised method requires 'supervised' configuration block")
}

func TestParseCreateFineTuningRequestConfig_InvalidYAMLSyntax(t *testing.T) {
	// Uses .txt extension to avoid IDE YAML linter errors on intentionally malformed content
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata", "invalid_yaml_syntax.txt"))
	require.Error(t, err)
	require.Nil(t, config)
	require.Contains(t, err.Error(), "failed to parse YAML")
}

func TestParseCreateFineTuningRequestConfig_FileNotFound(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata", "nonexistent.yaml"))
	require.Error(t, err)
	require.Nil(t, config)
	require.Contains(t, err.Error(), "failed to read config file")
}

func TestParseCreateFineTuningRequestConfig_EmptyFilePath(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig("")
	require.Error(t, err)
	require.Nil(t, config)
}

func TestParseCreateFineTuningRequestConfig_DirectoryInsteadOfFile(t *testing.T) {
	config, err := ParseCreateFineTuningRequestConfig(filepath.Join("testdata"))
	require.Error(t, err)
	require.Nil(t, config)
}

func TestParseCreateFineTuningRequestConfig_TempFile(t *testing.T) {
	// Create a temporary valid YAML file
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "temp_config.yaml")

	content := `model: gpt-4o-mini
training_file: file-abc123
`
	err := os.WriteFile(tempFile, []byte(content), 0644)
	require.NoError(t, err)

	config, err := ParseCreateFineTuningRequestConfig(tempFile)
	require.NoError(t, err)
	require.NotNil(t, config)
	require.Equal(t, "gpt-4o-mini", config.BaseModel)
	require.Equal(t, "file-abc123", config.TrainingFile)
}

func TestParseCreateFineTuningRequestConfig_EmptyFile(t *testing.T) {
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "empty_config.yaml")

	err := os.WriteFile(tempFile, []byte(""), 0644)
	require.NoError(t, err)

	config, err := ParseCreateFineTuningRequestConfig(tempFile)
	require.Error(t, err)
	require.Nil(t, config)
	// Empty YAML results in empty struct, which fails validation
	require.Contains(t, err.Error(), "model is required")
}

func TestParseCreateFineTuningRequestConfig_WhitespaceOnlyFile(t *testing.T) {
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "whitespace_config.yaml")

	err := os.WriteFile(tempFile, []byte("   \n\n   \t\t\n"), 0644)
	require.NoError(t, err)

	config, err := ParseCreateFineTuningRequestConfig(tempFile)
	require.Error(t, err)
	require.Nil(t, config)
}

func TestParseCreateFineTuningRequestConfig_YAMLWithComments(t *testing.T) {
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "commented_config.yaml")

	content := `# This is a comment
model: gpt-4o-mini  # Model name
# Another comment
training_file: file-abc123
# Final comment
`
	err := os.WriteFile(tempFile, []byte(content), 0644)
	require.NoError(t, err)

	config, err := ParseCreateFineTuningRequestConfig(tempFile)
	require.NoError(t, err)
	require.NotNil(t, config)
	require.Equal(t, "gpt-4o-mini", config.BaseModel)
}

func TestParseCreateFineTuningRequestConfig_ExtraFields(t *testing.T) {
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "extra_fields_config.yaml")

	content := `model: gpt-4o-mini
training_file: file-abc123
unknown_field: some_value
another_unknown: 123
`
	err := os.WriteFile(tempFile, []byte(content), 0644)
	require.NoError(t, err)

	// Should parse successfully, ignoring unknown fields
	config, err := ParseCreateFineTuningRequestConfig(tempFile)
	require.NoError(t, err)
	require.NotNil(t, config)
	require.Equal(t, "gpt-4o-mini", config.BaseModel)
}
