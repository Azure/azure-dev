// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

type recordingSkillProjectClient struct {
	project    *azdext.ProjectConfig
	section    map[string]any
	getErr     error
	addRequest *azdext.AddServiceRequest
	setRequest *azdext.SetServiceConfigSectionRequest
}

func (c *recordingSkillProjectClient) Get(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.GetProjectResponse, error) {
	if c.getErr != nil {
		return nil, c.getErr
	}
	return &azdext.GetProjectResponse{Project: c.project}, nil
}

func (c *recordingSkillProjectClient) AddService(
	_ context.Context,
	request *azdext.AddServiceRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	c.addRequest = request
	return &azdext.EmptyResponse{}, nil
}

func (c *recordingSkillProjectClient) GetServiceConfigSection(
	context.Context,
	*azdext.GetServiceConfigSectionRequest,
	...grpc.CallOption,
) (*azdext.GetServiceConfigSectionResponse, error) {
	if c.section == nil {
		return &azdext.GetServiceConfigSectionResponse{}, nil
	}
	section, err := structpb.NewStruct(c.section)
	if err != nil {
		return nil, err
	}
	return &azdext.GetServiceConfigSectionResponse{
		Found:   true,
		Section: section,
	}, nil
}

func (c *recordingSkillProjectClient) SetServiceConfigSection(
	_ context.Context,
	request *azdext.SetServiceConfigSectionRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	c.setRequest = request
	return &azdext.EmptyResponse{}, nil
}

func TestUpsertSkillService_AddsInlineService(t *testing.T) {
	t.Parallel()

	client := &recordingSkillProjectClient{
		project: &azdext.ProjectConfig{
			Path:     t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{},
		},
	}
	result, err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name: "code-review",
		Config: skillServiceConfig{
			Description:   "Review code",
			Instructions:  "Review for correctness.",
			License:       "MIT",
			Compatibility: "gpt-5",
			Metadata:      map[string]string{"owner": "platform"},
			Tools:         []string{"code_interpreter"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, client.addRequest)
	assert.Nil(t, client.setRequest)
	assert.True(t, result.Created)
	assert.Equal(t, "code-review", result.Name)

	service := client.addRequest.GetService()
	assert.Equal(t, aiSkillHost, service.GetHost())
	assert.Equal(t, map[string]any{
		"compatibility": "gpt-5",
		"description":   "Review code",
		"instructions":  "Review for correctness.",
		"license":       "MIT",
		"metadata":      map[string]any{"owner": "platform"},
		"tools":         []any{"code_interpreter"},
	}, service.GetAdditionalProperties().AsMap())
}

func TestUpsertSkillService_UpdatesOwnedFieldsAndPreservesOthers(t *testing.T) {
	t.Parallel()

	client := &recordingSkillProjectClient{
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"code-review": {
					Name:         "code-review",
					Host:         aiSkillHost,
					RelativePath: "skills",
					Uses:         []string{"review-tools"},
				},
			},
		},
		section: map[string]any{
			"host":    aiSkillHost,
			"project": "skills",
			"uses":    []any{"review-tools"},
			"archive": "old.zip",
			"custom":  "preserve-me",
		},
	}
	result, err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name: "code-review",
		Config: skillServiceConfig{
			Description:  "Updated review",
			Instructions: "Review new code.",
		},
	})
	require.NoError(t, err)
	assert.Nil(t, client.addRequest)
	require.NotNil(t, client.setRequest)
	assert.False(t, result.Created)

	updated := client.setRequest.GetSection().AsMap()
	assert.Equal(t, aiSkillHost, updated["host"])
	assert.Equal(t, "skills", updated["project"])
	assert.Equal(t, []any{"review-tools"}, updated["uses"])
	assert.Equal(t, "preserve-me", updated["custom"])
	assert.Equal(t, "Updated review", updated["description"])
	assert.Equal(t, "Review new code.", updated["instructions"])
	assert.NotContains(t, updated, "archive")
}

func TestUpsertSkillService_SavesPortableArchiveReference(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	source := filepath.Join(projectRoot, "skills", "code-review")
	writeSkillFiles(t, source)
	client := &recordingSkillProjectClient{
		project: &azdext.ProjectConfig{
			Path:     projectRoot,
			Services: map[string]*azdext.ServiceConfig{},
		},
	}

	_, err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name:          "code-review",
		ArchiveSource: source,
	})
	require.NoError(t, err)
	assert.Equal(
		t,
		"skills/code-review",
		client.addRequest.GetService().GetAdditionalProperties().AsMap()["archive"],
	)
}

func TestUpsertSkillService_SavesArchiveRelativeToServicePath(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	source := filepath.Join(projectRoot, "skills", "code-review")
	writeSkillFiles(t, source)
	client := &recordingSkillProjectClient{
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"code-review": {
					Name:         "code-review",
					Host:         aiSkillHost,
					RelativePath: "skills",
				},
			},
		},
		section: map[string]any{
			"host":    aiSkillHost,
			"project": "skills",
		},
	}

	_, err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name:          "code-review",
		ArchiveSource: source,
	})
	require.NoError(t, err)
	updated := client.setRequest.GetSection().AsMap()
	assert.Equal(t, "code-review", updated["archive"])
	assert.Equal(t, "skills", updated["project"])
}

func TestUpsertSkillService_SwitchesInlineServiceToArchive(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	source := filepath.Join(projectRoot, "skills", "code-review")
	writeSkillFiles(t, source)
	client := &recordingSkillProjectClient{
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"code-review": {
					Name: "code-review",
					Host: aiSkillHost,
				},
			},
		},
		section: map[string]any{
			"host":         aiSkillHost,
			"description":  "Old description",
			"instructions": "Old instructions",
			"tools":        []any{"code_interpreter"},
			"custom":       "preserve-me",
		},
	}

	_, err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name:          "code-review",
		ArchiveSource: source,
	})
	require.NoError(t, err)
	updated := client.setRequest.GetSection().AsMap()
	assert.Equal(t, "skills/code-review", updated["archive"])
	assert.Equal(t, "preserve-me", updated["custom"])
	assert.NotContains(t, updated, "description")
	assert.NotContains(t, updated, "instructions")
	assert.NotContains(t, updated, "tools")
}

func TestUpsertSkillService_RejectsArchiveOutsideServicePath(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	outside := filepath.Join(t.TempDir(), "code-review")
	writeSkillFiles(t, outside)
	client := &recordingSkillProjectClient{
		project: &azdext.ProjectConfig{
			Path:     projectRoot,
			Services: map[string]*azdext.ServiceConfig{},
		},
	}

	_, err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name:          "code-review",
		ArchiveSource: outside,
	})
	require.ErrorContains(t, err, "outside the skill service directory")
	assert.Nil(t, client.addRequest)
}

func TestUpsertSkillService_RejectsServiceDirectoryAsArchive(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "SKILL.md"),
		[]byte("---\nname: code-review\ndescription: Review code\n---\nReview."),
		0600,
	))
	client := &recordingSkillProjectClient{
		project: &azdext.ProjectConfig{
			Path:     projectRoot,
			Services: map[string]*azdext.ServiceConfig{},
		},
	}

	_, err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name:          "code-review",
		ArchiveSource: projectRoot,
	})
	require.ErrorContains(t, err, "service directory")
	assert.Nil(t, client.addRequest)
}

func TestUpsertSkillService_RejectsHostConflict(t *testing.T) {
	t.Parallel()

	client := &recordingSkillProjectClient{
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"code-review": {
					Name: "code-review",
					Host: "containerapp",
				},
			},
		},
	}
	_, err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name:   "code-review",
		Config: skillServiceConfig{Instructions: "Review code."},
	})
	require.ErrorContains(t, err, "already uses host")
	assert.Nil(t, client.addRequest)
	assert.Nil(t, client.setRequest)
}

func TestUpsertSkillService_DoesNotReplaceMissingExistingSection(t *testing.T) {
	t.Parallel()

	client := &recordingSkillProjectClient{
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"code-review": {
					Name: "code-review",
					Host: aiSkillHost,
				},
			},
		},
	}

	_, err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name:   "code-review",
		Config: skillServiceConfig{Instructions: "Review code."},
	})
	require.ErrorContains(t, err, "disappeared from azure.yaml")
	assert.Nil(t, client.setRequest)
}

func TestUpsertSkillService_RequiresProject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		client *recordingSkillProjectClient
	}{
		{
			name:   "project lookup fails",
			client: &recordingSkillProjectClient{getErr: errors.New("not found")},
		},
		{
			name:   "project missing",
			client: &recordingSkillProjectClient{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := upsertSkillService(t.Context(), tt.client, skillServiceDeclaration{
				Name:   "code-review",
				Config: skillServiceConfig{Instructions: "Review code."},
			})
			require.ErrorContains(t, err, "cannot add skill")
		})
	}
}

func writeSkillFiles(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: code-review\ndescription: Review code\n---\nReview."),
		0600,
	))
}
