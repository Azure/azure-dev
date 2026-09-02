// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azure.ai.routines/internal/exterrors"
	"azure.ai.routines/internal/pkg/routines"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

const aiAgentHost = "azure.ai.agent"

var routineCoreServiceFields = map[string]struct{}{
	"apiVersion": {}, "condition": {}, "config": {}, "dist": {},
	"docker": {}, "env": {}, "hooks": {}, "host": {}, "image": {},
	"infra": {}, "k8s": {}, "language": {}, "module": {}, "project": {},
	"remoteBuild": {}, "resourceGroup": {}, "resourceName": {}, "uses": {},
}

type routineProjectClient interface {
	Get(
		context.Context,
		*azdext.EmptyRequest,
		...grpc.CallOption,
	) (*azdext.GetProjectResponse, error)
	GetConfigSection(
		context.Context,
		*azdext.GetProjectConfigSectionRequest,
		...grpc.CallOption,
	) (*azdext.GetProjectConfigSectionResponse, error)
	AddService(
		context.Context,
		*azdext.AddServiceRequest,
		...grpc.CallOption,
	) (*azdext.EmptyResponse, error)
	SetServiceConfigValue(
		context.Context,
		*azdext.SetServiceConfigValueRequest,
		...grpc.CallOption,
	) (*azdext.EmptyResponse, error)
}

type routineServicePlan struct {
	projectClient     routineProjectClient
	routineName       string
	manifestRef       string
	services          map[string]*azdext.ServiceConfig
	existing          *routineServiceState
	agentServiceName  string
	preserveAgentUses bool
}

type routineServiceUpsertResult struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Ref     string `json:"ref"`
	Created bool   `json:"created"`
}

type routineServiceState struct {
	host       string
	uses       []string
	properties *structpb.Struct
	pathPrefix string
}

func upsertRoutineServiceToProject(
	ctx context.Context,
	routine *routines.Routine,
	file string,
) (*routineServiceUpsertResult, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, fmt.Errorf("connecting to the current azd project: %w", err)
	}
	defer azdClient.Close()
	return upsertRoutineService(ctx, azdClient.Project(), routine, file)
}

func upsertRoutineService(
	ctx context.Context,
	projectClient routineProjectClient,
	routine *routines.Routine,
	file string,
) (*routineServiceUpsertResult, error) {
	plan, err := planRoutineService(ctx, projectClient, routine, file)
	if err != nil {
		return nil, err
	}
	return plan.apply(ctx)
}

func planRoutineService(
	ctx context.Context,
	projectClient routineProjectClient,
	routine *routines.Routine,
	file string,
) (*routineServicePlan, error) {
	if routine == nil || strings.TrimSpace(routine.Name) == "" {
		return nil, fmt.Errorf("routine name is required for azure.yaml authoring")
	}
	if err := azdext.ValidateServiceName(routine.Name); err != nil {
		suggestion := "use a valid azd service name"
		if validationErr, ok := errors.AsType[*azdext.ValidationError](err); ok {
			suggestion = validationErr.Message
		}
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("routine name %q cannot be used as an azure.yaml service name", routine.Name),
			suggestion,
		)
	}

	projectResp, err := projectClient.Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, fmt.Errorf("reading azure.yaml before adding routine %q: %w", routine.Name, err)
	}
	project := projectResp.GetProject()
	if project == nil || strings.TrimSpace(project.GetPath()) == "" {
		return nil, fmt.Errorf("reading azure.yaml before adding routine %q: project configuration is empty", routine.Name)
	}

	services, err := currentRoutineProjectServices(ctx, projectClient)
	if err != nil {
		return nil, fmt.Errorf("reading current azure.yaml services: %w", err)
	}
	existing := routineServiceStateFromConfig(services[routine.Name])
	if existing != nil && existing.host != aiRoutineHost {
		return nil, exterrors.Validation(
			exterrors.CodeProjectServiceConflict,
			fmt.Sprintf(
				"cannot add routine %q to azure.yaml: service name is already used by host %q",
				routine.Name,
				existing.host,
			),
			"rename the routine or the conflicting azure.yaml service",
		)
	}
	if existing != nil && !routineServiceCanUpdateFileReference(existing) {
		return nil, exterrors.Validation(
			exterrors.CodeProjectServiceConflict,
			fmt.Sprintf("cannot update routine service %q as a file-backed declaration", routine.Name),
			"remove inline triggers and action overrides, or remove the service before rerunning the command",
		)
	}

	manifestRef, err := portableRoutineManifestReference(project.GetPath(), file)
	if err != nil {
		return nil, err
	}

	agentServiceName, err := resolveRoutineAgentService(services, project.GetPath(), routine)
	if err != nil {
		return nil, err
	}
	preserveAgentUses := existing != nil &&
		routine.Action != nil &&
		strings.TrimSpace(routine.Action.AgentName) != "" &&
		agentServiceName == ""

	return &routineServicePlan{
		projectClient:     projectClient,
		routineName:       routine.Name,
		manifestRef:       manifestRef,
		services:          services,
		existing:          existing,
		agentServiceName:  agentServiceName,
		preserveAgentUses: preserveAgentUses,
	}, nil
}

func currentRoutineProjectServices(
	ctx context.Context,
	projectClient routineProjectClient,
) (map[string]*azdext.ServiceConfig, error) {
	response, err := projectClient.GetConfigSection(ctx, &azdext.GetProjectConfigSectionRequest{Path: "services"})
	if err != nil {
		return nil, err
	}
	if !response.GetFound() || response.GetSection() == nil {
		return map[string]*azdext.ServiceConfig{}, nil
	}

	services := make(map[string]*azdext.ServiceConfig, len(response.GetSection().GetFields()))
	for name, value := range response.GetSection().GetFields() {
		raw := value.GetStructValue()
		if raw == nil {
			continue
		}
		additionalFields := maps.Clone(raw.GetFields())
		for field := range routineCoreServiceFields {
			delete(additionalFields, field)
		}
		var additionalProperties *structpb.Struct
		if len(additionalFields) > 0 {
			additionalProperties = &structpb.Struct{Fields: additionalFields}
		}
		services[name] = &azdext.ServiceConfig{
			Name:                 name,
			Host:                 raw.GetFields()["host"].GetStringValue(),
			RelativePath:         raw.GetFields()["project"].GetStringValue(),
			Uses:                 stringList(raw.GetFields()["uses"]),
			Config:               raw.GetFields()["config"].GetStructValue(),
			AdditionalProperties: additionalProperties,
		}
	}
	return services, nil
}

func routineServiceStateFromConfig(service *azdext.ServiceConfig) *routineServiceState {
	if service == nil {
		return nil
	}
	properties := routineConfigProperties(service)
	pathPrefix := ""
	if properties != nil && properties == service.GetConfig() {
		pathPrefix = "config."
	}
	return &routineServiceState{
		host:       service.GetHost(),
		uses:       service.GetUses(),
		properties: properties,
		pathPrefix: pathPrefix,
	}
}

func routineServiceCanUpdateFileReference(service *routineServiceState) bool {
	if service == nil || service.pathPrefix != "" || service.properties == nil {
		return false
	}
	fields := service.properties.GetFields()
	ref := fields["$ref"].GetStringValue()
	return strings.TrimSpace(ref) != "" && fields["triggers"] == nil && fields["action"] == nil
}

func stringList(value *structpb.Value) []string {
	if value == nil || value.GetListValue() == nil {
		return nil
	}
	items := value.GetListValue().GetValues()
	result := make([]string, 0, len(items))
	for _, item := range items {
		if value := item.GetStringValue(); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func (p *routineServicePlan) apply(ctx context.Context) (*routineServiceUpsertResult, error) {
	uses := reconcileRoutineUses(
		p.services,
		p.existing,
		p.agentServiceName,
		p.preserveAgentUses,
	)
	if p.existing != nil {
		refValue := structpb.NewStringValue(p.manifestRef)
		if p.existing.properties == nil ||
			!proto.Equal(refValue, p.existing.properties.GetFields()["$ref"]) {
			if err := p.setValue(ctx, "$ref", refValue); err != nil {
				return nil, err
			}
		}
		if !slices.Equal(p.existing.uses, uses) {
			values := make([]*structpb.Value, len(uses))
			for index, use := range uses {
				values[index] = structpb.NewStringValue(use)
			}
			value := structpb.NewListValue(&structpb.ListValue{Values: values})
			if err := p.setValue(ctx, "uses", value); err != nil {
				return nil, err
			}
		}
		return p.result(false), nil
	}

	service := &azdext.ServiceConfig{
		Name: p.routineName,
		Host: aiRoutineHost,
		AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
			"$ref": structpb.NewStringValue(p.manifestRef),
		}},
		Uses: uses,
	}

	_, err := p.projectClient.AddService(ctx, &azdext.AddServiceRequest{Service: service})
	if err != nil {
		return nil, fmt.Errorf("adding routine service %q to azure.yaml: %w", p.routineName, err)
	}
	return p.result(true), nil
}

func (p *routineServicePlan) result(created bool) *routineServiceUpsertResult {
	return &routineServiceUpsertResult{
		Name:    p.routineName,
		Host:    aiRoutineHost,
		Ref:     p.manifestRef,
		Created: created,
	}
}

func (p *routineServicePlan) setValue(ctx context.Context, path string, value *structpb.Value) error {
	_, err := p.projectClient.SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: p.routineName,
		Path:        path,
		Value:       value,
	})
	if err != nil {
		return fmt.Errorf("setting %s for routine service %q in azure.yaml: %w", path, p.routineName, err)
	}
	return nil
}

func portableRoutineManifestReference(projectRoot, file string) (string, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("resolving azd project path: %w", err)
	}
	manifest, err := filepath.Abs(file)
	if err != nil {
		return "", fmt.Errorf("resolving routine manifest path: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolving azd project path: %w", err)
	}
	resolvedManifest, err := filepath.EvalSymlinks(manifest)
	if err != nil {
		return "", fmt.Errorf("resolving routine manifest path: %w", err)
	}
	contained, err := filepath.Rel(resolvedRoot, resolvedManifest)
	if err != nil {
		return "", fmt.Errorf("checking routine manifest location: %w", err)
	}
	if contained == ".." || strings.HasPrefix(contained, ".."+string(filepath.Separator)) {
		return "", exterrors.Validation(
			exterrors.CodeInvalidRoutineManifest,
			"routine manifest must be inside the current azd project",
			"move the routine manifest into the project and rerun the command",
		)
	}
	reference, err := filepath.Rel(resolvedRoot, resolvedManifest)
	if err != nil {
		return "", fmt.Errorf("making routine manifest path relative to azure.yaml: %w", err)
	}
	reference = filepath.ToSlash(reference)
	if !strings.HasPrefix(reference, ".") {
		reference = "./" + reference
	}
	return reference, nil
}

func resolveRoutineAgentService(
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
	routine *routines.Routine,
) (string, error) {
	if routine.Action == nil || strings.TrimSpace(routine.Action.AgentName) == "" {
		return "", nil
	}

	agentName := strings.TrimSpace(routine.Action.AgentName)
	var matches []string
	for serviceName, service := range services {
		if service.GetHost() != aiAgentHost {
			continue
		}
		resolvedName, err := routineAgentName(service, projectRoot)
		if err != nil {
			return "", err
		}
		if resolvedName == agentName {
			matches = append(matches, serviceName)
		}
	}
	slices.Sort(matches)

	switch len(matches) {
	case 0:
		service := services[agentName]
		if service != nil && service.GetHost() == aiAgentHost {
			resolvedName, err := routineAgentName(service, projectRoot)
			if err != nil {
				return "", err
			}
			if resolvedName == "" {
				return agentName, nil
			}
		}
		return "", nil
	case 1:
		return matches[0], nil
	default:
		return "", exterrors.Validation(
			exterrors.CodeProjectServiceConflict,
			fmt.Sprintf(
				"cannot add routine %q to azure.yaml: agent name %q matches multiple services: %s",
				routine.Name,
				agentName,
				strings.Join(matches, ", "),
			),
			"ensure only one azure.ai.agent service declares that Agent name",
		)
	}
}

func routineAgentName(service *azdext.ServiceConfig, projectRoot string) (string, error) {
	if service == nil {
		return "", nil
	}
	for _, properties := range []*structpb.Struct{service.GetAdditionalProperties(), service.GetConfig()} {
		if properties == nil || len(properties.GetFields()) == 0 {
			continue
		}
		values := properties.AsMap()
		if projectRoot != "" {
			if ref, ok := values["$ref"].(string); ok && remoteRoutineRefPattern.MatchString(strings.TrimSpace(ref)) {
				return "", exterrors.Validation(
					exterrors.CodeInvalidRoutineManifest,
					"remote Agent service $ref is not supported",
					"set $ref to a local Agent manifest",
				)
			}
			resolved, err := foundry.ResolveFileRefs(values, projectRoot)
			if err != nil {
				return "", err
			}
			values = resolved
		}
		kind, hasKind := values["kind"].(string)
		if !hasKind || strings.TrimSpace(kind) == "" {
			continue
		}
		if name, ok := values["name"].(string); ok && strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name), nil
		}
		return "", nil
	}
	return routineAgentNameFromLegacyManifest(service, projectRoot)
}

func routineAgentNameFromLegacyManifest(service *azdext.ServiceConfig, projectRoot string) (string, error) {
	if projectRoot == "" {
		return "", nil
	}

	root, err := os.OpenRoot(projectRoot)
	if err != nil {
		return "", fmt.Errorf("opening project root: %w", err)
	}
	defer root.Close()

	for _, fileName := range []string{"agent.yaml", "agent.yml"} {
		manifestPath := filepath.Join(service.GetRelativePath(), fileName)
		data, err := root.ReadFile(manifestPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("reading legacy Agent manifest for service %q: %w", service.GetName(), err)
		}
		var manifest struct {
			Name string `yaml:"name"`
		}
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			return "", fmt.Errorf("parsing legacy Agent manifest for service %q: %w", service.GetName(), err)
		}
		return strings.TrimSpace(manifest.Name), nil
	}

	return "", nil
}

func reconcileRoutineUses(
	services map[string]*azdext.ServiceConfig,
	existing *routineServiceState,
	agentServiceName string,
	preserveAgentUses bool,
) []string {
	var uses []string
	seen := map[string]struct{}{}
	appendUse := func(serviceName string) {
		if serviceName == "" {
			return
		}
		if _, exists := seen[serviceName]; exists {
			return
		}
		seen[serviceName] = struct{}{}
		uses = append(uses, serviceName)
	}

	if existing != nil {
		for _, serviceName := range existing.uses {
			if dependency := services[serviceName]; dependency != nil &&
				dependency.GetHost() == aiAgentHost && !preserveAgentUses {
				continue
			}
			appendUse(serviceName)
		}
	}
	appendUse(agentServiceName)
	return uses
}
