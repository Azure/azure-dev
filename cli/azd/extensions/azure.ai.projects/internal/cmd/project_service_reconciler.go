// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/provisioning"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"google.golang.org/protobuf/types/known/structpb"
)

type projectMode string

const (
	projectModeNew              projectMode = "new"
	projectModeExistingID       projectMode = "existing-id"
	projectModeExistingEndpoint projectMode = "existing-endpoint"
)

type projectServiceInfo struct {
	Name       string
	Raw        map[string]any
	Resolved   map[string]any
	Expanded   *azdext.ServiceConfig
	ServiceRef string
	Legacy     bool
}

type projectServiceReconciler struct {
	client      *azdext.AzdClient
	projectRoot string
}

// discoverProjectService loads persisted and expanded views.
// Writes use persisted data; discovery uses expanded data.
func (r *projectServiceReconciler) discoverProjectService(
	ctx context.Context,
) (*projectServiceInfo, *azdext.ProjectConfig, error) {
	response, err := r.client.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, nil, err
	}
	if response.Project == nil {
		return nil, nil, fmt.Errorf("azd project is empty")
	}
	project := response.Project

	rawServices := map[string]any{}
	section, sectionErr := r.client.Project().GetConfigSection(ctx,
		&azdext.GetProjectConfigSectionRequest{Path: "services"})
	if sectionErr != nil {
		return nil, project, fmt.Errorf("read persisted project services: %w", sectionErr)
	}
	if section.GetFound() && section.GetSection() != nil {
		rawServices = section.GetSection().AsMap()
	}

	projectHosts := provisioning.FoundryProjectServiceHosts
	legacyHosts := provisioning.FoundryLegacyProvisioningHosts
	var projectNames, legacyNames []string
	for name, service := range project.GetServices() {
		if service == nil {
			continue
		}
		if slices.Contains(projectHosts, service.GetHost()) {
			projectNames = append(projectNames, name)
		} else if slices.Contains(legacyHosts, service.GetHost()) &&
			(hasProjectOwnedFields(rawServices[name]) || hasServiceRef(rawServices[name])) {
			legacyNames = append(legacyNames, name)
		}
	}
	slices.Sort(projectNames)
	slices.Sort(legacyNames)

	if len(projectNames) > 1 {
		return nil, project, ambiguousProjectServiceError(projectNames)
	}
	legacy := false
	names := projectNames
	if len(names) == 0 {
		names = legacyNames
		legacy = len(names) > 0
	}
	if len(names) > 1 {
		return nil, project, ambiguousProjectServiceError(names)
	}
	if len(names) == 0 {
		return nil, project, nil
	}

	name := names[0]
	raw, _ := rawServices[name].(map[string]any)
	raw, err = cloneMap(raw)
	if err != nil {
		return nil, project, fmt.Errorf(
			"copy persisted project service %q: %w", name, err,
		)
	}
	resolved, err := cloneMap(raw)
	if err != nil {
		return nil, project, fmt.Errorf(
			"copy resolved project service %q: %w", name, err,
		)
	}
	if resolved == nil {
		resolved = map[string]any{}
	}
	if len(resolved) > 0 && r.projectRoot != "" {
		resolved, err = foundry.ResolveFileRefs(resolved, r.projectRoot)
		if err != nil {
			return nil, project, fmt.Errorf("resolve project service %q $ref includes: %w", name, err)
		}
	}
	var serviceRef string
	if ref, ok := raw["$ref"].(string); ok {
		serviceRef = ref
	}
	return &projectServiceInfo{
		Name:       name,
		Raw:        raw,
		Resolved:   resolved,
		Expanded:   project.GetServices()[name],
		ServiceRef: serviceRef,
		Legacy:     legacy,
	}, project, nil
}

func (r *projectServiceReconciler) reconcileEndpoint(
	ctx context.Context,
	projectName, endpoint string,
	mode projectMode,
) (string, string, error) {
	service, project, err := r.discoverProjectService(ctx)
	if err != nil {
		return "", "", err
	}
	if service == nil {
		name := projectServiceName(projectName, project.GetServices())
		body := map[string]any{}
		if endpoint != "" {
			body["endpoint"] = endpoint
		}
		if err := r.addService(ctx, name, body); err != nil {
			return "", "", err
		}
		return name, "created", nil
	}

	// Copy legacy services instead of editing them in place.
	// A service-level ref cannot use the shallow overlay RPC.
	if service.Legacy {
		if service.ServiceRef != "" {
			return "", "", projectServiceRefError(service.Name, service.ServiceRef)
		}
		body, err := legacyProjectServiceBody(service.Raw, endpoint)
		if err != nil {
			return "", "", fmt.Errorf(
				"copy legacy project service %q: %w", service.Name, err,
			)
		}
		name := projectServiceName(projectName, project.GetServices())
		if err := r.addService(ctx, name, body); err != nil {
			return "", "", err
		}
		return name, "migrated", nil
	}

	currentEndpoint := serviceEndpoint(service.Resolved)
	if endpoint != "" {
		normalized, _, err := validateProjectEndpoint(endpoint)
		if err != nil {
			return "", "", err
		}
		endpoint = normalized
	}
	if equalProjectEndpoint(currentEndpoint, endpoint) {
		return service.Name, "unchanged", nil
	}
	if service.ServiceRef != "" {
		return "", "", projectServiceRefError(service.Name, service.ServiceRef)
	}

	if endpoint == "" {
		if _, ok := service.Raw["endpoint"]; !ok {
			return service.Name, "unchanged", nil
		}
	}
	if err := setProjectServiceEndpoint(ctx, r.client, service.Name, endpoint); err != nil {
		return "", "", err
	}
	_ = mode
	return service.Name, "updated", nil
}

func setProjectServiceEndpoint(
	ctx context.Context,
	client *azdext.AzdClient,
	serviceName, endpoint string,
) error {
	value, err := structpb.NewValue(endpoint)
	if err != nil {
		return err
	}
	if _, err := client.Project().SetServiceConfigValue(ctx,
		&azdext.SetServiceConfigValueRequest{
			ServiceName: serviceName,
			Path:        "endpoint",
			Value:       value,
		}); err != nil {
		return fmt.Errorf("update project service %q endpoint: %w", serviceName, err)
	}
	return nil
}

func legacyProjectServiceBody(
	raw map[string]any,
	endpoint string,
) (map[string]any, error) {
	body, err := cloneMap(raw)
	if err != nil {
		return nil, err
	}
	if body == nil {
		body = map[string]any{}
	}
	delete(body, "host")
	if endpoint != "" {
		body["endpoint"] = endpoint
	} else {
		delete(body, "endpoint")
	}
	return body, nil
}

func (r *projectServiceReconciler) addService(
	ctx context.Context,
	name string,
	body map[string]any,
) error {
	var err error
	body, err = cloneMap(body)
	if err != nil {
		return fmt.Errorf("copy project service %q: %w", name, err)
	}
	if body == nil {
		body = map[string]any{}
	}
	body["host"] = provisioning.FoundryProjectHost
	properties, err := structpb.NewStruct(body)
	if err != nil {
		return fmt.Errorf("encode project service %q: %w", name, err)
	}
	_, err = r.client.Project().AddService(ctx, &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{
			Name: name,
			Host: provisioning.FoundryProjectHost,
		},
	})
	if err != nil {
		return fmt.Errorf("add project service %q: %w", name, err)
	}
	if len(body) == 1 {
		return nil
	}
	if _, err := r.client.Project().SetServiceConfigSection(
		ctx,
		&azdext.SetServiceConfigSectionRequest{
			ServiceName: name,
			Section:     properties,
		},
	); err != nil {
		return fmt.Errorf("persist project service %q configuration: %w", name, err)
	}
	return nil
}

func hasProjectOwnedFields(value any) bool {
	body, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for _, key := range []string{"endpoint", "deployments", "network"} {
		if _, found := body[key]; found {
			return true
		}
	}
	return false
}

func hasManagedProjectFields(value map[string]any) bool {
	if value == nil {
		return false
	}
	for _, key := range []string{"deployments", "network"} {
		if _, found := value[key]; found {
			return true
		}
	}
	return false
}

func hasServiceRef(value any) bool {
	body, ok := value.(map[string]any)
	if !ok {
		return false
	}
	_, exists := body["$ref"]
	return exists
}

func ambiguousProjectServiceError(names []string) error {
	slices.Sort(names)
	return exterrors.Validation(
		"project_service_ambiguous",
		fmt.Sprintf("multiple Foundry project services found: %s", strings.Join(names, ", ")),
		"keep exactly one service with host azure.ai.project and retry",
	)
}

func projectServiceRefError(name, ref string) error {
	return exterrors.Validation(
		"project_service_ref_update_unsupported",
		fmt.Sprintf("project service %q is referenced from %q and cannot be updated safely", name, ref),
		fmt.Sprintf("edit %q directly or inline the service before retrying", ref),
	)
}

func validateProjectServiceMutation(
	service *projectServiceInfo,
	endpoint string,
	infra string,
) error {
	if service == nil || service.ServiceRef == "" {
		return nil
	}
	if service.Legacy || infra != "" {
		return projectServiceRefError(service.Name, service.ServiceRef)
	}
	if endpoint != "" {
		normalized, _, err := validateProjectEndpoint(endpoint)
		if err != nil {
			return err
		}
		endpoint = normalized
	}
	if equalProjectEndpoint(serviceEndpoint(service.Resolved), endpoint) {
		return nil
	}
	if endpoint == "" {
		if _, exists := service.Raw["endpoint"]; !exists {
			return nil
		}
	}
	return projectServiceRefError(service.Name, service.ServiceRef)
}

var serviceNameInvalid = regexp.MustCompile(`[^a-z0-9-]+`)

func projectServiceName(projectName string, services map[string]*azdext.ServiceConfig) string {
	used := make(map[string]struct{}, len(services))
	for name := range services {
		used[strings.ToLower(name)] = struct{}{}
	}
	base := serviceNameInvalid.ReplaceAllString(strings.ToLower(strings.TrimSpace(projectName)), "-")
	base = strings.Trim(base, "-")
	if len(base) > 63 {
		base = strings.TrimRight(base[:63], "-")
	}
	if base != "" {
		if _, exists := used[base]; !exists {
			return base
		}
	}
	if _, exists := used["ai-project"]; !exists {
		return "ai-project"
	}
	for i := 2; ; i++ {
		name := fmt.Sprintf("ai-project-%d", i)
		if _, exists := used[name]; !exists {
			return name
		}
	}
}

func serviceEndpoint(service map[string]any) string {
	if service == nil {
		return ""
	}
	endpoint, _ := service["endpoint"].(string)
	return endpoint
}

func equalProjectEndpoint(left, right string) bool {
	if left == "" || right == "" {
		return left == right
	}
	leftNormalized, _, leftErr := validateProjectEndpoint(left)
	rightNormalized, _, rightErr := validateProjectEndpoint(right)
	if leftErr != nil || rightErr != nil {
		return strings.TrimRight(strings.TrimSpace(left), "/") ==
			strings.TrimRight(strings.TrimSpace(right), "/")
	}
	return strings.EqualFold(leftNormalized, rightNormalized)
}

func cloneMap(value map[string]any) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("serialize map: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("deserialize map: %w", err)
	}
	return result, nil
}
