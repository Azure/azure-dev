// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/paths"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type agentServiceMutation string

const (
	agentServiceMutationAdded     agentServiceMutation = "added"
	agentServiceMutationUpdated   agentServiceMutation = "updated"
	agentServiceMutationUnchanged agentServiceMutation = "unchanged"
)

type agentServiceDeclarationInput struct {
	ServiceName     string
	ProjectRoot     string
	Definition      projectpkg.AgentDefinitionFile
	Dependencies    agentDependencyPlan
	ExistingService *structpb.Struct
	SourceOverride  *string
}

type agentServiceDeclarationPlan struct {
	Mutation         agentServiceMutation
	DesiredService   *structpb.Struct
	NewServiceConfig *azdext.ServiceConfig
	Warnings         []string
}

func planAgentServiceDeclaration(
	input agentServiceDeclarationInput,
) (agentServiceDeclarationPlan, error) {
	if err := validateAgentServiceDeclarationName(input.ServiceName); err != nil {
		return agentServiceDeclarationPlan{}, err
	}
	if err := validateLoadedAgentDefinition(input.Definition); err != nil {
		return agentServiceDeclarationPlan{}, err
	}
	if err := validateExistingAgentService(input.ServiceName, input.ExistingService); err != nil {
		return agentServiceDeclarationPlan{}, err
	}

	reference, definitionPath, err := agentDefinitionServiceReference(
		input.ProjectRoot,
		input.Definition.Path,
		input.ServiceName,
	)
	if err != nil {
		return agentServiceDeclarationPlan{}, err
	}
	projectValue, sourcePath, err := agentServiceProjectValue(input, definitionPath)
	if err != nil {
		return agentServiceDeclarationPlan{}, err
	}
	ownedProperties, err := projectpkg.AgentDefinitionOwnedServiceProperties()
	if err != nil {
		return agentServiceDeclarationPlan{}, fmt.Errorf(
			"loading Agent-owned service properties: %w",
			err,
		)
	}

	desired := &structpb.Struct{Fields: make(map[string]*structpb.Value)}
	if input.ExistingService != nil {
		desired = proto.Clone(input.ExistingService).(*structpb.Struct)
		if desired.Fields == nil {
			desired.Fields = make(map[string]*structpb.Value)
		}
	}
	for _, property := range ownedProperties {
		delete(desired.Fields, property)
	}
	desired.Fields["host"] = structpb.NewStringValue(AiAgentHost)
	desired.Fields["$ref"] = structpb.NewStringValue(reference)
	desired.Fields["project"] = projectValue
	desired.Fields["uses"] = agentServiceStringListValue(input.Dependencies.Uses)

	plan := agentServiceDeclarationPlan{
		DesiredService: desired,
		Warnings:       slices.Clone(input.Dependencies.Warnings),
	}
	if input.ExistingService == nil {
		plan.Mutation = agentServiceMutationAdded
		plan.NewServiceConfig = newAgentServiceConfig(
			input.ServiceName,
			reference,
			sourcePath,
			input.Dependencies.Uses,
		)
	} else if proto.Equal(input.ExistingService, desired) {
		plan.Mutation = agentServiceMutationUnchanged
	} else {
		plan.Mutation = agentServiceMutationUpdated
	}

	return plan, nil
}

func validateAgentServiceDeclarationName(serviceName string) error {
	if err := azdext.ValidateServiceName(serviceName); err != nil {
		suggestion := "use a valid azd service name"
		if validationErr, ok := errors.AsType[*azdext.ValidationError](err); ok &&
			strings.TrimSpace(validationErr.Message) != "" {
			suggestion = validationErr.Message
		}
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf(
				"agent service name %q cannot be used as an azure.yaml service name",
				serviceName,
			),
			suggestion,
		)
	}
	return nil
}

func validateExistingAgentService(
	serviceName string,
	existing *structpb.Struct,
) error {
	if existing == nil {
		return nil
	}

	if hostValue, found := existing.GetFields()["host"]; found {
		if hostValue == nil {
			return invalidAgentServiceHostConfig(serviceName)
		}
		host, ok := hostValue.Kind.(*structpb.Value_StringValue)
		if !ok {
			return invalidAgentServiceHostConfig(serviceName)
		}
		if host.StringValue != "" && host.StringValue != AiAgentHost {
			return invalidServiceHostError(serviceName, host.StringValue, AiAgentHost)
		}
	}

	configValue, found := existing.GetFields()["config"]
	if !found || configValue == nil {
		return nil
	}
	switch config := configValue.Kind.(type) {
	case *structpb.Value_NullValue:
		return nil
	case *structpb.Value_StructValue:
		if len(config.StructValue.GetFields()) == 0 {
			return nil
		}
		return incompatibleAgentServiceConfigError(serviceName)
	default:
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf(
				"agent service %q has a non-object config value",
				serviceName,
			),
			"remove the incompatible config value before adding a file-backed definition",
		)
	}
}

func invalidAgentServiceHostConfig(serviceName string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidServiceConfig,
		fmt.Sprintf("agent service %q has a non-string host value", serviceName),
		"set host to azure.ai.agent before adding a file-backed definition",
	)
}

func incompatibleAgentServiceConfigError(serviceName string) error {
	return exterrors.Validation(
		exterrors.CodeDeprecatedAgentServiceConfig,
		fmt.Sprintf(
			"cannot update agent service %q because its non-empty config block "+
				"is incompatible with a file-backed definition",
			serviceName,
		),
		"remove the unsupported config block or migrate its Agent fields to the service level",
	)
}

func agentDefinitionServiceReference(
	projectRoot string,
	filePath string,
	serviceName string,
) (string, string, error) {
	if strings.TrimSpace(filePath) == "" {
		return "", "", invalidAgentDefinitionServicePath(serviceName, filePath, "path is empty")
	}
	resolved, err := paths.Join(projectRoot, filePath)
	if err != nil {
		return "", "", invalidAgentDefinitionServicePath(serviceName, filePath, err.Error())
	}
	rootAbs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", "", invalidAgentDefinitionServicePath(
			serviceName,
			filePath,
			fmt.Sprintf("cannot resolve project root: %s", err),
		)
	}
	relativePath, err := filepath.Rel(rootAbs, resolved)
	if err != nil {
		return "", "", invalidAgentDefinitionServicePath(
			serviceName,
			filePath,
			fmt.Sprintf("cannot make path relative to project: %s", err),
		)
	}
	relativePath = filepath.ToSlash(relativePath)
	if relativePath == "." || relativePath == "" {
		return "", "", invalidAgentDefinitionServicePath(
			serviceName,
			filePath,
			"path must identify a file inside the project",
		)
	}

	switch strings.ToLower(path.Ext(relativePath)) {
	case ".yaml", ".yml", ".json":
	default:
		return "", "", invalidAgentDefinitionServicePath(
			serviceName,
			filePath,
			"expected a .yaml, .yml, or .json file",
		)
	}

	return "./" + relativePath, relativePath, nil
}

func invalidAgentDefinitionServicePath(
	serviceName string,
	filePath string,
	reason string,
) error {
	return exterrors.Validation(
		exterrors.CodeInvalidFilePath,
		fmt.Sprintf(
			"invalid agent definition file path %q for service %q: %s",
			filePath,
			serviceName,
			reason,
		),
		"load a validated .yaml, .yml, or .json definition inside the project",
	)
}

func agentServiceProjectValue(
	input agentServiceDeclarationInput,
	definitionPath string,
) (*structpb.Value, string, error) {
	if input.SourceOverride != nil {
		sourcePath, err := normalizeAgentServiceSource(
			input.ProjectRoot,
			*input.SourceOverride,
			true,
		)
		if err != nil {
			return nil, "", invalidAgentServiceSource(
				input.ServiceName,
				*input.SourceOverride,
				err,
			)
		}
		return structpb.NewStringValue(sourcePath), sourcePath, nil
	}

	if input.ExistingService != nil {
		projectValue, found := input.ExistingService.GetFields()["project"]
		if found {
			if projectValue == nil {
				return nil, "", invalidAgentServiceProjectValue(input.ServiceName)
			}
			projectString, ok := projectValue.Kind.(*structpb.Value_StringValue)
			if !ok {
				return nil, "", invalidAgentServiceProjectValue(input.ServiceName)
			}
			if _, err := normalizeAgentServiceSource(
				input.ProjectRoot,
				projectString.StringValue,
				false,
			); err != nil {
				return nil, "", invalidAgentServiceSource(
					input.ServiceName,
					projectString.StringValue,
					err,
				)
			}
			return proto.Clone(projectValue).(*structpb.Value), projectString.StringValue, nil
		}
	}

	sourcePath, err := normalizeAgentServiceSource(
		input.ProjectRoot,
		path.Dir(definitionPath),
		false,
	)
	if err != nil {
		return nil, "", invalidAgentServiceSource(
			input.ServiceName,
			path.Dir(definitionPath),
			err,
		)
	}
	return structpb.NewStringValue(sourcePath), sourcePath, nil
}

func normalizeAgentServiceSource(
	projectRoot string,
	source string,
	allowAbsolute bool,
) (string, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return "", fmt.Errorf("project root is empty")
	}
	rootAbs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("cannot resolve project root: %w", err)
	}

	relativePath := source
	if filepath.IsAbs(source) {
		if !allowAbsolute {
			return "", fmt.Errorf("path must be relative to the project root")
		}
		sourceAbs, err := filepath.Abs(source)
		if err != nil {
			return "", fmt.Errorf("cannot resolve source path: %w", err)
		}
		relativePath, err = filepath.Rel(rootAbs, sourceAbs)
		if err != nil {
			return "", fmt.Errorf("cannot make source relative to the project: %w", err)
		}
	}

	resolved, err := paths.JoinAllowRoot(rootAbs, filepath.ToSlash(relativePath))
	if err != nil {
		return "", err
	}
	relativePath, err = filepath.Rel(rootAbs, resolved)
	if err != nil {
		return "", fmt.Errorf("cannot make source relative to the project: %w", err)
	}
	return filepath.ToSlash(relativePath), nil
}

func invalidAgentServiceSource(serviceName, source string, cause error) error {
	return exterrors.Validation(
		exterrors.CodeInvalidFilePath,
		fmt.Sprintf(
			"invalid project source %q for agent service %q: %s",
			source,
			serviceName,
			cause,
		),
		"use a directory path contained within the current azd project",
	)
}

func invalidAgentServiceProjectValue(serviceName string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidServiceConfig,
		fmt.Sprintf("agent service %q has a non-string project value", serviceName),
		"set project to a path inside the project or pass --source to override it",
	)
}

func agentServiceStringListValue(values []string) *structpb.Value {
	items := make([]*structpb.Value, len(values))
	for index, value := range values {
		items[index] = structpb.NewStringValue(value)
	}
	return structpb.NewListValue(&structpb.ListValue{Values: items})
}

func newAgentServiceConfig(
	serviceName string,
	reference string,
	sourcePath string,
	uses []string,
) *azdext.ServiceConfig {
	return &azdext.ServiceConfig{
		Name:         serviceName,
		Host:         AiAgentHost,
		RelativePath: sourcePath,
		Uses:         slices.Clone(uses),
		AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
			"$ref": structpb.NewStringValue(reference),
		}},
	}
}
