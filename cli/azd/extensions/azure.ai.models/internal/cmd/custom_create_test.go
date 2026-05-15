// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"azure.ai.models/pkg/models"

	"github.com/spf13/cobra"
)

func TestBuildDerivedModelURI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "short model name",
			input:    "FW-GPT-OSS-120B",
			expected: "azureml://registries/azureml-fireworks/models/FW-GPT-OSS-120B/versions/1",
		},
		{
			name:     "full azureml URI passthrough",
			input:    "azureml://registries/custom-reg/models/MyModel/versions/3",
			expected: "azureml://registries/custom-reg/models/MyModel/versions/3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildDerivedModelURI(tt.input)
			if got != tt.expected {
				t.Errorf("buildDerivedModelURI(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractBaseModelName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain name passthrough",
			input:    "FW-DeepSeek-V3.1",
			expected: "FW-DeepSeek-V3.1",
		},
		{
			name:     "extracts from azureml URI",
			input:    "azureml://registries/azureml-fireworks/models/FW-GPT-OSS-120B/versions/1",
			expected: "FW-GPT-OSS-120B",
		},
		{
			name:     "malformed URI returns as-is",
			input:    "azureml://registries/reg/nomodels",
			expected: "azureml://registries/reg/nomodels",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractBaseModelName(tt.input)
			if got != tt.expected {
				t.Errorf("extractBaseModelName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractVersionFromURI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "extracts version",
			input:    "azureml://registries/azureml-fireworks/models/FW-Qwen3-14B/versions/2",
			expected: "2",
		},
		{
			name:     "non-azureml returns empty",
			input:    "FW-GPT-OSS-120B",
			expected: "",
		},
		{
			name:     "no versions segment returns empty",
			input:    "azureml://registries/reg/models/MyModel",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractVersionFromURI(tt.input)
			if got != tt.expected {
				t.Errorf("extractVersionFromURI(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// newTestCmd creates a cobra command with lora flags registered for testing buildLoRAConfig.
func newTestCmd(flags *customCreateFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().IntVar(&flags.LoRARank, "lora-rank", 0, "")
	cmd.Flags().IntVar(&flags.LoRAAlpha, "lora-alpha", 0, "")
	cmd.Flags().StringVar(&flags.LoRATargetModules, "lora-target-modules", "", "")
	cmd.Flags().Float64Var(&flags.LoRADropout, "lora-dropout", 0, "")
	return cmd
}

func TestBuildLoRAConfig(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErr     string
		wantRank    int
		wantAlpha   int
		wantModules []string
		wantDropout *float64
	}{
		{
			name:    "missing rank",
			args:    []string{"--lora-alpha", "32"},
			wantErr: "--lora-rank is required",
		},
		{
			name:    "missing alpha",
			args:    []string{"--lora-rank", "16"},
			wantErr: "--lora-alpha is required",
		},
		{
			name:      "rank and alpha only",
			args:      []string{"--lora-rank", "16", "--lora-alpha", "32"},
			wantRank:  16,
			wantAlpha: 32,
		},
		{
			name:        "all fields",
			args:        []string{"--lora-rank", "8", "--lora-alpha", "16", "--lora-target-modules", "q_proj,v_proj", "--lora-dropout", "0.05"},
			wantRank:    8,
			wantAlpha:   16,
			wantModules: []string{"q_proj", "v_proj"},
			wantDropout: new(0.05),
		},
		{
			name:    "empty entry in target modules",
			args:    []string{"--lora-rank", "16", "--lora-alpha", "32", "--lora-target-modules", "q_proj,,v_proj"},
			wantErr: "empty entry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := &customCreateFlags{}
			cmd := newTestCmd(flags)
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}

			got, err := buildLoRAConfig(cmd, flags)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Rank == nil || *got.Rank != tt.wantRank {
				t.Errorf("Rank = %v, want %d", got.Rank, tt.wantRank)
			}
			if got.Alpha == nil || *got.Alpha != tt.wantAlpha {
				t.Errorf("Alpha = %v, want %d", got.Alpha, tt.wantAlpha)
			}
			if len(tt.wantModules) > 0 {
				if len(got.TargetModules) != len(tt.wantModules) {
					t.Errorf("TargetModules = %v, want %v", got.TargetModules, tt.wantModules)
				}
				for i := range tt.wantModules {
					if i < len(got.TargetModules) && got.TargetModules[i] != tt.wantModules[i] {
						t.Errorf("TargetModules[%d] = %q, want %q", i, got.TargetModules[i], tt.wantModules[i])
					}
				}
			}
			if tt.wantDropout != nil {
				if got.Dropout == nil || *got.Dropout != *tt.wantDropout {
					t.Errorf("Dropout = %v, want %v", got.Dropout, *tt.wantDropout)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestLoRAConfigJSONRoundTrip(t *testing.T) {
	original := &models.CustomModel{
		Name:       "test-lora-adapter",
		Version:    "1",
		WeightType: "LoRA",
		LoRAConfig: &models.LoRAConfig{
			Rank:          new(16),
			Alpha:         new(32),
			TargetModules: []string{"q_proj", "v_proj", "k_proj", "o_proj"},
			Dropout:       new(0.05),
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded models.CustomModel
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.LoRAConfig == nil {
		t.Fatal("LoRAConfig is nil after round-trip")
	}
	if decoded.LoRAConfig.Rank == nil || *decoded.LoRAConfig.Rank != 16 {
		t.Errorf("Rank = %v, want 16", decoded.LoRAConfig.Rank)
	}
	if decoded.LoRAConfig.Alpha == nil || *decoded.LoRAConfig.Alpha != 32 {
		t.Errorf("Alpha = %v, want 32", decoded.LoRAConfig.Alpha)
	}
	if len(decoded.LoRAConfig.TargetModules) != 4 {
		t.Errorf("TargetModules len = %d, want 4", len(decoded.LoRAConfig.TargetModules))
	}
	if decoded.LoRAConfig.Dropout == nil || *decoded.LoRAConfig.Dropout != 0.05 {
		t.Errorf("Dropout = %v, want 0.05", decoded.LoRAConfig.Dropout)
	}
}

func TestLoRAConfigJSONOmittedWhenNil(t *testing.T) {
	model := &models.CustomModel{
		Name:       "full-weight-model",
		Version:    "1",
		WeightType: "FullWeight",
	}

	data, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	jsonStr := string(data)
	if contains(jsonStr, "loraConfig") {
		t.Errorf("loraConfig should be omitted for FullWeight models, got: %s", jsonStr)
	}
}
