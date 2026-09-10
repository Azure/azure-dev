// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"azureaiskills/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

var skillServiceOwnedFields = []string{
	"archive",
	"compatibility",
	"description",
	"instructions",
	"license",
	"metadata",
	"tools",
}

type skillServiceDeclaration struct {
	Name          string
	Config        skillServiceConfig
	ArchiveSource string
}

type skillServiceUpsertResult struct {
	Name        string `json:"name"`
	Host        string `json:"host"`
	ProjectPath string `json:"projectPath"`
	Created     bool   `json:"created"`
}

type skillProjectClient interface {
	Get(
		ctx context.Context,
		in *azdext.EmptyRequest,
		opts ...grpc.CallOption,
	) (*azdext.GetProjectResponse, error)
	AddService(
		ctx context.Context,
		in *azdext.AddServiceRequest,
		opts ...grpc.CallOption,
	) (*azdext.EmptyResponse, error)
	GetServiceConfigSection(
		ctx context.Context,
		in *azdext.GetServiceConfigSectionRequest,
		opts ...grpc.CallOption,
	) (*azdext.GetServiceConfigSectionResponse, error)
	SetServiceConfigSection(
		ctx context.Context,
		in *azdext.SetServiceConfigSectionRequest,
		opts ...grpc.CallOption,
	) (*azdext.EmptyResponse, error)
}

func upsertSkillServiceToProject(
	ctx context.Context,
	declaration skillServiceDeclaration,
) (*skillServiceUpsertResult, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, fmt.Errorf("create azd client to update azure.yaml: %w", err)
	}
	defer azdClient.Close()

	return upsertSkillService(ctx, azdClient.Project(), declaration)
}

func upsertSkillService(
	ctx context.Context,
	projectClient skillProjectClient,
	declaration skillServiceDeclaration,
) (*skillServiceUpsertResult, error) {
	projectResponse, err := projectClient.Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeProjectManifestNotFound,
			fmt.Sprintf("cannot add skill %q to azure.yaml: %s", declaration.Name, err),
			"run this command from an azd project containing azure.yaml",
		)
	}
	project := projectResponse.GetProject()
	if project == nil || strings.TrimSpace(project.GetPath()) == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeProjectManifestNotFound,
			fmt.Sprintf("cannot add skill %q to azure.yaml: no azd project is loaded", declaration.Name),
			"run this command from an azd project containing azure.yaml",
		)
	}

	existing, found := project.GetServices()[declaration.Name]
	if found && existing.GetHost() != aiSkillHost {
		return nil, exterrors.Validation(
			exterrors.CodeSkillServiceConflict,
			fmt.Sprintf(
				"cannot add skill %q to azure.yaml: service %q already uses host %q",
				declaration.Name,
				declaration.Name,
				existing.GetHost(),
			),
			"choose a different skill name or rename the existing azure.yaml service",
		)
	}

	cfg := declaration.Config
	if declaration.ArchiveSource != "" {
		serviceRoot := project.GetPath()
		if found {
			serviceRoot = skillServiceRoot(project.GetPath(), existing)
		}
		archiveReference, err := portableSkillArchiveReference(serviceRoot, declaration.ArchiveSource)
		if err != nil {
			return nil, err
		}

		archive, err := prepareSkillArchive(declaration.ArchiveSource)
		if err != nil {
			return nil, err
		}
		if err := archive.Reader.Close(); err != nil {
			return nil, fmt.Errorf("close prepared skill archive: %w", err)
		}
		cfg.Archive = archiveReference
	}
	if err := validateSkillServiceConfig(declaration.Name, &cfg); err != nil {
		return nil, err
	}

	cfgMap, err := skillServiceConfigMap(cfg)
	if err != nil {
		return nil, fmt.Errorf("encode skill service %q: %w", declaration.Name, err)
	}
	cfgStruct, err := structpb.NewStruct(cfgMap)
	if err != nil {
		return nil, fmt.Errorf("encode skill service %q: %w", declaration.Name, err)
	}

	if !found {
		if _, err := projectClient.AddService(ctx, &azdext.AddServiceRequest{
			Service: &azdext.ServiceConfig{
				Name:                 declaration.Name,
				Host:                 aiSkillHost,
				AdditionalProperties: cfgStruct,
			},
		}); err != nil {
			return nil, fmt.Errorf("add azure.ai.skill service %q: %w", declaration.Name, err)
		}
		return &skillServiceUpsertResult{
			Name:        declaration.Name,
			Host:        aiSkillHost,
			ProjectPath: project.GetPath(),
			Created:     true,
		}, nil
	}

	sectionResponse, err := projectClient.GetServiceConfigSection(
		ctx,
		&azdext.GetServiceConfigSectionRequest{ServiceName: declaration.Name},
	)
	if err != nil {
		return nil, fmt.Errorf("read azure.ai.skill service %q from azure.yaml: %w", declaration.Name, err)
	}
	if !sectionResponse.GetFound() || sectionResponse.GetSection() == nil {
		return nil, fmt.Errorf("azure.ai.skill service %q disappeared from azure.yaml", declaration.Name)
	}

	merged := sectionResponse.GetSection().AsMap()
	for _, field := range skillServiceOwnedFields {
		delete(merged, field)
	}
	maps.Copy(merged, cfgMap)
	merged["host"] = aiSkillHost

	section, err := structpb.NewStruct(merged)
	if err != nil {
		return nil, fmt.Errorf("encode updated skill service %q: %w", declaration.Name, err)
	}
	if _, err := projectClient.SetServiceConfigSection(ctx, &azdext.SetServiceConfigSectionRequest{
		ServiceName: declaration.Name,
		Section:     section,
	}); err != nil {
		return nil, fmt.Errorf("update azure.ai.skill service %q in azure.yaml: %w", declaration.Name, err)
	}

	return &skillServiceUpsertResult{
		Name:        declaration.Name,
		Host:        aiSkillHost,
		ProjectPath: project.GetPath(),
	}, nil
}

func skillServiceConfigMap(cfg skillServiceConfig) (map[string]any, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func portableSkillArchiveReference(serviceRoot, source string) (string, error) {
	if strings.TrimSpace(serviceRoot) == "" {
		return "", fmt.Errorf("service directory is empty")
	}

	rootAbs, err := filepath.Abs(serviceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve service directory %q: %w", serviceRoot, err)
	}
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("resolve skill archive path %q: %w", source, err)
	}

	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot resolve skill service directory %q: %s", serviceRoot, err),
			"verify the service project path exists and is readable",
		)
	}
	sourceReal, err := filepath.EvalSymlinks(sourceAbs)
	if err != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot resolve skill archive path %q: %s", source, err),
			"verify the archive or directory exists and is readable",
		)
	}

	relative, err := filepath.Rel(rootAbs, sourceAbs)
	if err != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot make skill archive path %q portable: %s", source, err),
			"move the skill archive or directory onto the same volume as the skill service directory",
		)
	}
	resolvedRelative, err := filepath.Rel(rootReal, sourceReal)
	if err != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot verify skill archive path %q: %s", source, err),
			"move the skill archive or directory onto the same volume as the skill service directory",
		)
	}
	if pathEscapesBase(relative) || pathEscapesBase(resolvedRelative) {
		return "", exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf(
				"cannot add archive %q to azure.yaml because it is outside the skill service directory at %q",
				source,
				serviceRoot,
			),
			"move the skill archive or directory inside the skill service directory and retry",
		)
	}
	if relative == "." || relative == "" {
		return "", exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot use the skill service directory %q itself as the archive source", serviceRoot),
			"place the skill in a child directory or .zip file and retry",
		)
	}

	return filepath.ToSlash(resolvedRelative), nil
}

func pathEscapesBase(relative string) bool {
	return filepath.IsAbs(relative) ||
		relative == ".." ||
		strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}
