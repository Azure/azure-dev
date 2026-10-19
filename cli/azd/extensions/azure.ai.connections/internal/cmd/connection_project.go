// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"azure.ai.connections/internal/definition"
	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/foundry"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

const aiProjectServiceHost = "azure.ai.project"

type connectionAddProjectClient interface {
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
	UnsetServiceConfig(
		context.Context,
		*azdext.UnsetServiceConfigRequest,
		...grpc.CallOption,
	) (*azdext.EmptyResponse, error)
}

type connectionDefinitionLoader func(
	projectRoot string,
	filePath string,
) (map[string]any, string, error)

type connectionProjectSelector func(
	ctx context.Context,
	projectKeys []string,
) (string, error)

type connectionProjectSnapshot struct {
	manifest    map[string]any
	services    map[string]any
	resources   map[string]any
	projectKeys []string
}

type connectionAddPlan struct {
	serviceName          string
	mutation             string
	ref                  string
	projectKey           string
	environmentVariables []string
	environmentBindings  []string
	serviceSection       map[string]any
	addRequest           *azdext.AddServiceRequest
	changes              []connectionServiceChange

	// baseline holds the project services the plan was built from.
	// apply re-reads the manifest and checks the state it depends on.
	baseline    map[string]any
	planClient  connectionAddProjectClient
	projectRoot string
	// definition is re-resolved before writing to check for changes.
	definition *definition.Definition
}

type connectionServiceChange struct {
	path  string
	value *structpb.Value
}

var connectionCoreServiceFields = map[string]struct{}{
	"apiVersion":    {},
	"condition":     {},
	"config":        {},
	"dist":          {},
	"docker":        {},
	"env":           {},
	"hooks":         {},
	"host":          {},
	"image":         {},
	"infra":         {},
	"k8s":           {},
	"language":      {},
	"module":        {},
	"project":       {},
	"remoteBuild":   {},
	"resourceGroup": {},
	"resourceName":  {},
	"uses":          {},
}

var connectionDefinitionFields = map[string]struct{}{
	"$ref":             {},
	"$schema":          {},
	"audience":         {},
	"authorizationUrl": {},
	"authType":         {},
	"category":         {},
	"connectorName":    {},
	"credentials":      {},
	"metadata":         {},
	"name":             {},
	"refreshUrl":       {},
	"scopes":           {},
	"target":           {},
	"tokenUrl":         {},
}

func connectionAuthoringProjectDir() (string, error) {
	project, err := azdcontext.NewAzdContext()
	if err != nil {
		return "", err
	}
	return project.ProjectDirectory(), nil
}

func planConnectionAdd(
	ctx context.Context,
	client connectionAddProjectClient,
	projectRoot string,
	serviceName string,
	filePath string,
	projectExplicit bool,
	requestedProject string,
	noPrompt bool,
	selectProject connectionProjectSelector,
	loadDefinition connectionDefinitionLoader,
) (*connectionAddPlan, error) {
	if err := azdext.ValidateServiceName(serviceName); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			err.Error(),
			"Use a valid azd service name and retry.",
		)
	}
	if strings.TrimSpace(filePath) == "" {
		return nil, exterrors.Validation(
			exterrors.CodeMissingConnectionField,
			"--file must name a non-blank Connection definition path.",
			"Provide an existing YAML or JSON file with --file.",
		)
	}
	if projectExplicit && strings.TrimSpace(requestedProject) == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--project must name an existing azure.ai.project service key.",
			"Provide a non-blank service key with --project.",
		)
	}
	if client == nil {
		return nil, fmt.Errorf("Project API is unavailable")
	}
	if loadDefinition == nil {
		loadDefinition = foundry.DecodeContainedDocument
	}

	snapshot, err := loadConnectionProjectSnapshot(ctx, client, projectRoot)
	if err != nil {
		return nil, err
	}

	rawExisting, existing, err := readExistingConnectionService(snapshot, serviceName)
	if err != nil {
		return nil, err
	}
	var uses []string
	owners := map[string]struct{}{}
	if existing {
		uses, err = parseConnectionUses(rawExisting)
		if err != nil {
			return nil, err
		}
		owners, err = validateConnectionDependencies(uses, snapshot)
		if err != nil {
			return nil, err
		}
	}

	document, displayPath, err := loadDefinition(projectRoot, filePath)
	if err != nil {
		return nil, err
	}
	ref, err := connectionProjectReference(displayPath)
	if err != nil {
		return nil, err
	}
	resolved, err := foundry.ResolveContainedFileRefsFromFile(
		document,
		projectRoot,
		filepath.Join(projectRoot, filepath.FromSlash(displayPath)),
	)
	if err != nil {
		return nil, err
	}
	input, err := decodeResolvedConnectionDefinition(serviceName, resolved)
	if err != nil {
		return nil, err
	}
	environmentVariables, environmentBindings, err := validateConnectionDefinitionForAuthoring(input)
	if err != nil {
		return nil, err
	}
	if err := validateUniqueConnectionServiceNames(
		snapshot.services, serviceName, input, projectRoot,
	); err != nil {
		return nil, err
	}

	env, includeEnv, err := mergeConnectionEnvironment(
		rawExisting,
		environmentVariables,
		environmentBindings,
	)
	if err != nil {
		return nil, err
	}
	projectKey, err := chooseConnectionProject(
		ctx,
		snapshot.projectKeys,
		owners,
		existing,
		projectExplicit,
		requestedProject,
		noPrompt,
		selectProject,
	)
	if err != nil {
		return nil, err
	}
	reconciledUses := reconcileConnectionUses(uses, snapshot.projectKeys, projectKey)

	candidate := makeConnectionServiceSection(
		rawExisting,
		existing,
		ref,
		reconciledUses,
		env,
		includeEnv,
	)
	candidateInput, candidateVariables, candidateBindings, err := resolveConnectionAuthoringDefinition(
		serviceName, candidate, projectRoot,
	)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(input, candidateInput) ||
		!slices.Equal(environmentVariables, candidateVariables) ||
		!slices.Equal(environmentBindings, candidateBindings) {
		return nil, connectionDefinitionChanged(serviceName)
	}
	if err := validateConnectionProjectCandidate(ctx, snapshot, serviceName, candidate); err != nil {
		return nil, err
	}

	plan := &connectionAddPlan{
		baseline:             snapshot.services,
		planClient:           client,
		projectRoot:          projectRoot,
		serviceName:          serviceName,
		ref:                  ref,
		projectKey:           projectKey,
		environmentVariables: slices.Clone(environmentVariables),
		environmentBindings:  slices.Clone(environmentBindings),
		serviceSection:       candidate,
		definition:           candidateInput,
	}
	if !existing {
		plan.mutation = "added"
	} else if reflect.DeepEqual(rawExisting, candidate) {
		plan.mutation = "unchanged"
	} else {
		plan.mutation = "updated"
	}
	if err := plan.prepareWrites(rawExisting, reconciledUses, env); err != nil {
		return nil, err
	}
	return plan, nil
}

func connectionDefinitionChanged(serviceName string) error {
	return exterrors.Validation(
		exterrors.CodeProjectChanged,
		fmt.Sprintf(
			"the Connection definition changed after service %q was validated; no changes were written.",
			serviceName,
		),
		"Rerun the command to plan against the current definition.",
	)
}

func loadConnectionProjectSnapshot(
	ctx context.Context,
	client connectionAddProjectClient,
	projectRoot string,
) (*connectionProjectSnapshot, error) {
	var manifest map[string]any
	var err error
	if reader, ok := client.(interface {
		readAuthoringManifest(context.Context) (map[string]any, error)
	}); ok {
		manifest, err = reader.readAuthoringManifest(ctx)
	} else {
		project, contextErr := azdcontext.NewAzdContextFromWd(projectRoot)
		if contextErr != nil {
			return nil, fmt.Errorf("locating the project manifest: %w", contextErr)
		}
		absoluteRoot, rootErr := filepath.Abs(projectRoot)
		if rootErr != nil {
			return nil, fmt.Errorf("locating the project directory: %w", rootErr)
		}
		if project.ProjectDirectory() != filepath.Clean(absoluteRoot) {
			return nil, fmt.Errorf("the authoring project directory does not contain a project manifest")
		}
		manifest, _, err = foundry.DecodeContainedDocument(projectRoot, project.ProjectPath())
	}
	if err != nil {
		return nil, fmt.Errorf("reading the project manifest: %w", err)
	}
	if _, layered := manifest["layers"]; layered {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"local Connection authoring supports top-level services projects, not layered manifests.",
			"Move the service to a top-level services manifest or author it manually.",
		)
	}

	services, err := connectionManifestMap(manifest, "services")
	if err != nil {
		return nil, err
	}
	resources, err := connectionManifestMap(manifest, "resources")
	if err != nil {
		return nil, err
	}

	projectKeys := make([]string, 0)
	for key, value := range services {
		service, ok := value.(map[string]any)
		if !ok {
			continue
		}
		host, ok := service["host"].(string)
		if ok && host == aiProjectServiceHost {
			projectKeys = append(projectKeys, key)
		}
	}
	slices.Sort(projectKeys)

	return &connectionProjectSnapshot{
		manifest:    manifest,
		services:    services,
		resources:   resources,
		projectKeys: projectKeys,
	}, nil
}

func validateConnectionProjectCandidate(
	ctx context.Context,
	snapshot *connectionProjectSnapshot,
	serviceName string,
	service map[string]any,
) error {
	manifest := maps.Clone(snapshot.manifest)
	services := maps.Clone(snapshot.services)
	services[serviceName] = service
	manifest["services"] = services
	content, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encoding the candidate project manifest")
	}
	if _, err := project.Parse(ctx, string(content)); err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"the candidate project manifest has invalid core configuration; no changes were written.",
			"Correct the project structure and core service settings in azure.yaml or azure.yml, then retry.",
		)
	}
	return validateConnectionServiceGraph(services)
}

func validateConnectionServiceGraph(services map[string]any) error {
	pending := make(map[string]int, len(services))
	dependents := make(map[string][]string, len(services))
	for key, raw := range services {
		service, ok := raw.(map[string]any)
		if !ok {
			return invalidProjectSection("services")
		}
		uses, err := parseConnectionUses(service)
		if err != nil {
			return err
		}
		pending[key] = 0
		for _, dependency := range uses {
			// Resource dependencies do not affect service ordering.
			if _, isService := services[dependency]; isService {
				pending[key]++
				dependents[dependency] = append(dependents[dependency], key)
			}
		}
	}
	queue := make([]string, 0, len(services))
	for key, count := range pending {
		if count == 0 {
			queue = append(queue, key)
		}
	}
	for index := 0; index < len(queue); index++ {
		for _, dependent := range dependents[queue[index]] {
			pending[dependent]--
			if pending[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}
	if len(queue) != len(services) {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"the candidate project manifest contains a service dependency cycle; no changes were written.",
			"Remove circular uses dependencies in azure.yaml or azure.yml, then retry.",
		)
	}
	return nil
}

func connectionManifestMap(manifest map[string]any, path string) (map[string]any, error) {
	value, found := manifest[path]
	if !found || (path == "resources" && value == nil) {
		return map[string]any{}, nil
	}
	sectionMap, ok := value.(map[string]any)
	if !ok {
		return nil, invalidProjectSection(path)
	}
	return sectionMap, nil
}

func invalidProjectSection(path string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf("project %s must be a mapping.", path),
		"Correct the azure.yaml project section and retry.",
	)
}

func readExistingConnectionService(
	snapshot *connectionProjectSnapshot,
	serviceName string,
) (map[string]any, bool, error) {
	value, found := snapshot.services[serviceName]
	if !found {
		return nil, false, nil
	}
	service, ok := value.(map[string]any)
	if !ok {
		return nil, false, connectionServiceKeyConflict(serviceName)
	}
	host, ok := service["host"].(string)
	if !ok || host != aiConnectionHost {
		return nil, false, connectionServiceKeyConflict(serviceName)
	}

	return service, true, nil
}

func connectionServiceKeyConflict(serviceName string) error {
	return exterrors.Validation(
		exterrors.CodeConflictingArguments,
		fmt.Sprintf("service key %q is already used by a different service host.", serviceName),
		"Choose a new service key for the Connection.",
	)
}

func parseConnectionUses(service map[string]any) ([]string, error) {
	value, found := service["uses"]
	if !found || value == nil {
		return nil, nil
	}
	var entries []any
	switch typed := value.(type) {
	case []any:
		entries = typed
	case []string:
		entries = make([]any, len(typed))
		for index, entry := range typed {
			entries[index] = entry
		}
	default:
		return nil, malformedConnectionUses()
	}

	uses := make([]string, 0, len(entries))
	for _, entry := range entries {
		value, ok := entry.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, malformedConnectionUses()
		}
		uses = append(uses, value)
	}
	return uses, nil
}

func malformedConnectionUses() error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		"Connection service uses must be a list of non-blank service or resource keys.",
		"Repair the uses list in azure.yaml before adding or updating this Connection.",
	)
}

func validateConnectionDependencies(
	uses []string,
	snapshot *connectionProjectSnapshot,
) (map[string]struct{}, error) {
	owners := map[string]struct{}{}
	for _, dependency := range uses {
		value, found := snapshot.services[dependency]
		if found {
			service, ok := value.(map[string]any)
			if !ok {
				return nil, exterrors.Validation(
					exterrors.CodeInvalidParameter,
					fmt.Sprintf("Connection dependency %q is not a valid service entry.", dependency),
					"Repair the uses list or the referenced service in azure.yaml.",
				)
			}
			if host, found := service["host"]; found && host != nil {
				hostName, ok := host.(string)
				if !ok {
					return nil, exterrors.Validation(
						exterrors.CodeInvalidParameter,
						fmt.Sprintf("Connection dependency %q has an invalid service host.", dependency),
						"Repair the referenced service in azure.yaml before retrying.",
					)
				}
				if hostName == aiProjectServiceHost {
					owners[dependency] = struct{}{}
				}
			}
			continue
		}
		if _, found := snapshot.resources[dependency]; found {
			continue
		}
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("Connection dependency %q in uses does not exist.", dependency),
			"Repair or remove the dangling uses entry in azure.yaml before retrying.",
		)
	}
	return owners, nil
}

func chooseConnectionProject(
	ctx context.Context,
	projectKeys []string,
	owners map[string]struct{},
	existing bool,
	explicit bool,
	requested string,
	noPrompt bool,
	selectProject connectionProjectSelector,
) (string, error) {
	if explicit {
		if strings.TrimSpace(requested) == "" {
			return "", exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--project must name an existing azure.ai.project service key.",
				"Provide a non-blank service key with --project.",
			)
		}
		if !slices.Contains(projectKeys, requested) {
			return "", invalidConnectionProjectSelection(requested)
		}
		return requested, nil
	}

	if existing {
		switch len(owners) {
		case 1:
			for owner := range owners {
				return owner, nil
			}
		case 0:
			if len(projectKeys) == 0 {
				return "", noConnectionProject()
			}
			return "", connectionProjectRepairRequired()
		default:
			return "", connectionProjectRepairRequired()
		}
	}

	switch len(projectKeys) {
	case 0:
		return "", noConnectionProject()
	case 1:
		return projectKeys[0], nil
	default:
		if noPrompt {
			return "", exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"multiple azure.ai.project services exist; --project is required in no-prompt mode.",
				"Rerun with --project <service-key> to select the owning Project.",
			)
		}
		if selectProject == nil {
			return "", fmt.Errorf("Project selection is unavailable")
		}
		selected, err := selectProject(ctx, projectKeys)
		if err != nil {
			return "", err
		}
		if !slices.Contains(projectKeys, selected) {
			return "", invalidConnectionProjectSelection(selected)
		}
		return selected, nil
	}
}

func invalidConnectionProjectSelection(projectKey string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf("--project value %q does not identify an existing azure.ai.project service.", projectKey),
		"Choose a service key whose host is azure.ai.project.",
	)
}

func noConnectionProject() error {
	return exterrors.Validation(
		exterrors.CodeMissingConnectionField,
		"no azure.ai.project service exists in this project.",
		"Run `azd ai project add` to add a Foundry Project before adding a Connection.",
	)
}

func connectionProjectRepairRequired() error {
	return exterrors.Validation(
		exterrors.CodeConflictingArguments,
		"the existing Connection service does not have one unambiguous Foundry Project owner.",
		"Rerun with --project <service-key> to select an existing azure.ai.project service.",
	)
}

func reconcileConnectionUses(
	uses []string,
	projectKeys []string,
	selectedProject string,
) []string {
	projects := make(map[string]struct{}, len(projectKeys))
	for _, key := range projectKeys {
		projects[key] = struct{}{}
	}

	firstProjectIndex := -1
	for index, dependency := range uses {
		if _, found := projects[dependency]; found {
			firstProjectIndex = index
			break
		}
	}

	result := make([]string, 0, len(uses)+1)
	for index, dependency := range uses {
		if _, found := projects[dependency]; found {
			if index == firstProjectIndex {
				result = append(result, selectedProject)
			}
			continue
		}
		result = append(result, dependency)
	}
	if firstProjectIndex == -1 {
		result = append(result, selectedProject)
	}
	return result
}

func mergeConnectionEnvironment(
	service map[string]any,
	required []string,
	referenced []string,
) (map[string]any, bool, error) {
	var env map[string]any
	envFound := false
	if service != nil {
		value, found := service["env"]
		envFound = found
		if found {
			switch typed := value.(type) {
			case nil:
				env = nil
			case map[string]any:
				env = maps.Clone(typed)
			case map[string]string:
				env = make(map[string]any, len(typed))
				for key, value := range typed {
					env[key] = value
				}
			default:
				return nil, false, exterrors.Validation(
					exterrors.CodeInvalidParameter,
					"Connection service env must be a mapping of string values.",
					"Repair the service env section in azure.yaml before retrying.",
				)
			}
		}
	}

	for _, key := range slices.Sorted(maps.Keys(env)) {
		if _, ok := env[key].(string); !ok {
			return nil, false, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("service env binding for %q must be a string.", key),
				"Repair the service env section in azure.yaml before retrying.",
			)
		}
	}

	added := false
	requiredSet := make(map[string]struct{}, len(required))
	for _, variable := range required {
		requiredSet[variable] = struct{}{}
		expected := "${" + variable + "}"
		if env == nil {
			env = make(map[string]any)
		}
		current, found := env[variable]
		if found {
			value, ok := current.(string)
			if !ok || value != expected {
				return nil, false, exterrors.Validation(
					exterrors.CodeConflictingArguments,
					fmt.Sprintf("service env binding for %q conflicts with the required reference.", variable),
					fmt.Sprintf("Set env.%q to %q or remove the conflicting binding.", variable, expected),
				)
			}
			continue
		}
		env[variable] = expected
		added = true
	}
	for _, variable := range referenced {
		if _, isRequired := requiredSet[variable]; isRequired {
			continue
		}
		if env == nil {
			env = make(map[string]any)
		}
		if current, found := env[variable]; found {
			if _, ok := current.(string); !ok {
				return nil, false, exterrors.Validation(
					exterrors.CodeInvalidParameter,
					fmt.Sprintf("service env binding for %q must be a string.", variable),
					"Repair the service env section in azure.yaml before retrying.",
				)
			}
			continue
		}
		env[variable] = "${" + variable + "}"
		added = true
	}
	return env, envFound || added, nil
}

func validateUniqueConnectionServiceNames(
	services map[string]any,
	candidateKey string,
	candidate *definition.Definition,
	projectRoot string,
) error {
	names := map[string]string{
		strings.ToLower(effectiveConnectionName(candidateKey, candidate)): candidateKey,
	}
	for _, key := range slices.Sorted(maps.Keys(services)) {
		if key == candidateKey {
			continue
		}
		value, ok := services[key].(map[string]any)
		if !ok {
			continue
		}
		host, ok := value["host"].(string)
		if !ok || host != aiConnectionHost {
			continue
		}

		source, _ := connectionDefinitionSource(value)
		input := &definition.Definition{}
		if len(source) > 0 {
			resolved, err := foundry.ResolveContainedFileRefs(source, projectRoot)
			if err != nil {
				return err
			}
			input, err = decodeResolvedConnectionDefinition(key, resolved)
			if err != nil {
				return err
			}
		}
		name := strings.ToLower(effectiveConnectionName(key, input))
		if previous, found := names[name]; found {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("Connection services %q and %q resolve to the same Connection name.", previous, key),
				"Use unique Connection names across services, including names loaded from $ref files.",
			)
		}
		names[name] = key
	}
	return nil
}

func connectionProjectReference(displayPath string) (string, error) {
	if strings.TrimSpace(displayPath) == "" {
		return "", fmt.Errorf("Connection definition path is empty")
	}
	relativePath := path.Clean(strings.ReplaceAll(displayPath, "\\", "/"))
	nativePath := filepath.FromSlash(relativePath)
	if relativePath == "." ||
		path.IsAbs(relativePath) ||
		filepath.IsAbs(nativePath) ||
		filepath.VolumeName(nativePath) != "" {
		return "", fmt.Errorf("Connection definition path is not project-relative")
	}
	if relativePath == ".." || strings.HasPrefix(relativePath, "../") {
		return "", fmt.Errorf("Connection definition path is outside the project")
	}
	return "./" + strings.TrimPrefix(relativePath, "./"), nil
}

func connectionDefinitionSource(service map[string]any) (map[string]any, bool) {
	source := make(map[string]any)
	for key, value := range service {
		if _, coreOwned := connectionCoreServiceFields[key]; coreOwned {
			continue
		}
		source[key] = value
	}
	if len(source) > 0 {
		return source, false
	}
	config, ok := service["config"].(map[string]any)
	if !ok || len(config) == 0 {
		return map[string]any{}, false
	}
	return maps.Clone(config), true
}

func makeConnectionServiceSection(
	existing map[string]any,
	isExisting bool,
	ref string,
	uses []string,
	env map[string]any,
	includeEnv bool,
) map[string]any {
	candidate := map[string]any{}
	if isExisting {
		candidate = maps.Clone(existing)
		_, fromConfig := connectionDefinitionSource(existing)
		if fromConfig {
			delete(candidate, "config")
		}
		for field := range connectionDefinitionFields {
			delete(candidate, field)
		}
	}
	candidate["host"] = aiConnectionHost
	candidate["$ref"] = ref
	candidate["uses"] = stringsToAny(uses)
	if includeEnv {
		if env == nil {
			// Preserve null so unchanged reruns remain no-ops.
			candidate["env"] = nil
		} else {
			candidate["env"] = env
		}
	} else {
		delete(candidate, "env")
	}
	return candidate
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func connectionServiceDefinition(service map[string]any) (map[string]any, error) {
	source, _ := connectionDefinitionSource(service)
	if len(source) == 0 {
		return nil, fmt.Errorf("Connection service definition is empty")
	}
	return source, nil
}

func resolveConnectionAuthoringDefinition(
	serviceName string,
	service map[string]any,
	projectRoot string,
) (*definition.Definition, []string, []string, error) {
	source, err := connectionServiceDefinition(service)
	if err != nil {
		return nil, nil, nil, err
	}
	resolved, err := foundry.ResolveContainedFileRefs(source, projectRoot)
	if err != nil {
		return nil, nil, nil, err
	}
	input, err := decodeResolvedConnectionDefinition(serviceName, resolved)
	if err != nil {
		return nil, nil, nil, err
	}
	variables, bindings, err := validateConnectionDefinitionForAuthoring(input)
	if err != nil {
		return nil, nil, nil, err
	}
	return input, variables, bindings, nil
}

func (plan *connectionAddPlan) prepareWrites(
	existing map[string]any,
	uses []string,
	env map[string]any,
) error {
	if plan.mutation == "added" {
		plan.addRequest = &azdext.AddServiceRequest{
			Service: &azdext.ServiceConfig{
				Name: plan.serviceName,
				Host: aiConnectionHost,
				Uses: slices.Clone(uses),
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"$ref": structpb.NewStringValue(plan.ref),
				}},
			},
		}
		if len(env) > 0 {
			value, err := structpb.NewValue(env)
			if err != nil {
				return fmt.Errorf("encoding Connection service environment bindings")
			}
			// AddService escapes templates in its Environment field.
			plan.changes = append(plan.changes, connectionServiceChange{path: "env", value: value})
		}
		return nil
	}
	// Remove legacy config before $ref so interrupted reruns finish
	// migrating it. Keep config that is not a legacy definition.
	if _, preserved := plan.serviceSection["config"]; !preserved {
		if _, existed := existing["config"]; existed {
			plan.changes = append(plan.changes, connectionServiceChange{path: "config"})
		}
	}
	for _, field := range []string{"$ref", "uses", "env"} {
		value, found := plan.serviceSection[field]
		old, existed := existing[field]
		if found == existed && reflect.DeepEqual(old, value) {
			continue
		}
		var encoded *structpb.Value
		if found {
			var err error
			encoded, err = structpb.NewValue(value)
			if err != nil {
				return fmt.Errorf("encoding Connection service field %q", field)
			}
		}
		plan.changes = append(plan.changes, connectionServiceChange{path: field, value: encoded})
	}
	// Write $ref before removing inline fields so interrupted reruns
	// can find and remove the remaining inline fields.
	for _, field := range slices.Sorted(maps.Keys(existing)) {
		if _, preserved := plan.serviceSection[field]; preserved {
			continue
		}
		if _, owned := connectionDefinitionFields[field]; !owned {
			continue
		}
		plan.changes = append(plan.changes, connectionServiceChange{path: field})
	}
	return nil
}

// verifyUnchanged re-reads the manifest and definition before writes.
// It checks the planned service's managed fields, dependency presence
// and hosts, definition contents, and environment references. It also
// checks effective-name uniqueness and validates the candidate graph.
// Changes to unrelated services and unowned fields are left alone.
//
// This narrows the race window, but the RPCs lack compare-and-swap.
// Later concurrent changes can still be overwritten.
func (plan *connectionAddPlan) verifyUnchanged(ctx context.Context) error {
	if plan.baseline == nil || plan.planClient == nil {
		return nil
	}
	current, err := loadConnectionProjectSnapshot(ctx, plan.planClient, plan.projectRoot)
	if err != nil {
		return fmt.Errorf("re-reading the project manifest before writing: %w", err)
	}

	changed := !reflect.DeepEqual(
		plan.plannedServiceState(plan.baseline),
		plan.plannedServiceState(current.services),
	)
	if plan.definition != nil {
		input, variables, bindings, err := resolveConnectionAuthoringDefinition(
			plan.serviceName, plan.serviceSection, plan.projectRoot,
		)
		if err != nil || !reflect.DeepEqual(plan.definition, input) ||
			!slices.Equal(plan.environmentVariables, variables) ||
			!slices.Equal(plan.environmentBindings, bindings) {
			return connectionDefinitionChanged(plan.serviceName)
		}
		if err := validateUniqueConnectionServiceNames(
			current.services, plan.serviceName, input, plan.projectRoot,
		); err != nil {
			return err
		}
	}
	for _, key := range plan.dependencyKeys() {
		if !reflect.DeepEqual(
			connectionDependencyState(plan.baseline, key),
			connectionDependencyState(current.services, key),
		) {
			changed = true
		}
	}
	if !changed {
		uses, err := parseConnectionUses(plan.serviceSection)
		if err != nil {
			return err
		}
		_, err = validateConnectionDependencies(uses, current)
		if err != nil {
			return err
		}
		services := maps.Clone(current.services)
		services[plan.serviceName] = plan.serviceSection
		return validateConnectionServiceGraph(services)
	}
	return exterrors.Validation(
		exterrors.CodeProjectChanged,
		fmt.Sprintf(
			"the project manifest changed after Connection service %q was planned; "+
				"no changes were written. Rerun the command to plan against the current manifest.",
			plan.serviceName,
		),
		"Another process changed azure.yaml while this command ran. Rerun the command.",
	)
}

// plannedServiceState selects fields the plan reads or writes.
// The baseline field set excludes new unowned fields added later.
func (plan *connectionAddPlan) plannedServiceState(services map[string]any) map[string]any {
	raw, exists := services[plan.serviceName]
	state := map[string]any{"exists": exists}
	service, ok := raw.(map[string]any)
	if !ok {
		state["value"] = raw
		return state
	}
	baseline, _ := plan.baseline[plan.serviceName].(map[string]any)
	fields := []string{"host", "uses", "env"}
	fields = append(fields, slices.Sorted(maps.Keys(connectionDefinitionFields))...)
	if _, hadConfig := baseline["config"]; hadConfig {
		fields = append(fields, "config")
	}
	for _, field := range fields {
		if value, found := service[field]; found {
			state[field] = value
		}
	}
	return state
}

func (plan *connectionAddPlan) dependencyKeys() []string {
	keys := []string{}
	if plan.projectKey != "" {
		keys = append(keys, plan.projectKey)
	}
	switch uses := plan.serviceSection["uses"].(type) {
	case []string:
		keys = append(keys, uses...)
	case []any:
		for _, use := range uses {
			if key, ok := use.(string); ok {
				keys = append(keys, key)
			}
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

func connectionDependencyState(services map[string]any, key string) map[string]any {
	raw, exists := services[key]
	state := map[string]any{"exists": exists}
	if service, ok := raw.(map[string]any); ok {
		state["host"] = service["host"]
	}
	return state
}

func (plan *connectionAddPlan) apply(ctx context.Context, client connectionAddProjectClient) error {
	if plan.mutation != "unchanged" {
		if err := plan.verifyUnchanged(ctx); err != nil {
			return err
		}
	}
	switch plan.mutation {
	case "added":
		if _, err := client.AddService(ctx, plan.addRequest); err != nil {
			return fmt.Errorf(
				"adding Connection service %q failed; the service may have been added; "+
					"inspect the manifest and rerun to reconcile: %w",
				plan.serviceName, err,
			)
		}
	case "updated":
	case "unchanged":
		return nil
	default:
		return fmt.Errorf("unsupported Connection service mutation %q", plan.mutation)
	}
	for _, change := range plan.changes {
		var err error
		if change.value == nil {
			_, err = client.UnsetServiceConfig(ctx, &azdext.UnsetServiceConfigRequest{
				ServiceName: plan.serviceName, Path: change.path,
			})
		} else {
			_, err = client.SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
				ServiceName: plan.serviceName, Path: change.path, Value: change.value,
			})
		}
		if err != nil {
			return fmt.Errorf(
				"authoring Connection service %q at field %q failed; "+
					"changes may be partially applied; inspect the manifest and rerun to reconcile: %w",
				plan.serviceName, change.path, err,
			)
		}
	}
	return nil
}
