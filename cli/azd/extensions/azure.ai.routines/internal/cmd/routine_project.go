// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
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

var routineManagedServiceFields = []string{
	"description",
	"enabled",
	"triggers",
	"action",
}

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

type routineProjectAuthor struct {
	projectClient routineProjectClient
	routine       *routines.Routine
	close         func()
}

type routineServicePlan struct {
	projectClient     routineProjectClient
	routineName       string
	properties        *structpb.Struct
	services          map[string]*azdext.ServiceConfig
	existing          *routineServiceState
	agentServiceName  string
	preserveAgentUses bool
}

type routineServiceState struct {
	host       string
	uses       []string
	properties *structpb.Struct
	pathPrefix string
}

func newRoutineProjectAuthor(ctx context.Context) (*routineProjectAuthor, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, fmt.Errorf("connecting to the current azd project: %w", err)
	}

	return &routineProjectAuthor{
		projectClient: azdClient.Project(),
		close:         azdClient.Close,
	}, nil
}

func (a *routineProjectAuthor) Prepare(ctx context.Context, routine *routines.Routine) error {
	if _, err := planRoutineService(ctx, a.projectClient, routine); err != nil {
		return err
	}
	a.routine = routine
	return nil
}

func (a *routineProjectAuthor) Close() {
	if a.close != nil {
		a.close()
	}
}

func (a *routineProjectAuthor) Apply(ctx context.Context) error {
	if a.routine == nil {
		return fmt.Errorf("routine project author is not prepared")
	}
	plan, err := planRoutineService(ctx, a.projectClient, a.routine)
	if err != nil {
		return err
	}
	return plan.apply(ctx)
}

func upsertRoutineService(
	ctx context.Context,
	projectClient routineProjectClient,
	routine *routines.Routine,
) error {
	plan, err := planRoutineService(ctx, projectClient, routine)
	if err != nil {
		return err
	}
	return plan.apply(ctx)
}

func planRoutineService(
	ctx context.Context,
	projectClient routineProjectClient,
	routine *routines.Routine,
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
	if project == nil {
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

	properties, err := routineServiceProperties(routine)
	if err != nil {
		return nil, err
	}

	agentServiceName, err := resolveRoutineAgentService(services, project.GetPath(), routine)
	if err != nil {
		return nil, err
	}
	preserveAgentUses, err := shouldPreserveRoutineAgentUses(existing, project.GetPath(), routine, agentServiceName)
	if err != nil {
		return nil, err
	}

	return &routineServicePlan{
		projectClient:     projectClient,
		routineName:       routine.Name,
		properties:        properties,
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

func (p *routineServicePlan) apply(ctx context.Context) error {
	uses := reconcileRoutineUses(
		p.services,
		p.existing,
		p.agentServiceName,
		p.preserveAgentUses,
	)
	if p.existing != nil {
		for _, field := range routineManagedServiceFields {
			if value, found := p.properties.GetFields()[field]; found {
				if p.existing.properties != nil && proto.Equal(value, p.existing.properties.GetFields()[field]) {
					continue
				}
				if err := p.setValue(ctx, p.existing.pathPrefix+field, value); err != nil {
					return err
				}
			}
		}
		if !slices.Equal(p.existing.uses, uses) {
			values := make([]*structpb.Value, len(uses))
			for index, use := range uses {
				values[index] = structpb.NewStringValue(use)
			}
			value := structpb.NewListValue(&structpb.ListValue{Values: values})
			if err := p.setValue(ctx, "uses", value); err != nil {
				return err
			}
		}
		return nil
	}

	service := &azdext.ServiceConfig{
		Name:                 p.routineName,
		Host:                 aiRoutineHost,
		AdditionalProperties: p.properties,
		Uses:                 uses,
	}

	_, err := p.projectClient.AddService(ctx, &azdext.AddServiceRequest{Service: service})
	if err != nil {
		return fmt.Errorf("adding routine service %q to azure.yaml: %w", p.routineName, err)
	}
	return nil
}

func shouldPreserveRoutineAgentUses(
	existing *routineServiceState,
	projectRoot string,
	routine *routines.Routine,
	agentServiceName string,
) (bool, error) {
	if existing == nil || routine.Action == nil || agentServiceName != "" {
		return false, nil
	}
	requestedAgentName := strings.TrimSpace(routine.Action.AgentName)
	if requestedAgentName == "" {
		return false, nil
	}
	existingService := &azdext.ServiceConfig{Name: routine.Name}
	if existing.pathPrefix == "config." {
		existingService.Config = existing.properties
	} else {
		existingService.AdditionalProperties = existing.properties
	}
	existingRoutine, err := parseRoutineServiceConfig(existingService, projectRoot)
	if err != nil {
		return false, fmt.Errorf("reading existing routine service %q: %w", routine.Name, err)
	}
	return existingRoutine.Action != nil &&
		strings.TrimSpace(existingRoutine.Action.AgentName) == requestedAgentName, nil
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

func routineProjectAuthoringRetry(name string) string {
	return fmt.Sprintf("rerun `azd ai routine update %q --add-to-project`", name)
}

func routineServiceProperties(routine *routines.Routine) (*structpb.Struct, error) {
	data, err := json.Marshal(routine)
	if err != nil {
		return nil, fmt.Errorf("encoding routine %q for azure.yaml: %w", routine.Name, err)
	}

	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("decoding routine %q for azure.yaml: %w", routine.Name, err)
	}
	delete(values, "name")
	delete(values, "created_at")
	delete(values, "updated_at")
	values["description"] = routine.Description

	properties, err := structpb.NewStruct(values)
	if err != nil {
		return nil, fmt.Errorf("creating routine service %q configuration: %w", routine.Name, err)
	}
	return properties, nil
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
