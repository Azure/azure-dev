// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"google.golang.org/protobuf/proto"
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
	// AiSkillHost owns a single Foundry skill and its versions. azd never
	// uploads a skill bundle itself; it emits one service per skills/<name>/
	// folder and attaches the version that extension publishes.
	AiSkillHost = "azure.ai.skill"

	// aiProjectServiceName is the stable azure.yaml service key used for the
	// single azure.ai.project service. A stable name keeps repeated inits
	// idempotent (AddService overwrites by name) so there is one project
	// service per project, matching the unified Foundry config design. It is
	// deliberately generic rather than derived from the Foundry project name so
	// azure.yaml carries no tenant-specific identifiers and can be copied
	// between projects unchanged.
	aiProjectServiceName = "ai-project"

	// projectEndpointEnvVar carries the concrete Foundry project endpoint in the
	// azd environment. azure.yaml references it instead of embedding the URL so
	// the project stays portable: set it to reuse an existing project, leave it
	// unset to have `azd provision` create a new one.
	projectEndpointEnvVar = "AZURE_AI_PROJECT_ENDPOINT"

	// projectEndpointRef is the portable reference written as endpoint: on the
	// azure.ai.project service. Synthesize expands it before deciding
	// brownfield vs greenfield, so an unset variable resolves to "" (greenfield).
	projectEndpointRef = "${" + projectEndpointEnvVar + "}"

	// projectWorkspaceEnvVar carries the AML workspace name backing the Foundry
	// project (<account>@<project>@AML). The managed control plane's agent
	// routes are workspace-scoped, so the deploy path reads it from the azd
	// environment rather than from azure.yaml.
	projectWorkspaceEnvVar = "AZURE_AI_WORKSPACE"
)

// promptResourceServices derives the sibling Foundry services a prompt or
// managed agent needs from its scaffolded definition and folder layout, so a
// prompt agent's azure.yaml carries the same hosts as a hosted agent's.
//
//   - Each entry under connections: becomes an azure.ai.connection service.
//   - Each skills/<dir>/ folder becomes an azure.ai.skill service keyed by the
//     name its SKILL.md declares. The agents extension never uploads a bundle
//     itself; at deploy time it attaches the version the skill service
//     published.
//   - toolbox: names an existing toolbox rather than defining one, so there is
//     nothing to write as a service. Its name is added to the agent's uses: when
//     a toolbox service of that name is already in azure.yaml, which is what
//     orders the toolbox ahead of the agent; a uses: entry naming a service that
//     does not exist would fail the project load instead.
//
// Deployments are left to the caller, which owns the model selection flow.
func promptResourceServices(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	promptAgent *agent_yaml.PromptAgent,
	serviceRelPath string,
) (foundryResources, error) {
	resources := foundryResources{}

	for _, conn := range promptAgent.Connections {
		resources.Connections = append(resources.Connections, project.Connection{
			Name:        conn.Name,
			Category:    conn.Category,
			Target:      conn.Target,
			AuthType:    conn.AuthType,
			Credentials: conn.Credentials,
			Metadata:    conn.Metadata,
		})
	}

	bundles, err := project.ScanSkillBundles(serviceRelPath)
	if err != nil {
		return foundryResources{}, err
	}
	for _, bundle := range bundles {
		if resources.Skills == nil {
			resources.Skills = map[string]project.SkillService{}
		}
		resources.Skills[bundle.Name] = project.SkillService{
			Description: bundle.Description,
			// Relative to azure.yaml, which lives in the directory init runs in.
			Archive: "./" + path.Join(filepath.ToSlash(serviceRelPath), bundle.RelPath),
		}
	}

	if promptAgent.Toolbox != nil {
		name := sanitizeServiceName(promptAgent.Toolbox.Name)
		if name != "" && serviceHasHost(ctx, azdClient, name, AiToolboxHost) {
			resources.ExtraUses = append(resources.ExtraUses, name)
		}
	}

	return resources, nil
}

// serviceHasHost reports whether azure.yaml already defines a service named
// name with the given host. Errors are treated as "no", because the callers use
// it to decide whether adding a uses: edge is safe and the conservative answer
// is to leave the edge out.
func serviceHasHost(ctx context.Context, azdClient *azdext.AzdClient, name, host string) bool {
	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		return false
	}
	svc, ok := resp.GetProject().GetServices()[name]
	return ok && svc.GetHost() == host
}

// foundryResources are the Foundry resources an agent depends on, each written
// to azure.yaml as its own sibling service entry keyed by the resource name.
// Grouping them keeps emitResourceServices readable as the set of hosts grows;
// a zero value emits only the always-present azure.ai.project service.
type foundryResources struct {
	// Deployments are the model deployments carried by the project service.
	Deployments []project.Deployment
	// Connections become one azure.ai.connection service each.
	Connections []project.Connection
	// Toolboxes become one azure.ai.toolbox service each.
	Toolboxes []project.Toolbox
	// Skills become one azure.ai.skill service each, keyed by skill name.
	Skills map[string]project.SkillService
	// ExtraUses are service keys added to the agent's uses: list without
	// emitting a service for them. A prompt agent's `toolbox:` names an
	// *existing* toolbox, so there is no definition to write, but the edge is
	// still needed for ordering and for the deploy-time dependency check.
	ExtraUses []string
}

// emitResourceServices writes the Foundry resource sibling services that the
// agent depends on (one azure.ai.project carrying the model deployments, one
// azure.ai.connection per connection, one azure.ai.toolbox per toolbox, one
// azure.ai.skill per skill bundle) and wires the agent service's uses: list to
// them for ordering. Each resource is its own azure.yaml service entry so a
// different extension can own each host.
//
// projectEndpoint, when non-empty, is written as endpoint: on the project
// service to mark an existing (brownfield) Foundry project so provision
// connects to it instead of creating a new one. It is empty for new projects.
// Callers pass projectEndpointRef (not a literal URL) so azure.yaml stays
// portable; see recordFoundryProjectEnv.
func emitResourceServices(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentServiceName string,
	projectEndpoint string,
	resources foundryResources,
) (int, error) {
	var agentUses []string
	emittedConnections := 0

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
		Deployments: resources.Deployments,
	})
	if err != nil {
		return 0, fmt.Errorf("marshaling project service config: %w", err)
	}
	projectServiceName := resolveProjectServiceKey(ctx, azdClient)
	if err := reserveServiceName(usedNames, projectServiceName, "project service"); err != nil {
		return 0, err
	}
	if err := addResourceService(ctx, azdClient, projectServiceName, AiProjectHost, projectCfg, nil); err != nil {
		return 0, err
	}
	agentUses = append(agentUses, projectServiceName)

	// Connection, toolbox and skill services depend on the project service so
	// the project is provisioned first.
	siblingUses := []string{projectServiceName}

	for i := range resources.Connections {
		conn := resources.Connections[i]
		connName := sanitizeServiceName(conn.Name)
		if connName == "" {
			fmt.Fprintf(os.Stderr,
				"warning: connection %q has no characters usable as an azure.yaml service key; "+
					"skipping it. Rename the connection so it is written to azure.yaml.\n",
				conn.Name)
			continue
		}
		if err := reserveServiceName(usedNames, connName, fmt.Sprintf("connection %q", conn.Name)); err != nil {
			return 0, err
		}
		connCfg, err := project.MarshalStruct(&conn)
		if err != nil {
			return 0, fmt.Errorf("marshaling connection service %q config: %w", connName, err)
		}
		if err := addResourceService(ctx, azdClient, connName, AiConnectionHost, connCfg, siblingUses); err != nil {
			return 0, err
		}
		agentUses = append(agentUses, connName)
		emittedConnections++
	}

	for i := range resources.Toolboxes {
		toolbox := resources.Toolboxes[i]
		toolboxName := sanitizeServiceName(toolbox.Name)
		if toolboxName == "" {
			fmt.Fprintf(os.Stderr,
				"warning: toolbox %q has no characters usable as an azure.yaml service key; "+
					"skipping it. Rename the toolbox so it is written to azure.yaml.\n",
				toolbox.Name)
			continue
		}
		if err := reserveServiceName(usedNames, toolboxName, fmt.Sprintf("toolbox %q", toolbox.Name)); err != nil {
			return 0, err
		}
		toolboxCfg, err := project.MarshalStruct(&toolbox)
		if err != nil {
			return 0, fmt.Errorf("marshaling toolbox service %q config: %w", toolboxName, err)
		}
		if err := addResourceService(ctx, azdClient, toolboxName, AiToolboxHost, toolboxCfg, siblingUses); err != nil {
			return 0, err
		}
		agentUses = append(agentUses, toolboxName)
	}

	// The service key is the skill name the azure.ai.skills extension creates,
	// and the name the agent's SKILL.md declares, so iterate in sorted order to
	// keep repeated inits byte-identical.
	for _, skill := range slices.Sorted(maps.Keys(resources.Skills)) {
		skillName := sanitizeServiceName(skill)
		if skillName == "" {
			fmt.Fprintf(os.Stderr,
				"warning: skill %q has no characters usable as an azure.yaml service key; "+
					"skipping it. Rename the skill so it is written to azure.yaml.\n",
				skill)
			continue
		}
		if err := reserveServiceName(usedNames, skillName, fmt.Sprintf("skill %q", skill)); err != nil {
			return 0, err
		}
		definition := resources.Skills[skill]
		skillCfg, err := project.MarshalStruct(&definition)
		if err != nil {
			return 0, fmt.Errorf("marshaling skill service %q config: %w", skillName, err)
		}
		if err := addResourceService(ctx, azdClient, skillName, AiSkillHost, skillCfg, siblingUses); err != nil {
			return 0, err
		}
		agentUses = append(agentUses, skillName)
	}

	for _, name := range resources.ExtraUses {
		if name != "" && !slices.Contains(agentUses, name) {
			agentUses = append(agentUses, name)
		}
	}

	// Wire the agent service to its resource siblings so azd walks them first.
	if len(agentUses) > 0 && agentServiceName != "" {
		if err := setServiceUses(ctx, azdClient, agentServiceName, agentUses); err != nil {
			return 0, err
		}
	}

	return emittedConnections, nil
}

// resolveProjectServiceKey picks the azure.yaml service key for the single
// azure.ai.project service. Precedence:
//
//  1. Reuse an existing azure.ai.project service key when one is already in the
//     project. This keeps repeated inits idempotent (azd's extension API has no
//     remove-service call, so a changed key would leave a second project service
//     behind, which the provisioning provider rejects).
//  2. Otherwise use the generic "ai-project" key.
//
// The key is deliberately not derived from the Foundry project name: a
// tenant-specific key makes azure.yaml non-portable, and the key is not
// load-bearing anyway -- the provider and collectors find the project service by
// host (azure.ai.project), and the generated uses: edges reference whatever key
// this returns.
func resolveProjectServiceKey(
	ctx context.Context,
	azdClient *azdext.AzdClient,
) string {
	if existing := existingProjectServiceKey(ctx, azdClient); existing != "" {
		return existing
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

// recordFoundryProjectEnv stores the concrete Foundry project coordinates that
// azure.yaml only references by name -- the data-plane endpoint and the backing
// AML workspace -- in the azd environment, and returns the portable ${VAR}
// reference to write as endpoint: on the project service.
//
// A nil or incomplete project (the "create a new project" path) writes nothing
// and returns "", leaving the project service greenfield.
func recordFoundryProjectEnv(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	foundryProject *FoundryProjectInfo,
) (string, error) {
	endpoint := strings.TrimSpace(foundryProject.Endpoint())
	if endpoint == "" {
		return "", nil
	}
	if err := setEnvValue(ctx, azdClient, envName, projectEndpointEnvVar, endpoint); err != nil {
		return "", fmt.Errorf("recording %s: %w", projectEndpointEnvVar, err)
	}
	// Managed agent CRUD routes are workspace-scoped; for Foundry projects the
	// backing AML workspace name is <account>@<project>@AML.
	workspace := fmt.Sprintf("%s@%s@AML", foundryProject.AccountName, foundryProject.ProjectName)
	if err := setEnvValue(ctx, azdClient, envName, projectWorkspaceEnvVar, workspace); err != nil {
		return "", fmt.Errorf("recording %s: %w", projectWorkspaceEnvVar, err)
	}
	return projectEndpointRef, nil
}

// stampProjectEndpoint writes endpointRef as endpoint: on the existing
// azure.ai.project service in azure.yaml. Callers pass the portable
// ${AZURE_AI_PROJECT_ENDPOINT} reference returned by recordFoundryProjectEnv, not
// a literal URL. This is a no-op when endpointRef is empty (a new project) or
// when no azure.ai.project service exists yet.
func stampProjectEndpoint(ctx context.Context, azdClient *azdext.AzdClient, endpointRef string) error {
	endpointRef = strings.TrimSpace(endpointRef)
	if endpointRef == "" {
		return nil
	}
	projectSvcKey := existingProjectServiceKey(ctx, azdClient)
	if projectSvcKey == "" {
		return nil
	}
	endpointVal, err := structpb.NewValue(endpointRef)
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

// serviceEnvironmentTemplates discovers client-side templates in the
// generic nested resource config emitted to azure.yaml.
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
	for _, reference := range findEnvironmentReferences(value) {
		// env is keyed by name, so store one canonical ${NAME}.
		// A ${NAME:-default} default is re-applied by the owning
		// extension against the raw config at deploy, so the env section
		// only needs NAME's resolved base value. Collapsing every form of
		// a var to one value also keeps collection deterministic when the
		// same var appears with and without a default. This assumes a
		// literal default: a nested ${VAR} default is unsupported and
		// gets no entry here. See findEnvironmentReferences.
		environment[reference.Name] = "${" + reference.Name + "}"
	}
}

// escapeFoundryTemplates escapes Foundry ${{...}} spans as $${{...}}
// so azd core's envsubst emits a literal ${{...}} for the owning
// extension to resolve. Already-escaped $${{...}} and bare ${VAR}
// are left unchanged, so it is safe on values read back from disk.
func escapeFoundryTemplates(value string) string {
	if !strings.Contains(value, "${{") {
		return value
	}
	var b strings.Builder
	b.Grow(len(value) + 2)
	for i := 0; i < len(value); i++ {
		if value[i] == '$' && strings.HasPrefix(value[i:], "${{") &&
			(i == 0 || value[i-1] != '$') {
			b.WriteByte('$')
		}
		b.WriteByte(value[i])
	}
	return b.String()
}

// setServiceEnvironment writes the env: block of a service, and
// leaves azure.yaml untouched when there is nothing to write.
//
// A generated service with no variables of its own therefore reads
// as legacy at run and deploy. Declaring an explicit env: {} here
// would not change that today: core drops a zero-length env on
// save because ServiceConfig.Environment is tagged omitempty.
// Fixing it needs core to distinguish an absent env: from an
// explicitly empty one.
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
		sectionValues[key] = escapeFoundryTemplates(value)
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
	projectRoot string,
) ([]project.Deployment, error) {
	for _, svc := range services {
		if svc.GetHost() == AiProjectHost {
			return nil, nil
		}
	}

	legacy, err := collectLegacyAgentConfigs(
		services,
		projectRoot,
	)
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
func collectConnections(
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
) ([]project.Connection, error) {
	var out []project.Connection
	for _, svc := range sortedServices(services) {
		if svc.Host != AiConnectionHost {
			continue
		}
		props, err := resolvedResourceServiceProps(
			svc,
			projectRoot,
		)
		if err != nil {
			return nil, err
		}
		if props == nil {
			continue
		}
		var conn *project.Connection
		if err := project.UnmarshalStruct(props, &conn); err != nil {
			return nil, fmt.Errorf("parsing connection service %q config: %w", svc.Name, err)
		}
		if conn != nil {
			if conn.Name == "" {
				conn.Name = svc.Name
			}
			out = append(out, *conn)
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	legacy, err := collectLegacyAgentConfigs(
		services,
		projectRoot,
	)
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
func collectToolboxes(
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
) ([]project.Toolbox, error) {
	var out []project.Toolbox
	for _, svc := range sortedServices(services) {
		if svc.Host != AiToolboxHost {
			continue
		}
		props, err := resolvedResourceServiceProps(
			svc,
			projectRoot,
		)
		if err != nil {
			return nil, err
		}
		if props == nil {
			continue
		}
		var toolbox *project.Toolbox
		if err := project.UnmarshalStruct(props, &toolbox); err != nil {
			return nil, fmt.Errorf("parsing toolbox service %q config: %w", svc.Name, err)
		}
		if toolbox != nil {
			if toolbox.Name == "" {
				toolbox.Name = svc.Name
			}
			out = append(out, *toolbox)
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	legacy, err := collectLegacyAgentConfigs(
		services,
		projectRoot,
	)
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
func collectAgentToolConnections(
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
) ([]project.ToolConnection, error) {
	configs, err := collectLegacyAgentConfigs(services, projectRoot)
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
func collectLegacyAgentConfigs(
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
) ([]*project.ServiceTargetAgentConfig, error) {
	var out []*project.ServiceTargetAgentConfig
	for _, svc := range sortedServices(services) {
		if svc.Host != AiAgentHost {
			continue
		}
		if project.ServiceConfigProps(svc) == nil {
			continue
		}
		effective := proto.Clone(svc).(*azdext.ServiceConfig)
		if projectRoot != "" {
			if err := project.ResolveServiceConfigInPlace(
				effective,
				projectRoot,
			); err != nil {
				return nil, fmt.Errorf(
					"resolving agent service %q config: %w",
					svc.Name,
					err,
				)
			}
		}
		cfg, err := project.LoadServiceTargetAgentConfig(effective)
		if err != nil {
			return nil, fmt.Errorf("parsing agent service %q config: %w", svc.Name, err)
		}
		if cfg != nil {
			out = append(out, cfg)
		}
	}
	return out, nil
}

func resolvedResourceServiceProps(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (*structpb.Struct, error) {
	props := project.ServiceConfigProps(svc)
	if props == nil || projectRoot == "" {
		return props, nil
	}
	resolved, err := foundry.ResolveFileRefs(
		props.AsMap(),
		projectRoot,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"resolving service %q config: %w",
			svc.Name,
			err,
		)
	}
	out, err := structpb.NewStruct(resolved)
	if err != nil {
		return nil, fmt.Errorf(
			"encoding service %q config: %w",
			svc.Name,
			err,
		)
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
