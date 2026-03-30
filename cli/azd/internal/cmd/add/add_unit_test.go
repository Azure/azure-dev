// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	dmp "github.com/sergi/go-diff/diffmatchpatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// validateServiceName
// ---------------------------------------------------------------------------

func TestValidateServiceName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		input     string
		services  map[string]*project.ServiceConfig
		wantError string
	}{
		{
			name:     "valid name with no existing services",
			input:    "web-api",
			services: map[string]*project.ServiceConfig{},
		},
		{
			name:      "empty name",
			input:     "",
			services:  map[string]*project.ServiceConfig{},
			wantError: "cannot be empty",
		},
		{
			name:  "duplicate service name",
			input: "api",
			services: map[string]*project.ServiceConfig{
				"api": {},
			},
			wantError: "already exists",
		},
		{
			name:      "name starts with hyphen",
			input:     "-invalid",
			services:  map[string]*project.ServiceConfig{},
			wantError: "must start with",
		},
		{
			name:      "name ends with hyphen",
			input:     "invalid-",
			services:  map[string]*project.ServiceConfig{},
			wantError: "must end with",
		},
		{
			name:      "uppercase letters",
			input:     "Invalid",
			services:  map[string]*project.ServiceConfig{},
			wantError: "must start with a lower",
		},
		{
			name:     "valid single char",
			input:    "a",
			services: map[string]*project.ServiceConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			prj := &project.ProjectConfig{
				Services: tt.services,
			}
			err := validateServiceName(tt.input, prj)
			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// validateResourceName
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// validateContainerName
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// resourceType
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// allStorageDataTypes
// ---------------------------------------------------------------------------

func TestAllStorageDataTypes(t *testing.T) {
	t.Parallel()
	types := allStorageDataTypes()
	require.Len(t, types, 1)
	assert.Equal(t, StorageDataTypeBlob, types[0])
}

// ---------------------------------------------------------------------------
// fillAiProjectName
// ---------------------------------------------------------------------------

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
				context.Background(), r, nil, opts,
			)
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, got.Name)
		})
	}
}

// ---------------------------------------------------------------------------
// Configure — singleton resource types (Redis, Search, KeyVault)
// ---------------------------------------------------------------------------

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
				context.Background(), r, nil, opts,
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

// ---------------------------------------------------------------------------
// ServiceFromDetect
// ---------------------------------------------------------------------------

func TestServiceFromDetect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		root    string
		svcName string
		prj     appdetect.Project
		svcKind project.ServiceTargetKind
		check   func(t *testing.T, svc project.ServiceConfig)
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
			check: func(t *testing.T, svc project.ServiceConfig) {
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
			check: func(t *testing.T, svc project.ServiceConfig) {
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
			check: func(t *testing.T, svc project.ServiceConfig) {
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
			check: func(t *testing.T, svc project.ServiceConfig) {
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
			check: func(t *testing.T, svc project.ServiceConfig) {
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
			check: func(t *testing.T, svc project.ServiceConfig) {
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

// ---------------------------------------------------------------------------
// diffNotEq
// ---------------------------------------------------------------------------

func TestDiffNotEq(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []dmp.Diff
		want bool
	}{
		{
			name: "all equal",
			in: []dmp.Diff{
				{Type: dmp.DiffEqual, Text: "hello"},
			},
			want: false,
		},
		{
			name: "has insert",
			in: []dmp.Diff{
				{Type: dmp.DiffEqual, Text: "a"},
				{Type: dmp.DiffInsert, Text: "b"},
			},
			want: true,
		},
		{
			name: "has delete",
			in: []dmp.Diff{
				{Type: dmp.DiffDelete, Text: "x"},
			},
			want: true,
		},
		{
			name: "empty slice",
			in:   []dmp.Diff{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, diffNotEq(tt.in))
		})
	}
}

// ---------------------------------------------------------------------------
// lineDiffsFromStr
// ---------------------------------------------------------------------------

func TestLineDiffsFromStr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		op     dmp.Operation
		input  string
		wantN  int
		wantOp dmp.Operation
	}{
		{
			name:   "single line",
			op:     dmp.DiffInsert,
			input:  "hello",
			wantN:  1,
			wantOp: dmp.DiffInsert,
		},
		{
			name:   "multi line",
			op:     dmp.DiffDelete,
			input:  "a\nb\nc",
			wantN:  3,
			wantOp: dmp.DiffDelete,
		},
		{
			name:   "empty string",
			op:     dmp.DiffEqual,
			input:  "",
			wantN:  1,
			wantOp: dmp.DiffEqual,
		},
		{
			name:   "trailing newline",
			op:     dmp.DiffInsert,
			input:  "line1\n",
			wantN:  2,
			wantOp: dmp.DiffInsert,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := lineDiffsFromStr(tt.op, tt.input)
			assert.Len(t, result, tt.wantN)
			for _, r := range result {
				assert.Equal(t, tt.wantOp, r.Type)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// linesDiffsFromTextDiffs
// ---------------------------------------------------------------------------

func TestLinesDiffsFromTextDiffs(t *testing.T) {
	t.Parallel()
	diffs := []dmp.Diff{
		{Type: dmp.DiffEqual, Text: "line1\nline2"},
		{Type: dmp.DiffInsert, Text: "new"},
	}
	result := linesDiffsFromTextDiffs(diffs)
	// "line1\nline2" → 2 lines, "new" → 1 line = 3 total
	require.Len(t, result, 3)
	assert.Equal(t, dmp.DiffEqual, result[0].Type)
	assert.Equal(t, "line1", result[0].Text)
	assert.Equal(t, dmp.DiffEqual, result[1].Type)
	assert.Equal(t, "line2", result[1].Text)
	assert.Equal(t, dmp.DiffInsert, result[2].Type)
	assert.Equal(t, "new", result[2].Text)
}

// ---------------------------------------------------------------------------
// formatLine
// ---------------------------------------------------------------------------

func TestFormatLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		op     dmp.Operation
		text   string
		indent int
		check  func(t *testing.T, out string)
	}{
		{
			name:   "insert prefix",
			op:     dmp.DiffInsert,
			text:   "added",
			indent: 0,
			check: func(t *testing.T, out string) {
				assert.Contains(t, out, "+")
				assert.Contains(t, out, "added")
				assert.True(t, strings.HasSuffix(out, "\n"))
			},
		},
		{
			name:   "delete prefix",
			op:     dmp.DiffDelete,
			text:   "removed",
			indent: 0,
			check: func(t *testing.T, out string) {
				assert.Contains(t, out, "-")
				assert.Contains(t, out, "removed")
			},
		},
		{
			name:   "equal prefix with indent",
			op:     dmp.DiffEqual,
			text:   "same",
			indent: 4,
			check: func(t *testing.T, out string) {
				assert.Contains(t, out, "    same")
				assert.NotContains(t, out, "+")
				assert.NotContains(t, out, "-")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := formatLine(tt.op, tt.text, tt.indent)
			tt.check(t, out)
		})
	}
}

// ---------------------------------------------------------------------------
// DiffBlocks (integration of the diff helpers)
// ---------------------------------------------------------------------------

func TestDiffBlocks_NewEntry(t *testing.T) {
	t.Parallel()
	old := map[string]*project.ResourceConfig{}
	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbRedis,
		Name: "redis",
	}
	new := map[string]*project.ResourceConfig{
		"redis": r,
	}

	result, err := DiffBlocks(old, new)
	require.NoError(t, err)
	// New entry should contain insert markers
	assert.Contains(t, result, "redis:")
	assert.Contains(t, result, "+")
}

func TestDiffBlocks_NoChanges(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbRedis,
		Name: "redis",
	}
	same := map[string]*project.ResourceConfig{
		"redis": r,
	}

	result, err := DiffBlocks(same, same)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestDiffBlocks_EmptyMaps(t *testing.T) {
	t.Parallel()
	result, err := DiffBlocks(
		map[string]*project.ResourceConfig{},
		map[string]*project.ResourceConfig{},
	)
	require.NoError(t, err)
	assert.Empty(t, result)
}

// ---------------------------------------------------------------------------
// previewWriter
// ---------------------------------------------------------------------------

func TestPreviewWriter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		check func(t *testing.T, out string)
	}{
		{
			name:  "plus line is green",
			input: "+  added item\n",
			check: func(t *testing.T, out string) {
				// The output should contain the text
				assert.Contains(t, out, "added item")
			},
		},
		{
			name:  "minus line is red",
			input: "-  removed item\n",
			check: func(t *testing.T, out string) {
				assert.Contains(t, out, "removed item")
			},
		},
		{
			name:  "b prefix replaced with space",
			input: "b  header text\n",
			check: func(t *testing.T, out string) {
				// 'b' is replaced with space
				assert.Contains(t, out, "header text")
				assert.NotContains(t, out, "b  header")
			},
		},
		{
			name:  "g prefix replaced with space",
			input: "g  green text\n",
			check: func(t *testing.T, out string) {
				assert.Contains(t, out, "green text")
			},
		},
		{
			name:  "normal text unchanged",
			input: "   normal line\n",
			check: func(t *testing.T, out string) {
				assert.Contains(t, out, "normal line")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			pw := &previewWriter{w: &buf}
			n, err := pw.Write([]byte(tt.input))
			require.NoError(t, err)
			assert.Equal(t, len(tt.input), n)
			tt.check(t, buf.String())
		})
	}
}

// ---------------------------------------------------------------------------
// Metadata
// ---------------------------------------------------------------------------

func TestMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		res        *project.ResourceConfig
		wantType   string
		wantHasVar bool
	}{
		{
			name: "redis resource returns metadata",
			res: &project.ResourceConfig{
				Type: project.ResourceTypeDbRedis,
				Name: "redis",
			},
			wantType:   "Microsoft.Cache/redis",
			wantHasVar: true,
		},
		{
			name: "unknown resource type returns empty",
			res: &project.ResourceConfig{
				Type: project.ResourceType("unknown.type"),
				Name: "thing",
			},
			wantType: "",
		},
		{
			name: "host resource uses uppercase name prefix",
			res: &project.ResourceConfig{
				Type: project.ResourceTypeHostContainerApp,
				Name: "web-api",
			},
			wantType:   "Microsoft.App/containerApps",
			wantHasVar: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			meta := Metadata(tt.res)
			if tt.wantType == "" {
				assert.Empty(t, meta.ResourceType)
			} else {
				assert.Equal(t, tt.wantType, meta.ResourceType)
			}

			if tt.wantHasVar {
				assert.NotEmpty(
					t, meta.Variables,
					"expected variables for type %s",
					tt.wantType,
				)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DbMap
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// LanguageMap
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// HostMap
// ---------------------------------------------------------------------------

func TestHostMap(t *testing.T) {
	t.Parallel()
	assert.Equal(
		t,
		project.AppServiceTarget,
		HostMap[project.ResourceTypeHostAppService],
	)
	assert.Equal(
		t,
		project.ContainerAppTarget,
		HostMap[project.ResourceTypeHostContainerApp],
	)
	assert.Len(t, HostMap, 2)
}

// ---------------------------------------------------------------------------
// ServiceLanguageMap
// ---------------------------------------------------------------------------

func TestServiceLanguageMap(t *testing.T) {
	t.Parallel()
	pyRuntime := ServiceLanguageMap[project.ServiceLanguagePython]
	assert.Equal(
		t,
		project.AppServiceRuntimeStackPython,
		pyRuntime.Stack,
	)

	jsRuntime := ServiceLanguageMap[project.ServiceLanguageJavaScript]
	assert.Equal(
		t,
		project.AppServiceRuntimeStackNode,
		jsRuntime.Stack,
	)

	tsRuntime := ServiceLanguageMap[project.ServiceLanguageTypeScript]
	assert.Equal(
		t,
		project.AppServiceRuntimeStackNode,
		tsRuntime.Stack,
	)

	// Java and .NET are not in the map
	_, hasJava := ServiceLanguageMap[project.ServiceLanguageJava]
	assert.False(t, hasJava)
}

// ---------------------------------------------------------------------------
// provisionSelection constants
// ---------------------------------------------------------------------------

func TestProvisionSelectionConstants(t *testing.T) {
	t.Parallel()
	// iota constants: verify ordering and distinct values
	assert.Equal(t, 0, int(provisionUnknown))
	assert.Equal(t, 1, int(provision))
	assert.Equal(t, 2, int(provisionPreview))
	assert.Equal(t, 3, int(provisionSkip))
}
