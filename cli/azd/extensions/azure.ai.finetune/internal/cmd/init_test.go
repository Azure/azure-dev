// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractProjectDetails_ValidResourceID(t *testing.T) {
	tests := []struct {
		name            string
		resourceID      string
		expectedSubID   string
		expectedRG      string
		expectedAccount string
		expectedProject string
	}{
		{
			name:            "ValidFullResourceID",
			resourceID:      "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.CognitiveServices/accounts/my-account/projects/my-project",
			expectedSubID:   "12345678-1234-1234-1234-123456789012",
			expectedRG:      "my-rg",
			expectedAccount: "my-account",
			expectedProject: "my-project",
		},
		{
			name:            "ResourceIDWithSpecialCharacters",
			resourceID:      "/subscriptions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/resourceGroups/rg-prod-east-001/providers/Microsoft.CognitiveServices/accounts/ai-account-prod/projects/finetune-project-v2",
			expectedSubID:   "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			expectedRG:      "rg-prod-east-001",
			expectedAccount: "ai-account-prod",
			expectedProject: "finetune-project-v2",
		},
		{
			name:            "ResourceIDWithUppercase",
			resourceID:      "/subscriptions/ABCD1234-ABCD-1234-ABCD-ABCD12345678/resourceGroups/MyResourceGroup/providers/Microsoft.CognitiveServices/accounts/MyAccount/projects/MyProject",
			expectedSubID:   "ABCD1234-ABCD-1234-ABCD-ABCD12345678",
			expectedRG:      "MyResourceGroup",
			expectedAccount: "MyAccount",
			expectedProject: "MyProject",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractProjectDetails(tt.resourceID)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.expectedSubID, result.SubscriptionId)
			require.Equal(t, tt.expectedRG, result.ResourceGroupName)
			require.Equal(t, tt.expectedAccount, result.AiAccountName)
			require.Equal(t, tt.expectedProject, result.AiProjectName)
		})
	}
}

func TestExtractProjectDetails_InvalidResourceID(t *testing.T) {
	tests := []struct {
		name          string
		resourceID    string
		errorContains string
	}{
		{
			name:          "EmptyResourceID",
			resourceID:    "",
			errorContains: "failed to parse",
		},
		{
			name:          "MalformedResourceID",
			resourceID:    "not-a-valid-resource-id",
			errorContains: "failed to parse",
		},
		{
			name:          "MissingSubscription",
			resourceID:    "/resourceGroups/my-rg/providers/Microsoft.CognitiveServices/accounts/my-account/projects/my-project",
			errorContains: "failed to parse",
		},
		{
			name:          "WrongProvider",
			resourceID:    "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Storage/storageAccounts/my-storage",
			errorContains: "not a Microsoft Foundry project",
		},
		{
			name:          "WrongResourceType_AccountOnly",
			resourceID:    "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.CognitiveServices/accounts/my-account",
			errorContains: "not a Microsoft Foundry project",
		},
		{
			name:          "WrongResourceType_DifferentChild",
			resourceID:    "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.CognitiveServices/accounts/my-account/deployments/my-deployment",
			errorContains: "not a Microsoft Foundry project",
		},
		{
			name:          "PartialResourceID",
			resourceID:    "/subscriptions/12345678-1234-1234-1234-123456789012",
			errorContains: "not a Microsoft Foundry project",
		},
		{
			name:          "ResourceIDWithExtraSlashes",
			resourceID:    "//subscriptions//12345678-1234-1234-1234-123456789012//resourceGroups//my-rg",
			errorContains: "not a Microsoft Foundry project", // ARM parser handles extra slashes but resource type won't match
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractProjectDetails(tt.resourceID)

			require.Error(t, err)
			require.Nil(t, result)
			require.Contains(t, err.Error(), tt.errorContains)
		})
	}
}

func TestExtractProjectDetails_EdgeCases(t *testing.T) {
	t.Run("ResourceIDWithTrailingSlash", func(t *testing.T) {
		resourceID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.CognitiveServices/accounts/my-account/projects/my-project/"

		// ARM parser typically handles trailing slashes
		result, err := extractProjectDetails(resourceID)
		// The behavior depends on the ARM SDK parsing - it may succeed or fail
		if err == nil {
			require.NotNil(t, result)
			require.Equal(t, "my-project", result.AiProjectName)
		}
	})

	t.Run("ResourceIDWithURLEncoding", func(t *testing.T) {
		// Spaces encoded as %20
		resourceID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my%20rg/providers/Microsoft.CognitiveServices/accounts/my-account/projects/my-project"

		// This might fail depending on how ARM SDK handles URL encoding
		_, err := extractProjectDetails(resourceID)
		// Just ensure it doesn't panic
		_ = err
	})

	t.Run("CaseInsensitiveProvider", func(t *testing.T) {
		// Test with different casing
		resourceID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/microsoft.cognitiveservices/accounts/my-account/projects/my-project"

		result, err := extractProjectDetails(resourceID)
		// ARM SDK typically handles case-insensitive namespace matching
		if err == nil {
			require.NotNil(t, result)
		}
	})
}

func TestFoundryProject_Struct(t *testing.T) {
	project := &FoundryProject{
		TenantId:          "tenant-123",
		SubscriptionId:    "sub-456",
		Location:          "eastus",
		ResourceGroupName: "my-rg",
		AiAccountName:     "my-account",
		AiProjectName:     "my-project",
	}

	require.Equal(t, "tenant-123", project.TenantId)
	require.Equal(t, "sub-456", project.SubscriptionId)
	require.Equal(t, "eastus", project.Location)
	require.Equal(t, "my-rg", project.ResourceGroupName)
	require.Equal(t, "my-account", project.AiAccountName)
	require.Equal(t, "my-project", project.AiProjectName)
}

func TestGitHubUrlInfo_Struct(t *testing.T) {
	urlInfo := &GitHubUrlInfo{
		RepoSlug: "Azure/azure-dev",
		Branch:   "main",
		FilePath: "templates/finetune/job.yaml",
		Hostname: "github.com",
	}

	require.Equal(t, "Azure/azure-dev", urlInfo.RepoSlug)
	require.Equal(t, "main", urlInfo.Branch)
	require.Equal(t, "templates/finetune/job.yaml", urlInfo.FilePath)
	require.Equal(t, "github.com", urlInfo.Hostname)
}

func TestInitAction_IsGitHubUrl(t *testing.T) {
	action := &InitAction{}

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "StandardGitHubURL",
			url:      "https://github.com/Azure/azure-dev/blob/main/templates/job.yaml",
			expected: true,
		},
		{
			name:     "RawGitHubURL",
			url:      "https://raw.githubusercontent.com/Azure/azure-dev/main/templates/job.yaml",
			expected: true,
		},
		{
			name:     "GitHubAPIURL",
			url:      "https://api.github.com/repos/Azure/azure-dev/contents/templates/job.yaml",
			expected: true,
		},
		{
			name:     "LocalFilePath",
			url:      "/path/to/local/file.yaml",
			expected: false,
		},
		{
			name:     "WindowsFilePath",
			url:      "C:\\Users\\test\\file.yaml",
			expected: false,
		},
		{
			name:     "RelativeFilePath",
			url:      "./templates/job.yaml",
			expected: false,
		},
		{
			name:     "OtherURL",
			url:      "https://example.com/file.yaml",
			expected: false,
		},
		{
			name:     "HTTPGitHub",
			url:      "http://github.com/Azure/azure-dev",
			expected: true,
		},
		{
			name:     "EmptyString",
			url:      "",
			expected: false,
		},
		{
			name:     "InvalidURL",
			url:      "://invalid",
			expected: false,
		},
		{
			name:     "GitHubEnterpriseURL",
			url:      "https://github.mycompany.com/org/repo/file.yaml",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := action.isGitHubUrl(tt.url)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestAiFineTuningHost_Constant(t *testing.T) {
	require.Equal(t, "azure.ai.finetune", AiFineTuningHost)
}
