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
	"description",
	"instructions",
	"tools",
}

type skillServiceDeclaration struct {
	Name          string
	Config        skillServiceConfig
	ArchiveSource string
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

func saveSkillServiceToProject(ctx context.Context, declaration skillServiceDeclaration) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("creating azd client to update azure.yaml: %w", err)
	}
	defer azdClient.Close()

	return upsertSkillService(ctx, azdClient.Project(), declaration)
}

// upsertSkillService adds or updates the azure.ai.skill service owned by this
// extension. Existing non-skill fields (including uses:) are preserved.
func upsertSkillService(
	ctx context.Context,
	projectClient skillProjectClient,
	declaration skillServiceDeclaration,
) error {
	projectResp, err := projectClient.Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeProjectManifestNotFound,
			fmt.Sprintf("cannot save skill %q to azure.yaml: %s", declaration.Name, err),
			"run this command from an azd project containing azure.yaml, or omit --save-to-azure-yaml",
		)
	}
	project := projectResp.GetProject()
	if project == nil {
		return exterrors.Dependency(
			exterrors.CodeProjectManifestNotFound,
			fmt.Sprintf("cannot save skill %q to azure.yaml: no azd project is loaded", declaration.Name),
			"run this command from an azd project containing azure.yaml, or omit --save-to-azure-yaml",
		)
	}

	cfg := declaration.Config
	if declaration.ArchiveSource != "" {
		archive, err := portableSkillArchiveReference(project.GetPath(), declaration.ArchiveSource)
		if err != nil {
			return err
		}
		cfg.Archive = archive
	}

	cfgMap, err := skillServiceConfigMap(cfg)
	if err != nil {
		return fmt.Errorf("encoding skill service %q: %w", declaration.Name, err)
	}
	cfgStruct, err := structpb.NewStruct(cfgMap)
	if err != nil {
		return fmt.Errorf("encoding skill service %q: %w", declaration.Name, err)
	}

	existing, found := project.GetServices()[declaration.Name]
	if !found {
		_, err := projectClient.AddService(ctx, &azdext.AddServiceRequest{
			Service: &azdext.ServiceConfig{
				Name:                 declaration.Name,
				Host:                 aiSkillHost,
				AdditionalProperties: cfgStruct,
			},
		})
		if err != nil {
			return fmt.Errorf("adding azure.ai.skill service %q: %w", declaration.Name, err)
		}
		return nil
	}

	if existing.GetHost() != aiSkillHost {
		return exterrors.Validation(
			exterrors.CodeSkillServiceConflict,
			fmt.Sprintf(
				"cannot save skill %q to azure.yaml: service %q already uses host %q",
				declaration.Name,
				declaration.Name,
				existing.GetHost(),
			),
			"choose a different skill name or rename the existing azure.yaml service",
		)
	}

	sectionResp, err := projectClient.GetServiceConfigSection(ctx, &azdext.GetServiceConfigSectionRequest{
		ServiceName: declaration.Name,
	})
	if err != nil {
		return fmt.Errorf("reading azure.ai.skill service %q from azure.yaml: %w", declaration.Name, err)
	}

	merged := map[string]any{"host": aiSkillHost}
	if sectionResp.GetFound() && sectionResp.GetSection() != nil {
		merged = sectionResp.GetSection().AsMap()
	}
	for _, field := range skillServiceOwnedFields {
		delete(merged, field)
	}
	maps.Copy(merged, cfgMap)
	merged["host"] = aiSkillHost

	section, err := structpb.NewStruct(merged)
	if err != nil {
		return fmt.Errorf("encoding updated skill service %q: %w", declaration.Name, err)
	}
	if _, err := projectClient.SetServiceConfigSection(ctx, &azdext.SetServiceConfigSectionRequest{
		ServiceName: declaration.Name,
		Section:     section,
	}); err != nil {
		return fmt.Errorf("updating azure.ai.skill service %q in azure.yaml: %w", declaration.Name, err)
	}

	return nil
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

func portableSkillArchiveReference(projectPath, source string) (string, error) {
	projectRoot := projectPath
	if projectRoot == "" {
		var err error
		projectRoot, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolving current project directory: %w", err)
		}
	}

	rootAbs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("resolving project path %q: %w", projectRoot, err)
	}
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("resolving skill archive path %q: %w", source, err)
	}
	relative, err := filepath.Rel(rootAbs, sourceAbs)
	if err != nil {
		return "", fmt.Errorf("making skill archive path portable: %w", err)
	}
	if filepath.IsAbs(relative) ||
		relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf(
				"cannot save archive reference %q to azure.yaml because it is outside the azd project at %q",
				source,
				projectRoot,
			),
			"move the skill archive or directory inside the azd project and retry",
		)
	}

	return filepath.ToSlash(relative), nil
}
