// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package show

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// showTypeFromLanguage
// ---------------------------------------------------------------------------

func TestShowTypeFromLanguage(t *testing.T) {
	tests := []struct {
		name     string
		language project.ServiceLanguageKind
		want     contracts.ShowType
	}{
		{
			name:     "none",
			language: project.ServiceLanguageNone,
			want:     contracts.ShowTypeNone,
		},
		{
			name:     "dotnet",
			language: project.ServiceLanguageDotNet,
			want:     contracts.ShowTypeDotNet,
		},
		{
			name:     "csharp",
			language: project.ServiceLanguageCsharp,
			want:     contracts.ShowTypeDotNet,
		},
		{
			name:     "fsharp",
			language: project.ServiceLanguageFsharp,
			want:     contracts.ShowTypeDotNet,
		},
		{
			name:     "python",
			language: project.ServiceLanguagePython,
			want:     contracts.ShowTypePython,
		},
		{
			name:     "typescript",
			language: project.ServiceLanguageTypeScript,
			want:     contracts.ShowTypeNode,
		},
		{
			name:     "javascript",
			language: project.ServiceLanguageJavaScript,
			want:     contracts.ShowTypeNode,
		},
		{
			name:     "java",
			language: project.ServiceLanguageJava,
			want:     contracts.ShowTypeJava,
		},
		{
			name:     "custom",
			language: project.ServiceLanguageCustom,
			want:     contracts.ShowTypeCustom,
		},
		{
			name:     "unknown extension language",
			language: project.ServiceLanguageKind("rust"),
			want:     contracts.ShowTypeNone,
		},
		{
			name:     "empty string same as none",
			language: project.ServiceLanguageKind(""),
			want:     contracts.ShowTypeNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := showTypeFromLanguage(tt.language)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// selectContainer
// ---------------------------------------------------------------------------

func TestSelectContainer(t *testing.T) {
	ptr := func(s string) *string { return &s }

	tests := []struct {
		name         string
		containers   []*armappcontainers.Container
		resourceName string
		wantName     *string // nil means expect nil return
	}{
		{
			name:         "empty slice returns nil",
			containers:   []*armappcontainers.Container{},
			resourceName: "app",
			wantName:     nil,
		},
		{
			name:         "nil slice returns nil",
			containers:   nil,
			resourceName: "app",
			wantName:     nil,
		},
		{
			name: "single container returned",
			containers: []*armappcontainers.Container{
				{Name: ptr("my-app")},
			},
			resourceName: "app",
			wantName:     ptr("my-app"),
		},
		{
			name: "single nil container returns nil",
			containers: []*armappcontainers.Container{
				nil,
			},
			resourceName: "app",
			wantName:     nil,
		},
		{
			name: "matches by resource name",
			containers: []*armappcontainers.Container{
				{Name: ptr("sidecar")},
				{Name: ptr("web-api")},
			},
			resourceName: "web-api",
			wantName:     ptr("web-api"),
		},
		{
			name: "matches by resource name case insensitive",
			containers: []*armappcontainers.Container{
				{Name: ptr("sidecar")},
				{Name: ptr("Web-Api")},
			},
			resourceName: "web-api",
			wantName:     ptr("Web-Api"),
		},
		{
			name: "matches main fallback",
			containers: []*armappcontainers.Container{
				{Name: ptr("sidecar")},
				{Name: ptr("main")},
			},
			resourceName: "unknown",
			wantName:     ptr("main"),
		},
		{
			name: "matches main case insensitive",
			containers: []*armappcontainers.Container{
				{Name: ptr("sidecar")},
				{Name: ptr("Main")},
			},
			resourceName: "other",
			wantName:     ptr("Main"),
		},
		{
			name: "no match returns nil",
			containers: []*armappcontainers.Container{
				{Name: ptr("worker-a")},
				{Name: ptr("worker-b")},
			},
			resourceName: "web",
			wantName:     nil,
		},
		{
			name: "skips nil elements in multi",
			containers: []*armappcontainers.Container{
				nil,
				{Name: ptr("my-app")},
			},
			resourceName: "my-app",
			wantName:     ptr("my-app"),
		},
		{
			name: "container with nil name skipped",
			containers: []*armappcontainers.Container{
				{Name: nil},
				{Name: ptr("app")},
			},
			resourceName: "app",
			wantName:     ptr("app"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectContainer(
				tt.containers, tt.resourceName,
			)
			if tt.wantName == nil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				require.NotNil(t, got.Name)
				assert.Equal(t, *tt.wantName, *got.Name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// getResourceMeta
// ---------------------------------------------------------------------------

func TestGetResourceMeta(t *testing.T) {
	tests := []struct {
		name       string
		resourceID string
		wantNil    bool
		wantType   string // expected ResourceType in meta
	}{
		{
			name: "exact match container app",
			resourceID: fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/rg/providers"+
					"/Microsoft.App/containerApps/myapp",
				testSubscriptionID,
			),
			wantType: "Microsoft.App/containerApps",
		},
		{
			name: "exact match redis",
			resourceID: fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/rg/providers"+
					"/Microsoft.Cache/redis/myredis",
				testSubscriptionID,
			),
			wantType: "Microsoft.Cache/redis",
		},
		{
			name: "child resource matches parent prefix",
			resourceID: fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/rg/providers"+
					"/Microsoft.CognitiveServices/accounts/myacct"+
					"/deployments/gpt4",
				testSubscriptionID,
			),
			wantType: "Microsoft.CognitiveServices/accounts/deployments",
		},
		{
			name: "unknown resource type returns nil",
			resourceID: fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/rg/providers"+
					"/Microsoft.Unknown/things/foo",
				testSubscriptionID,
			),
			wantNil: true,
		},
		{
			name: "key vault exact match",
			resourceID: fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/rg/providers"+
					"/Microsoft.KeyVault/vaults/myvault",
				testSubscriptionID,
			),
			wantType: "Microsoft.KeyVault/vaults",
		},
		{
			name: "storage account exact match",
			resourceID: fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/rg/providers"+
					"/Microsoft.Storage/storageAccounts/mysa",
				testSubscriptionID,
			),
			wantType: "Microsoft.Storage/storageAccounts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := arm.ParseResourceID(tt.resourceID)
			require.NoError(t, err)

			meta, retID := getResourceMeta(*id)
			if tt.wantNil {
				assert.Nil(t, meta)
			} else {
				require.NotNil(t, meta)
				assert.Equal(t, tt.wantType, meta.ResourceType)
				assert.NotEmpty(t, retID.Name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// getFullPathToProjectForService
// ---------------------------------------------------------------------------

func TestGetFullPathToProjectForService(t *testing.T) {
	t.Run("non-dotnet returns path directly", func(t *testing.T) {
		dir := t.TempDir()
		svc := &project.ServiceConfig{
			Name:         "api",
			Language:     project.ServiceLanguagePython,
			RelativePath: dir,
		}
		got, err := getFullPathToProjectForService(svc)
		require.NoError(t, err)
		assert.Equal(t, svc.Path(), got)
	})

	t.Run("dotnet with single csproj", func(t *testing.T) {
		dir := t.TempDir()
		csproj := filepath.Join(dir, "Api.csproj")
		require.NoError(t, os.WriteFile(csproj, []byte("<Project/>"), 0600))

		svc := &project.ServiceConfig{
			Name:         "api",
			Language:     project.ServiceLanguageDotNet,
			RelativePath: dir,
		}
		got, err := getFullPathToProjectForService(svc)
		require.NoError(t, err)
		assert.Contains(t, got, "Api.csproj")
	})

	t.Run("dotnet with multiple project files errors", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "A.csproj"),
			[]byte("<Project/>"), 0600,
		))
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "B.fsproj"),
			[]byte("<Project/>"), 0600,
		))

		svc := &project.ServiceConfig{
			Name:         "svc",
			Language:     project.ServiceLanguageCsharp,
			RelativePath: dir,
		}
		_, err := getFullPathToProjectForService(svc)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "multiple .NET project files")
	})

	t.Run("dotnet with no project files errors", func(t *testing.T) {
		dir := t.TempDir()
		svc := &project.ServiceConfig{
			Name:         "svc",
			Language:     project.ServiceLanguageFsharp,
			RelativePath: dir,
		}
		_, err := getFullPathToProjectForService(svc)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not determine")
	})

	t.Run("dotnet path is already a file", func(t *testing.T) {
		dir := t.TempDir()
		csproj := filepath.Join(dir, "My.csproj")
		require.NoError(t, os.WriteFile(csproj, []byte("<Project/>"), 0600))

		svc := &project.ServiceConfig{
			Name:         "svc",
			Language:     project.ServiceLanguageDotNet,
			RelativePath: csproj,
		}
		got, err := getFullPathToProjectForService(svc)
		require.NoError(t, err)
		assert.Equal(t, svc.Path(), got)
	})
}

// ---------------------------------------------------------------------------
// NewShowCmd
// ---------------------------------------------------------------------------

func TestNewShowCmd(t *testing.T) {
	cmd := NewShowCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "show [resource-name|resource-id]", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}

// ---------------------------------------------------------------------------
// showResourceOptions
// ---------------------------------------------------------------------------

func TestShowResourceOptions_Defaults(t *testing.T) {
	opts := showResourceOptions{}
	assert.False(t, opts.showSecrets)
	assert.Nil(t, opts.resourceSpec)
	assert.Nil(t, opts.clientOpts)
}

// ---------------------------------------------------------------------------
// showFlags.Bind
// ---------------------------------------------------------------------------

func TestShowFlags_Bind(t *testing.T) {
	cmd := NewShowCmd()
	flags := NewShowFlags(cmd, nil)
	require.NotNil(t, flags)
	// Verify the --show-secrets flag was registered
	f := cmd.Flags().Lookup("show-secrets")
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}
