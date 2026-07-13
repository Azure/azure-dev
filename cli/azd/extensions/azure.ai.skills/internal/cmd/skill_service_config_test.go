// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiskills/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

type fakeSkillProjectClient struct {
	project       *azdext.ProjectConfig
	added         *azdext.ServiceConfig
	current       map[string]any
	updated       map[string]any
	getErr        error
	getSectionErr error
	setSectionErr error
}

func (f *fakeSkillProjectClient) Get(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.GetProjectResponse, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &azdext.GetProjectResponse{Project: f.project}, nil
}

func (f *fakeSkillProjectClient) AddService(
	_ context.Context,
	req *azdext.AddServiceRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	f.added = req.GetService()
	return &azdext.EmptyResponse{}, nil
}

func (f *fakeSkillProjectClient) GetServiceConfigSection(
	context.Context,
	*azdext.GetServiceConfigSectionRequest,
	...grpc.CallOption,
) (*azdext.GetServiceConfigSectionResponse, error) {
	if f.getSectionErr != nil {
		return nil, f.getSectionErr
	}
	if f.current == nil {
		return &azdext.GetServiceConfigSectionResponse{Found: false}, nil
	}
	section, err := structpb.NewStruct(f.current)
	if err != nil {
		return nil, err
	}
	return &azdext.GetServiceConfigSectionResponse{
		Found:   true,
		Section: section,
	}, nil
}

func (f *fakeSkillProjectClient) SetServiceConfigSection(
	_ context.Context,
	req *azdext.SetServiceConfigSectionRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	if f.setSectionErr != nil {
		return nil, f.setSectionErr
	}
	f.updated = req.GetSection().AsMap()
	return &azdext.EmptyResponse{}, nil
}

func TestUpsertSkillService_AddsInlineService(t *testing.T) {
	client := &fakeSkillProjectClient{
		project: &azdext.ProjectConfig{
			Path:     t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{},
		},
	}

	err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name: "triage-rules",
		Config: skillServiceConfig{
			Description:  "Issue triage rules",
			Instructions: "Triage incoming issues.",
			Tools:        []string{"web_search"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, client.added)
	assert.Equal(t, "triage-rules", client.added.GetName())
	assert.Equal(t, aiSkillHost, client.added.GetHost())
	assert.Equal(t, "Issue triage rules", client.added.GetAdditionalProperties().AsMap()["description"])
	assert.Equal(t, "Triage incoming issues.", client.added.GetAdditionalProperties().AsMap()["instructions"])
	assert.Equal(t, []any{"web_search"}, client.added.GetAdditionalProperties().AsMap()["tools"])
}

func TestUpsertSkillService_UpdatesOwnedFieldsAndPreservesOthers(t *testing.T) {
	client := &fakeSkillProjectClient{
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"triage-rules": {Name: "triage-rules", Host: aiSkillHost},
			},
		},
		current: map[string]any{
			"host":    aiSkillHost,
			"archive": "./old.zip",
			"uses":    []any{"ai-project"},
			"custom":  "preserve-me",
		},
	}

	err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name: "triage-rules",
		Config: skillServiceConfig{
			Description:  "Updated",
			Instructions: "Updated instructions.",
		},
	})
	require.NoError(t, err)
	require.Nil(t, client.added)
	require.NotNil(t, client.updated)
	assert.Equal(t, aiSkillHost, client.updated["host"])
	assert.Equal(t, []any{"ai-project"}, client.updated["uses"])
	assert.Equal(t, "preserve-me", client.updated["custom"])
	assert.Equal(t, "Updated", client.updated["description"])
	assert.Equal(t, "Updated instructions.", client.updated["instructions"])
	assert.NotContains(t, client.updated, "archive")
}

func TestUpsertSkillService_SavesPortableArchiveReference(t *testing.T) {
	projectDir := t.TempDir()
	archiveDir := filepath.Join(projectDir, "skills", "triage-rules")
	require.NoError(t, writeSkillMd(archiveDir, "triage-rules"))

	client := &fakeSkillProjectClient{
		project: &azdext.ProjectConfig{
			Path:     projectDir,
			Services: map[string]*azdext.ServiceConfig{},
		},
	}

	err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name:          "triage-rules",
		ArchiveSource: archiveDir,
	})
	require.NoError(t, err)
	assert.Equal(t, "skills/triage-rules", client.added.GetAdditionalProperties().AsMap()["archive"])
}

func TestUpsertSkillService_RejectsArchiveOutsideProject(t *testing.T) {
	projectDir := t.TempDir()
	outsideDir := t.TempDir()
	require.NoError(t, writeSkillMd(outsideDir, "triage-rules"))

	client := &fakeSkillProjectClient{
		project: &azdext.ProjectConfig{
			Path:     projectDir,
			Services: map[string]*azdext.ServiceConfig{},
		},
	}

	err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name:          "triage-rules",
		ArchiveSource: outsideDir,
	})
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInvalidSkillFile, localErr.Code)
}

func TestUpsertSkillService_RejectsHostConflict(t *testing.T) {
	client := &fakeSkillProjectClient{
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"triage-rules": {Name: "triage-rules", Host: "containerapp"},
			},
		},
	}

	err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name: "triage-rules",
		Config: skillServiceConfig{
			Instructions: "Triage issues.",
		},
	})
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeSkillServiceConflict, localErr.Code)
}

func TestUpsertSkillService_RequiresProject(t *testing.T) {
	client := &fakeSkillProjectClient{getErr: errors.New("azure.yaml not found")}

	err := upsertSkillService(t.Context(), client, skillServiceDeclaration{
		Name: "triage-rules",
		Config: skillServiceConfig{
			Instructions: "Triage issues.",
		},
	})
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeProjectManifestNotFound, localErr.Code)
}

func writeSkillMd(dir, name string) error {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	return os.WriteFile(
		filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: "+name+"\ndescription: test\n---\ninstructions\n"),
		0600,
	)
}
