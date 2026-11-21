// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateResourceName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		input     string
		resources map[string]*project.ResourceConfig
		wantError string
	}{
		{
			name:      "valid name",
			input:     "my-db",
			resources: map[string]*project.ResourceConfig{},
		},
		{
			name:      "empty name",
			input:     "",
			resources: map[string]*project.ResourceConfig{},
			wantError: "cannot be empty",
		},
		{
			name:  "duplicate resource name",
			input: "redis",
			resources: map[string]*project.ResourceConfig{
				"redis": {},
			},
			wantError: "already exists",
		},
		{
			name:      "over 63 chars",
			input:     strings.Repeat("a", 64),
			resources: map[string]*project.ResourceConfig{},
			wantError: "63 characters",
		},
		{
			name:      "exact 63 chars is valid",
			input:     strings.Repeat("a", 63),
			resources: map[string]*project.ResourceConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			prj := &project.ProjectConfig{
				Resources: tt.resources,
			}
			err := validateResourceName(tt.input, prj)
			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateContainerName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		input     string
		wantError string
	}{
		{
			name:  "valid container name",
			input: "my-container",
		},
		{
			name:  "minimum length 3",
			input: "abc",
		},
		{
			name:      "too short",
			input:     "ab",
			wantError: "3 characters or more",
		},
		{
			name:      "consecutive hyphens",
			input:     "my--container",
			wantError: "consecutive hyphens",
		},
		{
			name:      "uppercase letters",
			input:     "MyContainer",
			wantError: "lower case",
		},
		{
			name:      "single char",
			input:     "a",
			wantError: "3 characters or more",
		},
		{
			name:  "all numbers",
			input: "123",
		},
		{
			name:      "empty string",
			input:     "",
			wantError: "3 characters or more",
		},
		{
			name:      "starts with hyphen",
			input:     "-abc",
			wantError: "must start with",
		},
		{
			name:      "ends with hyphen",
			input:     "abc-",
			wantError: "must end with",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateContainerName(tt.input)
			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestResourceType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		azureResType string
		want         project.ResourceType
	}{
		{
			name:         "redis",
			azureResType: "Microsoft.Cache/redis",
			want:         project.ResourceTypeDbRedis,
		},
		{
			name:         "container apps",
			azureResType: "Microsoft.App/containerApps",
			want:         project.ResourceTypeHostContainerApp,
		},
		{
			name:         "app service",
			azureResType: "Microsoft.Web/sites",
			want:         project.ResourceTypeHostAppService,
		},
		{
			name:         "key vault",
			azureResType: "Microsoft.KeyVault/vaults",
			want:         project.ResourceTypeKeyVault,
		},
		{
			name:         "unknown type returns empty",
			azureResType: "Microsoft.Unknown/things",
			want:         project.ResourceType(""),
		},
		{
			name:         "empty string returns empty",
			azureResType: "",
			want:         project.ResourceType(""),
		},
		{
			name:         "storage accounts",
			azureResType: "Microsoft.Storage/storageAccounts",
			want:         project.ResourceTypeStorage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resourceType(tt.azureResType)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAllStorageDataTypes(t *testing.T) {
	t.Parallel()
	types := allStorageDataTypes()
	require.Len(t, types, 1)
	assert.Equal(t, StorageDataTypeBlob, types[0])
}

func TestFillAiProjectName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		rName     string
		resources map[string]*project.ResourceConfig
		wantName  string
	}{
		{
			name:      "sets default name when empty",
			rName:     "",
			resources: map[string]*project.ResourceConfig{},
			wantName:  "ai-project",
		},
		{
			name:      "keeps existing name",
			rName:     "my-ai",
			resources: map[string]*project.ResourceConfig{},
			wantName:  "my-ai",
		},
		{
			name:  "appends suffix when default conflicts",
			rName: "",
			resources: map[string]*project.ResourceConfig{
				"ai-project": {},
			},
			wantName: "ai-project-2",
		},
		{
			name:  "appends increasing suffix for multiple conflicts",
			rName: "",
			resources: map[string]*project.ResourceConfig{
				"ai-project":     {},
				"ai-project-2":   {},
				"ai-project-2-3": {},
			},
			// The naming logic appends to current: ai-project→ai-project-2→ai-project-2-3→ai-project-2-3-4
			wantName: "ai-project-2-3-4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &project.ResourceConfig{Name: tt.rName}
			opts := PromptOptions{
				PrjConfig: &project.ProjectConfig{
					Resources: tt.resources,
				},
			}
			got, err := fillAiProjectName(
				t.Context(), r, nil, opts,
			)
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, got.Name)
		})
	}
}

func TestConfigure_SingletonResources(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		resType   project.ResourceType
		resources map[string]*project.ResourceConfig
		wantName  string
		wantError string
	}{
		{
			name:      "redis sets name",
			resType:   project.ResourceTypeDbRedis,
			resources: map[string]*project.ResourceConfig{},
			wantName:  "redis",
		},
		{
			name:    "redis duplicate error",
			resType: project.ResourceTypeDbRedis,
			resources: map[string]*project.ResourceConfig{
				"redis": {},
			},
			wantError: "only one Redis",
		},
		{
			name:      "search sets name",
			resType:   project.ResourceTypeAiSearch,
			resources: map[string]*project.ResourceConfig{},
			wantName:  "search",
		},
		{
			name:    "search duplicate error",
			resType: project.ResourceTypeAiSearch,
			resources: map[string]*project.ResourceConfig{
				"search": {},
			},
			wantError: "only one AI Search",
		},
		{
			name:      "keyvault sets name",
			resType:   project.ResourceTypeKeyVault,
			resources: map[string]*project.ResourceConfig{},
			wantName:  "vault",
		},
		{
			name:    "keyvault duplicate error",
			resType: project.ResourceTypeKeyVault,
			resources: map[string]*project.ResourceConfig{
				"vault": {},
			},
			wantError: "already have a project key vault",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &project.ResourceConfig{Type: tt.resType}
			opts := PromptOptions{
				PrjConfig: &project.ProjectConfig{
					Resources: tt.resources,
				},
			}
			got, err := Configure(
				t.Context(), r, nil, opts,
			)
			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantName, got.Name)
			}
		})
	}
}

func TestServiceFromDetect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		root    string
		svcName string
		prj     appdetect.Project
		svcKind project.ServiceTargetKind
		check   func(t *testing.T, svc *project.ServiceConfig)
		wantErr string
	}{
		{
			name:    "basic python project",
			root:    "/projects",
			svcName: "my-api",
			prj: appdetect.Project{
				Path:     "/projects/api",
				Language: appdetect.Python,
			},
			svcKind: project.ContainerAppTarget,
			check: func(t *testing.T, svc *project.ServiceConfig) {
				assert.Equal(t, "my-api", svc.Name)
				assert.Equal(
					t,
					project.ServiceLanguagePython,
					svc.Language,
				)
				assert.Equal(t, "api", svc.RelativePath)
				assert.Equal(
					t,
					project.ContainerAppTarget,
					svc.Host,
				)
			},
		},
		{
			name:    "empty svc name uses dir name",
			root:    "/projects",
			svcName: "",
			prj: appdetect.Project{
				Path:     "/projects/my-service",
				Language: appdetect.JavaScript,
			},
			svcKind: project.ContainerAppTarget,
			check: func(t *testing.T, svc *project.ServiceConfig) {
				assert.Equal(t, "my-service", svc.Name)
				assert.Equal(
					t,
					project.ServiceLanguageJavaScript,
					svc.Language,
				)
			},
		},
		{
			name:    "unsupported language",
			root:    "/projects",
			svcName: "svc",
			prj: appdetect.Project{
				Path:     "/projects/app",
				Language: appdetect.Language("cobol"),
			},
			svcKind: project.ContainerAppTarget,
			wantErr: "unsupported language",
		},
		{
			name:    "dotnet with app service",
			root:    "/projects",
			svcName: "web",
			prj: appdetect.Project{
				Path:     "/projects/web",
				Language: appdetect.DotNet,
			},
			svcKind: project.AppServiceTarget,
			check: func(t *testing.T, svc *project.ServiceConfig) {
				assert.Equal(
					t,
					project.ServiceLanguageDotNet,
					svc.Language,
				)
				assert.Equal(
					t,
					project.AppServiceTarget,
					svc.Host,
				)
			},
		},
		{
			name:    "docker with non-container target errors",
			root:    "/projects",
			svcName: "svc",
			prj: appdetect.Project{
				Path:     "/projects/app",
				Language: appdetect.Python,
				Docker: &appdetect.Docker{
					Path: "/projects/app/Dockerfile",
				},
			},
			svcKind: project.AppServiceTarget,
			wantErr: "unsupported host with Dockerfile",
		},
		{
			name:    "web ui framework sets output path",
			root:    "/projects",
			svcName: "spa",
			prj: appdetect.Project{
				Path:     "/projects/spa",
				Language: appdetect.TypeScript,
				Dependencies: []appdetect.Dependency{
					appdetect.JsVite,
				},
			},
			svcKind: project.ContainerAppTarget,
			check: func(t *testing.T, svc *project.ServiceConfig) {
				assert.Equal(t, "dist", svc.OutputPath)
			},
		},
		{
			name:    "next.js clears output path",
			root:    "/projects",
			svcName: "next",
			prj: appdetect.Project{
				Path:     "/projects/next",
				Language: appdetect.JavaScript,
				Dependencies: []appdetect.Dependency{
					appdetect.JsNext,
				},
			},
			svcKind: project.ContainerAppTarget,
			check: func(t *testing.T, svc *project.ServiceConfig) {
				assert.Equal(t, "", svc.OutputPath)
			},
		},
		{
			name:    "react sets build output path",
			root:    "/projects",
			svcName: "cra",
			prj: appdetect.Project{
				Path:     "/projects/cra",
				Language: appdetect.JavaScript,
				Dependencies: []appdetect.Dependency{
					appdetect.JsReact,
				},
			},
			svcKind: project.ContainerAppTarget,
			check: func(t *testing.T, svc *project.ServiceConfig) {
				assert.Equal(t, "build", svc.OutputPath)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, err := ServiceFromDetect(
				tt.root, tt.svcName, tt.prj, tt.svcKind,
			)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
				tt.check(t, svc)
			}
		})
	}
}

func TestDbMap(t *testing.T) {
	t.Parallel()
	expected := map[appdetect.DatabaseDep]project.ResourceType{
		appdetect.DbMongo:    project.ResourceTypeDbMongo,
		appdetect.DbPostgres: project.ResourceTypeDbPostgres,
		appdetect.DbMySql:    project.ResourceTypeDbMySql,
		appdetect.DbRedis:    project.ResourceTypeDbRedis,
	}
	assert.Equal(t, expected, DbMap)
}

func TestLanguageMap(t *testing.T) {
	t.Parallel()
	assert.Equal(
		t,
		project.ServiceLanguageDotNet,
		LanguageMap[appdetect.DotNet],
	)
	assert.Equal(
		t,
		project.ServiceLanguageJava,
		LanguageMap[appdetect.Java],
	)
	assert.Equal(
		t,
		project.ServiceLanguagePython,
		LanguageMap[appdetect.Python],
	)
	assert.Equal(
		t,
		project.ServiceLanguageJavaScript,
		LanguageMap[appdetect.JavaScript],
	)
	assert.Equal(
		t,
		project.ServiceLanguageTypeScript,
		LanguageMap[appdetect.TypeScript],
	)
	assert.Len(t, LanguageMap, 5)
}
