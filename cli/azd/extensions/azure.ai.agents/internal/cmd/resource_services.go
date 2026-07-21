// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/protobuf/types/known/structpb"
)

// Foundry resource service hosts. Each Foundry resource is written to azure.yaml
// as its own service entry keyed by the resource name, carrying a singular
// host: azure.ai.<kind>. The owning extension registers a service-target
// provider for the host so `azd up`/`provision`/`deploy` can walk the service.
const (
	// AiProjectHost owns the Foundry project and its model deployments.
	AiProjectHost = "azure.ai.project"
	// AiConnectionHost owns a single Foundry project connection.
	AiConnectionHost = "azure.ai.connection"
	// AiToolboxHost owns a single Foundry toolbox (toolset).
	AiToolboxHost = "azure.ai.toolbox"

	// aiProjectServiceName is the stable azure.yaml service key used for the
	// single azure.ai.project service. A stable name keeps repeated inits
	// idempotent (AddService overwrites by name) so there is one project
	// service per project, matching the unified Foundry config design.
	aiProjectServiceName = "ai-project"
)

// emitResourceServices writes the Foundry resource sibling services that the
// agent depends on (one azure.ai.project carrying the model deployments, one
// azure.ai.connection per connection, one azure.ai.toolbox per toolbox) and
// wires the agent service's uses: list to them for ordering. Each resource is
// its own azure.yaml service entry so a different extension can own each host.
//
// projectEndpoint, when non-empty, is written as endpoint: on the project
// service to mark an existing (brownfield) Foundry project so provision
// connects to it instead of creating a new one. It is empty for new projects.
//
// projectName, when known, is the Foundry project name used to derive the
// project service key (so azure.yaml reads like the real project). It falls back
// to aiProjectServiceName when unknown or colliding. See resolveProjectServiceKey.
func emitResourceServices(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentServiceName string,
	projectName string,
	projectEndpoint string,
	deployments []project.Deployment,
	connections []project.Connection,
	toolboxes []project.Toolbox,
) error {
	var agentUses []string

	// Track every azure.yaml service key we emit so two resource names that
	// sanitize to the same key (e.g. "my conn" and "myconn") fail fast instead
	// of silently overwriting each other -- AddService overwrites by name.
	// Seed it with the agent service name, which the caller adds before this
	// runs, plus the project's existing non-project services, so a resource
	// colliding with the agent or a hand-authored service is caught too. The
	// existing azure.ai.project service is intentionally left out: it is reused
	// by resolveProjectServiceKey to keep repeated inits idempotent.
	usedNames := map[string]string{}
	if agentServiceName != "" {
		usedNames[agentServiceName] = "agent service"
	}
	if resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{}); err == nil && resp.GetProject() != nil {
		for name, svc := range resp.GetProject().GetServices() {
			if name == agentServiceName || svc.GetHost() == AiProjectHost {
				continue
			}
			usedNames[name] = fmt.Sprintf("existing service %q", name)
		}
	}

	// One project service owns the model deployments and represents the single
	// Foundry project the agent targets. It is always emitted -- even with no
	// deployments (e.g. "Skip model configuration") -- so every agent has one
	// project sibling that connections and toolboxes can depend on to enforce
	// provisioning order. A non-empty endpoint marks an existing project.
	projectCfg, err := project.MarshalStruct(&project.ServiceTargetAgentConfig{
		Endpoint:    projectEndpoint,
		Deployments: deployments,
	})
	if err != nil {
		return fmt.Errorf("marshaling project service config: %w", err)
	}
	projectServiceName := resolveProjectServiceKey(ctx, azdClient, projectName, agentServiceName)
	if err := reserveServiceName(usedNames, projectServiceName, "project service"); err != nil {
		return err
	}
	if err := addResourceService(ctx, azdClient, projectServiceName, AiProjectHost, projectCfg, nil); err != nil {
		return err
	}
	agentUses = append(agentUses, projectServiceName)

	// Connection and toolbox services depend on the project service so the
	// project is provisioned first.
	siblingUses := []string{projectServiceName}

	for i := range connections {
		conn := connections[i]
		connName := sanitizeServiceName(conn.Name)
		if connName == "" {
			fmt.Fprintf(os.Stderr,
				"warning: connection %q has no characters usable as an azure.yaml service key; "+
					"skipping it. Rename the connection so it is written to azure.yaml.\n",
				conn.Name)
			continue
		}
		if err := reserveServiceName(usedNames, connName, fmt.Sprintf("connection %q", conn.Name)); err != nil {
			return err
		}
		connCfg, err := project.MarshalStruct(&conn)
		if err != nil {
			return fmt.Errorf("marshaling connection service %q config: %w", connName, err)
		}
		if err := addResourceService(ctx, azdClient, connName, AiConnectionHost, connCfg, siblingUses); err != nil {
			return err
		}
		agentUses = append(agentUses, connName)
	}

	for i := range toolboxes {
		toolbox := toolboxes[i]
		toolboxName := sanitizeServiceName(toolbox.Name)
		if toolboxName == "" {
			fmt.Fprintf(os.Stderr,
				"warning: toolbox %q has no characters usable as an azure.yaml service key; "+
					"skipping it. Rename the toolbox so it is written to azure.yaml.\n",
				toolbox.Name)
			continue
		}
		if err := reserveServiceName(usedNames, toolboxName, fmt.Sprintf("toolbox %q", toolbox.Name)); err != nil {
			return err
		}
		toolboxCfg, err := project.MarshalStruct(&toolbox)
		if err != nil {
			return fmt.Errorf("marshaling toolbox service %q config: %w", toolboxName, err)
		}
		if err := addResourceService(ctx, azdClient, toolboxName, AiToolboxHost, toolboxCfg, siblingUses); err != nil {
			return err
		}
		agentUses = append(agentUses, toolboxName)
	}

	// Wire the agent service to its resource siblings so azd walks them first.
	if len(agentUses) > 0 && agentServiceName != "" {
		if err := setServiceUses(ctx, azdClient, agentServiceName, agentUses); err != nil {
			return err
		}
	}

	return nil
}

// resolveProjectServiceKey picks the azure.yaml service key for the single
// azure.ai.project service. Precedence:
//
//  1. Reuse an existing azure.ai.project service key when one is already in the
//     project. This keeps repeated inits idempotent (azd's extension API has no
//     remove-service call, so a changed key would leave a second project service
//     behind, which the provisioning provider rejects).
//  2. Otherwise derive the key from the Foundry project name when it is known and
//     does not collide with the agent service name, so azure.yaml reads like the
//     real project.
//  3. Otherwise fall back to the stable "ai-project" default.
//
// The key is not load-bearing: the provider and collectors find the project
// service by host (azure.ai.project), and the generated uses: edges reference
// whatever key this returns.
func resolveProjectServiceKey(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	projectName string,
	agentServiceName string,
) string {
	if existing := existingProjectServiceKey(ctx, azdClient); existing != "" {
		return existing
	}
	if key := sanitizeServiceName(projectName); key != "" && key != agentServiceName {
		return key
	}
	return aiProjectServiceName
}

// existingProjectServiceKey returns the key of the azure.ai.project service
// already present in the project, or "" when none exists or the project cannot be
// read. When more than one is present (should not happen) the lexicographically
// first key is returned so the choice is deterministic.
func existingProjectServiceKey(ctx context.Context, azdClient *azdext.AzdClient) string {
	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		return ""
	}
	var keys []string
	for name, svc := range resp.GetProject().GetServices() {
		if svc.GetHost() == AiProjectHost {
			keys = append(keys, name)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	slices.Sort(keys)
	return keys[0]
}

// projectNameHint returns the Foundry project name to derive the project service
// key from: the selected existing project's name, else the AZURE_AI_PROJECT_NAME
// azd environment value when concretely set (not a ${...} placeholder), else "".
func projectNameHint(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	selected *FoundryProjectInfo,
) string {
	if selected != nil && selected.ProjectName != "" {
		return selected.ProjectName
	}
	v, err := getEnvValue(ctx, azdClient, envName, "AZURE_AI_PROJECT_NAME")
	if err != nil || strings.HasPrefix(strings.TrimSpace(v), "${") {
		return ""
	}
	return v
}

// stampProjectEndpoint writes the selected project's endpoint onto the existing
// azure.ai.project service in azure.yaml. This is a no-op when the project is
// nil, has no endpoint, or when no ai-project service exists yet.
func stampProjectEndpoint(ctx context.Context, azdClient *azdext.AzdClient, selectedProject *FoundryProjectInfo) error {
	if selectedProject == nil {
		return nil
	}
	endpoint := selectedProject.Endpoint()
	if endpoint == "" {
		return nil
	}
	projectSvcKey := existingProjectServiceKey(ctx, azdClient)
	if projectSvcKey == "" {
		return nil
	}
	endpointVal, err := structpb.NewValue(endpoint)
	if err != nil {
		return fmt.Errorf("encoding project endpoint: %w", err)
	}
	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: projectSvcKey,
		Path:        "endpoint",
		Value:       endpointVal,
	}); err != nil {
		return fmt.Errorf("writing project endpoint to azure.yaml: %w", err)
	}
	return nil
}

// addResourceService adds a single Foundry resource service to azure.yaml with
// its keys composed at the service level (inline, via AdditionalProperties, the
// same shape the agent service uses) and optionally wires its uses: list. The
// service is added with an empty language so azd resolves a no-op framework; the
// owning extension's service-target provider handles its lifecycle.
func addResourceService(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	name string,
	host string,
	cfg *structpb.Struct,
	uses []string,
) error {
	environment := serviceEnvironmentTemplates(cfg)
	svc := &azdext.ServiceConfig{
		Name:                 name,
		Host:                 host,
		AdditionalProperties: cfg,
	}

	if _, err := azdClient.Project().AddService(ctx, &azdext.AddServiceRequest{Service: svc}); err != nil {
		return fmt.Errorf("adding %s service %q: %w", host, name, err)
	}

	if err := setServiceEnvironment(
		ctx,
		azdClient,
		name,
		environment,
	); err != nil {
		return err
	}

	if len(uses) > 0 {
		if err := setServiceUses(ctx, azdClient, name, uses); err != nil {
			return err
		}
	}

	return nil
}

func serviceEnvironmentTemplates(cfg *structpb.Struct) map[string]string {
	if cfg == nil {
		return nil
	}

	environment := map[string]string{}
	collectEnvironmentTemplates(cfg.AsMap(), environment)
	if len(environment) == 0 {
		return nil
	}
	return environment
}

func collectEnvironmentTemplates(value any, environment map[string]string) {
	switch typed := value.(type) {
	case string:
		collectStringEnvironmentTemplates(typed, environment)
	case map[string]any:
		for _, nested := range typed {
			collectEnvironmentTemplates(nested, environment)
		}
	case []any:
		for _, nested := range typed {
			collectEnvironmentTemplates(nested, environment)
		}
	}
}

func collectStringEnvironmentTemplates(value string, environment map[string]string) {
	for offset := 0; offset < len(value); {
		startOffset := strings.Index(value[offset:], "${")
		if startOffset < 0 {
			return
		}
		start := offset + startOffset
		if strings.HasPrefix(value[start:], "${{") {
			end := strings.Index(value[start+3:], "}}")
			if end < 0 {
				return
			}
			offset = start + end + 5
			continue
		}

		name, end, found := environmentTemplateAt(value, start)
		if !found {
			offset = start + 2
			continue
		}
		environment[name] = value[start:end]
		offset = end
	}
}

func environmentTemplateAt(value string, start int) (string, int, bool) {
	if start > 0 && value[start-1] == '$' {
		return "", 0, false
	}

	index := start + 2
	if index >= len(value) || !isEnvironmentNameStart(value[index]) {
		return "", 0, false
	}
	nameStart := index
	index++
	for index < len(value) && isEnvironmentNameCharacter(value[index]) {
		index++
	}
	name := value[nameStart:index]

	if index < len(value) && value[index] == '}' {
		return name, index + 1, true
	}
	if !strings.HasPrefix(value[index:], ":-") {
		return "", 0, false
	}

	end, found := environmentTemplateEnd(value, index+2)
	if !found {
		return "", 0, false
	}
	return name, end, true
}

// environmentTemplateEnd skips nested Foundry expressions.
func environmentTemplateEnd(value string, index int) (int, bool) {
	depth := 1
	for index < len(value) {
		if strings.HasPrefix(value[index:], "${{") {
			end := strings.Index(value[index+3:], "}}")
			if end < 0 {
				return 0, false
			}
			index += end + 5
			continue
		}
		if strings.HasPrefix(value[index:], "${") {
			depth++
			index += 2
			continue
		}
		if value[index] == '}' {
			depth--
			index++
			if depth == 0 {
				return index, true
			}
			continue
		}
		index++
	}
	return 0, false
}

func isEnvironmentNameStart(value byte) bool {
	return value == '_' || value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z'
}

func isEnvironmentNameCharacter(value byte) bool {
	return isEnvironmentNameStart(value) || value >= '0' && value <= '9'
}

func setServiceEnvironment(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceName string,
	environment map[string]string,
) error {
	if len(environment) == 0 {
		return nil
	}

	sectionValues := make(map[string]any, len(environment))
	for key, value := range environment {
		sectionValues[key] = value
	}
	section, err := structpb.NewStruct(sectionValues)
	if err != nil {
		return fmt.Errorf(
			"encoding env for service %q: %w",
			serviceName,
			err,
		)
	}

	// ServiceConfig.Environment only carries expanded values.
	// The config RPC preserves raw ${VAR} templates.
	_, err = azdClient.Project().SetServiceConfigSection(
		ctx,
		&azdext.SetServiceConfigSectionRequest{
			ServiceName: serviceName,
			Path:        "env",
			Section:     section,
		},
	)
	if err != nil {
		return fmt.Errorf(
			"setting env for service %q: %w",
			serviceName,
			err,
		)
	}
	return nil
}

// setServiceUses sets the uses: list on an existing service. uses is a real
// core ServiceConfig field, so it is written via SetServiceConfigValue (a raw
// map path) rather than AddService's inlined config map, which cannot carry it.
func setServiceUses(ctx context.Context, azdClient *azdext.AzdClient, serviceName string, uses []string) error {
	usesItems := make([]any, len(uses))
	for i, u := range uses {
		usesItems[i] = u
	}

	usesValue, err := structpb.NewValue(usesItems)
	if err != nil {
		return fmt.Errorf("encoding uses for service %q: %w", serviceName, err)
	}

	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "uses",
		Value:       usesValue,
	}); err != nil {
		return fmt.Errorf("setting uses for service %q: %w", serviceName, err)
	}

	return nil
}

// sanitizeServiceName converts a resource name into an azure.yaml service key by
// trimming surrounding whitespace and removing interior spaces, matching how the
// agent service name is derived from the agent name. Only spaces are stripped, so
// the name is expected to otherwise consist of characters valid in a YAML map key
// (letters, digits, '-', '_', '.'); Foundry resource names already meet this. A
// name that reduces to an empty string is skipped by the caller with a warning.
func sanitizeServiceName(name string) string {
	return strings.ReplaceAll(strings.TrimSpace(name), " ", "")
}

// reserveServiceName records an azure.yaml service key derived from a Foundry
// resource name, returning an error when two resources sanitize to the same
// key. AddService overwrites by name, so without this a collision would
// silently drop a resource and corrupt the uses: graph; failing fast lets the
// user rename the offending resource.
func reserveServiceName(used map[string]string, name, source string) error {
	if existing, ok := used[name]; ok {
		return fmt.Errorf(
			"resource service name collision: %s and %s both map to azure.yaml service %q; "+
				"rename one so they produce distinct service names",
			existing, source, name,
		)
	}
	used[name] = source
	return nil
}

// collectLegacyProjectDeployments reads only pre-split agent config.
// A split project disables this compatibility path because projects
// owns that service's runtime projection.
func collectLegacyProjectDeployments(
	services map[string]*azdext.ServiceConfig,
) ([]project.Deployment, error) {
	for _, svc := range services {
		if svc.GetHost() == AiProjectHost {
			return nil, nil
		}
	}

	legacy, err := collectLegacyAgentConfigs(services)
	if err != nil {
		return nil, err
	}
	var out []project.Deployment
	for _, cfg := range legacy {
		out = append(out, cfg.Deployments...)
	}
	return out, nil
}

// collectConnections gathers the connections declared across all
// azure.ai.connection services. Falls back to the connections bundled on the
// agent service when no connection service carries any, so a pre-split
// azure.yaml still provisions without re-running init.
func collectConnections(services map[string]*azdext.ServiceConfig) ([]project.Connection, error) {
	var out []project.Connection
	for _, svc := range sortedServices(services) {
		props := project.ServiceConfigProps(svc)
		if svc.Host != AiConnectionHost || props == nil {
			continue
		}
		var conn *project.Connection
		if err := project.UnmarshalStruct(props, &conn); err != nil {
			return nil, fmt.Errorf("parsing connection service %q config: %w", svc.Name, err)
		}
		if conn != nil {
			out = append(out, *conn)
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	legacy, err := collectLegacyAgentConfigs(services)
	if err != nil {
		return nil, err
	}
	for _, cfg := range legacy {
		out = append(out, cfg.Connections...)
	}
	return out, nil
}

// collectToolboxes gathers the toolboxes declared across all azure.ai.toolbox
// services. Falls back to the toolboxes bundled on the agent service when no
// toolbox service carries any, so a pre-split azure.yaml still provisions
// without re-running init.
func collectToolboxes(services map[string]*azdext.ServiceConfig) ([]project.Toolbox, error) {
	var out []project.Toolbox
	for _, svc := range sortedServices(services) {
		props := project.ServiceConfigProps(svc)
		if svc.Host != AiToolboxHost || props == nil {
			continue
		}
		var toolbox *project.Toolbox
		if err := project.UnmarshalStruct(props, &toolbox); err != nil {
			return nil, fmt.Errorf("parsing toolbox service %q config: %w", svc.Name, err)
		}
		if toolbox != nil {
			out = append(out, *toolbox)
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	legacy, err := collectLegacyAgentConfigs(services)
	if err != nil {
		return nil, err
	}
	for _, cfg := range legacy {
		out = append(out, cfg.Toolboxes...)
	}
	return out, nil
}

// collectAgentToolConnections gathers the tool connections declared on agent
// services. Tool connections stay on the agent service (they are agent tool
// configuration), so toolbox enrichment still needs them alongside the
// connections sourced from azure.ai.connection services.
func collectAgentToolConnections(services map[string]*azdext.ServiceConfig) ([]project.ToolConnection, error) {
	configs, err := collectLegacyAgentConfigs(services)
	if err != nil {
		return nil, err
	}
	var out []project.ToolConnection
	for _, cfg := range configs {
		out = append(out, cfg.ToolConnections...)
	}
	return out, nil
}

// collectLegacyAgentConfigs parses the bundled ServiceTargetAgentConfig from
// every agent service, in sorted name order. Tool connections always live here;
// projects created before the per-resource split also carry their deployments,
// connections, and toolboxes here rather than in sibling azure.ai.<kind>
// services, so the collectors fall back to these when no sibling service exists.
func collectLegacyAgentConfigs(services map[string]*azdext.ServiceConfig) ([]*project.ServiceTargetAgentConfig, error) {
	var out []*project.ServiceTargetAgentConfig
	for _, svc := range sortedServices(services) {
		if svc.Host != AiAgentHost {
			continue
		}
		if project.ServiceConfigProps(svc) == nil {
			continue
		}
		cfg, err := project.LoadServiceTargetAgentConfig(svc)
		if err != nil {
			return nil, fmt.Errorf("parsing agent service %q config: %w", svc.Name, err)
		}
		if cfg != nil {
			out = append(out, cfg)
		}
	}
	return out, nil
}

// sortedServices returns the services ordered by their map key so callers that
// serialize collected resources produce deterministic output across runs.
func sortedServices(services map[string]*azdext.ServiceConfig) []*azdext.ServiceConfig {
	keys := make([]string, 0, len(services))
	for k := range services {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	out := make([]*azdext.ServiceConfig, 0, len(services))
	for _, k := range keys {
		out = append(out, services[k])
	}
	return out
}
