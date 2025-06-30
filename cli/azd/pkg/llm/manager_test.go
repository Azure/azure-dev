// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLlmConfig(t *testing.T) {
	tests := []struct {
		name         string
		envVars      map[string]string
		expectedType LlmType
		expectErr    bool
	}{
		{
			name:         "Default to local Ollama",
			envVars:      map[string]string{},
			expectedType: LlmTypeOllama,
			expectErr:    false,
		},
		{
			name: "Use Ollama when AZD_LLM_TYPE=ollama",
			envVars: map[string]string{
				"AZD_LLM_TYPE": "ollama",
			},
			expectedType: LlmTypeOllama,
			expectErr:    false,
		},
		{
			name: "Use Azure OpenAI when AZD_LLM_TYPE=azure",
			envVars: map[string]string{
				"AZD_LLM_TYPE": "azure",
				keyEnvVar:      "test-key",
				urlEnvVar:      "https://test.openai.azure.com/",
				versionEnvVar:  "2023-05-15",
				modelEnvVar:    "gpt-35-turbo",
			},
			expectedType: LlmTypeOpenAIAzure,
			expectErr:    false,
		},
		{
			name: "Error on invalid LLM type",
			envVars: map[string]string{
				"AZD_LLM_TYPE": "invalid",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(innerTest *testing.T) {

			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			info, err := LlmConfig()
			if tt.expectErr {
				require.Error(innerTest, err)
				return
			}

			require.NoError(innerTest, err)
			require.Equal(innerTest, tt.expectedType, info.Type, "Expected LLM type does not match")
		})
	}
}

func TestLlmClient(t *testing.T) {
	tests := []struct {
		name      string
		info      InfoResponse
		expectErr bool
		env       map[string]string
	}{
		{
			name: "Create Ollama client",
			info: InfoResponse{
				Type: LlmTypeOllama,
				Model: LlmModel{
					Name: "llama2",
				},
			},
			expectErr: false,
		},
		{
			name: "Create Azure OpenAI client",
			info: InfoResponse{
				Type: LlmTypeOpenAIAzure,
				Model: LlmModel{
					Name:    "gpt-35-turbo",
					Version: "2023-05-15",
				},
				Url: "https://test.openai.azure.com/",
			},
			expectErr: false,
			env: map[string]string{
				keyEnvVar: "test-key",
			},
		},
		{
			name: "Error on invalid LLM type",
			info: InfoResponse{
				Type: "invalid",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			client, err := LlmClient(tt.info)
			if tt.expectErr {
				require.Error(t, err)
				require.Equal(t, Client{}, client, "Expected empty client on error")
				require.Nil(t, client.Model, "Expected nil Model on error")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, client)
		})
	}
}
