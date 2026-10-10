// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/paths"
	"azureaiagent/internal/pkg/projectconfig"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

type agentServiceAddFlags struct {
	file    string
	project string
	source  string
}

type agentServiceAddActionFlags struct {
	serviceName string
	file        string
	project     string
	projectSet  bool
	source      string
	sourceSet   bool
}

type agentServiceAddResult struct {
	Name         string   `json:"name"`
	Host         string   `json:"host"`
	Mutation     string   `json:"mutation"`
	Ref          string   `json:"ref"`
	Project      string   `json:"project"`
	Dependencies []string `json:"dependencies"`
}

type agentServiceAddAction struct {
	projectClient azdext.ProjectServiceClient
	flags         agentServiceAddActionFlags
	outputFormat  string
	out           io.Writer
	errOut        io.Writer
}

type agentServiceAddClientFactory func() (
	azdext.ProjectServiceClient,
	func(),
	error,
)

type agentServiceAddProject struct {
	rawServices map[string]map[string]any
	services    map[string]*azdext.ServiceConfig
}

type agentServiceAddRawService struct {
	ResourceGroupName    string         `yaml:"resourceGroup"`
	ResourceName         string         `yaml:"resourceName"`
	ApiVersion           string         `yaml:"apiVersion"`
	RelativePath         string         `yaml:"project"`
	Host                 string         `yaml:"host"`
	Language             string         `yaml:"language"`
	OutputPath           string         `yaml:"dist"`
	Image                string         `yaml:"image"`
	Docker               any            `yaml:"docker"`
	K8s                  any            `yaml:"k8s"`
	Module               any            `yaml:"module"`
	Infra                any            `yaml:"infra"`
	Hooks                any            `yaml:"hooks"`
	Uses                 []string       `yaml:"uses"`
	Config               map[string]any `yaml:"config"`
	Environment          map[string]any `yaml:"env"`
	Condition            any            `yaml:"condition"`
	RemoteBuild          any            `yaml:"remoteBuild"`
	AdditionalProperties map[string]any `yaml:",inline"`
}

func newAgentServiceAddCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newAgentServiceAddCommandWithClientFactory(
		extCtx,
		func() (azdext.ProjectServiceClient, func(), error) {
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return nil, nil, err
			}
			return azdClient.Project(), azdClient.Close, nil
		},
	)
}

func newAgentServiceAddCommandWithClientFactory(
	extCtx *azdext.ExtensionContext,
	newProjectClient agentServiceAddClientFactory,
) *cobra.Command {
	flags := &agentServiceAddFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a file-backed Agent service to the current azd project.",
		Long: `Add or update a file-backed Agent service in the current project's azure.yaml.

<name> is the azure.yaml service key, not the Foundry agent name. --file must
refer to a direct YAML or JSON definition for a prompt, hosted (including code
deployment), voice, or prompt-voice Agent. The service's root $ref resolves
from the directory containing azure.yaml.

The command validates locally and writes through azd's project API only. It
does not authenticate to Azure, create remote resources, copy source files,
or deploy the Agent. The project must declare exactly one
azure.ai.project service; multiple Project services are rejected before any
write. --project may confirm that service's key. --source overrides the Agent
service's project directory and must name an existing directory inside the
current project.

If <name> already identifies an Agent service, the command updates its
Agent-owned declaration and preserves other service fields. Deploy it with
'azd deploy <name>' or 'azd up'.`,
		Example: `  # Add an Agent service from a direct definition
  azd ai agent add support-agent --file ./agents/support.yaml

  # Override the source directory and confirm the sole Foundry Project service
  azd ai agent add support-agent --file ./agents/support.json \
    --source ./src/support-agent --project ai-project

  # Emit the service declaration as JSON
  azd ai agent add support-agent --file ./agents/support.yaml --output json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(flags.file) == "" {
				return exterrors.Validation(
					exterrors.CodeInvalidFilePath,
					"--file must identify a direct Agent definition",
					"pass an existing .yaml, .yml, or .json file inside the current project",
				)
			}
			if cmd.Flags().Changed("project") &&
				strings.TrimSpace(flags.project) == "" {
				return exterrors.Validation(
					exterrors.CodeConflictingArguments,
					"--project must name the project's azure.ai.project service",
					"omit --project or pass the service key of the sole azure.ai.project service",
				)
			}

			projectClient, closeClient, err := newProjectClient()
			if err != nil {
				return exterrors.Internal(
					exterrors.CodeAzdClientFailed,
					fmt.Sprintf("failed to connect to azd: %s", err),
				)
			}
			if closeClient != nil {
				defer closeClient()
			}

			action := &agentServiceAddAction{
				projectClient: projectClient,
				flags: agentServiceAddActionFlags{
					serviceName: args[0],
					file:        flags.file,
					project:     flags.project,
					projectSet:  cmd.Flags().Changed("project"),
					source:      flags.source,
					sourceSet:   cmd.Flags().Changed("source"),
				},
				outputFormat: extCtx.OutputFormat,
				out:          cmd.OutOrStdout(),
				errOut:       cmd.ErrOrStderr(),
			}
			return action.Run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&flags.file, "file", "", "Path to a direct Agent definition file.")
	cmd.Flags().StringVar(
		&flags.project,
		"project",
		"",
		"Service key for the sole azure.ai.project service.",
	)
	cmd.Flags().StringVar(
		&flags.source,
		"source",
		"",
		"Override the Agent service's project directory.",
	)
	_ = cmd.MarkFlagRequired("file")

	return cmd
}

func (a *agentServiceAddAction) Run(ctx context.Context) (runErr error) {
	if a.projectClient == nil {
		return fmt.Errorf("project client is required")
	}
	outputFormat, err := azdext.ParseOutputFormat(a.outputFormat)
	if err != nil {
		return err
	}
	a.outputFormat = string(outputFormat)
	if err := validateAgentServiceDeclarationName(a.flags.serviceName); err != nil {
		return err
	}
	if strings.TrimSpace(a.flags.file) == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidFilePath,
			"--file must identify a direct Agent definition",
			"pass an existing .yaml, .yml, or .json file inside the current project",
		)
	}
	if a.flags.sourceSet && strings.TrimSpace(a.flags.source) == "" {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--source must name a project directory",
			"pass an existing directory inside the current project",
		)
	}

	response, err := a.projectClient.Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return err
	}
	projectConfig := response.GetProject()
	if projectConfig == nil || strings.TrimSpace(projectConfig.GetPath()) == "" {
		return exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			"the current directory does not contain an azd project",
			"run the command from a directory containing azure.yaml",
		)
	}
	projectRoot := projectConfig.GetPath()

	projectLock, err := acquireAgentAddProjectLock(ctx, projectRoot)
	if err != nil {
		return err
	}
	if projectLock != nil {
		defer func() {
			if unlockErr := projectLock.Unlock(); unlockErr != nil {
				runErr = errors.Join(
					runErr,
					fmt.Errorf("unlocking agent project configuration: %w", unlockErr),
				)
			}
		}()
	}

	currentProject, err := readAgentServiceAddProject(projectRoot)
	if err != nil {
		return err
	}

	if err := validateAgentServiceAddProjectFlag(
		a.flags.project,
		a.flags.projectSet,
		currentProject.services,
	); err != nil {
		return err
	}

	var sourceOverride *string
	if a.flags.sourceSet {
		if err := validateAgentServiceAddSource(
			projectRoot,
			a.flags.serviceName,
			a.flags.source,
		); err != nil {
			return err
		}
		sourceOverride = &a.flags.source
	}

	var existingService *structpb.Struct
	if values, found := currentProject.rawServices[a.flags.serviceName]; found {
		existingService, err = agentServiceAddStruct(values)
		if err != nil {
			return exterrors.ValidationFromError(
				err,
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf(
					"service %q in the current project has unsupported configuration",
					a.flags.serviceName,
				),
				"fix the service configuration in azure.yaml before adding an Agent definition",
			)
		}
		if err := validateExistingAgentService(a.flags.serviceName, existingService); err != nil {
			return err
		}
	}

	definition, err := projectpkg.LoadAgentDefinitionFile(
		projectRoot,
		a.flags.file,
		a.flags.serviceName,
	)
	if err != nil {
		return err
	}
	dependencies, err := planAgentServiceUses(
		a.flags.serviceName,
		definition,
		nil,
		currentProject.services,
		projectRoot,
	)
	if err != nil {
		return err
	}
	plan, err := planAgentServiceDeclaration(agentServiceDeclarationInput{
		ServiceName:     a.flags.serviceName,
		ProjectRoot:     projectRoot,
		Definition:      definition,
		Dependencies:    dependencies,
		ExistingService: existingService,
		SourceOverride:  sourceOverride,
	})
	if err != nil {
		return err
	}

	result, err := agentServiceAddResultFromPlan(a.flags.serviceName, plan)
	if err != nil {
		return err
	}

	switch plan.Mutation {
	case agentServiceMutationAdded:
		if plan.NewServiceConfig == nil {
			return fmt.Errorf("Agent add plan is missing the new service configuration")
		}
		if _, err := a.projectClient.AddService(ctx, &azdext.AddServiceRequest{
			Service: plan.NewServiceConfig,
		}); err != nil {
			return err
		}
	case agentServiceMutationUpdated:
		if plan.DesiredService == nil {
			return fmt.Errorf("Agent update plan is missing the complete service section")
		}
		if _, err := a.projectClient.SetServiceConfigSection(
			ctx,
			&azdext.SetServiceConfigSectionRequest{
				ServiceName: a.flags.serviceName,
				Path:        "",
				Section:     plan.DesiredService,
			},
		); err != nil {
			return err
		}
	case agentServiceMutationUnchanged:
	default:
		return fmt.Errorf("Agent add plan has unknown mutation %q", plan.Mutation)
	}

	for _, warning := range plan.Warnings {
		if _, err := fmt.Fprintf(a.errOut, "Warning: %s\n", warning); err != nil {
			return fmt.Errorf("writing Agent service warning: %w", err)
		}
	}

	return writeAgentServiceAddResult(a.out, a.outputFormat, result)
}

func readAgentServiceAddProject(
	projectRoot string,
) (agentServiceAddProject, error) {
	data, projectFile, err := projectconfig.ReadProjectFile(projectRoot)
	if err != nil {
		return agentServiceAddProject{}, err
	}
	if projectFile == "" {
		return agentServiceAddProject{}, exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			fmt.Sprintf("no azure.yaml or azure.yml was found in %q", projectRoot),
			"run the command from a directory containing azure.yaml",
		)
	}

	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return agentServiceAddProject{}, invalidAgentServiceAddProjectFile(projectFile, err)
	}
	if document == nil {
		return agentServiceAddProject{}, invalidAgentServiceAddProjectFile(
			projectFile,
			fmt.Errorf("project document must be a mapping"),
		)
	}

	rawServices := map[string]any{}
	if value, found := document["services"]; found {
		var ok bool
		rawServices, ok = value.(map[string]any)
		if !ok {
			return agentServiceAddProject{}, invalidAgentServiceAddProjectFile(
				projectFile,
				fmt.Errorf("services must be a mapping"),
			)
		}
	}

	rawServiceSections := make(map[string]map[string]any, len(rawServices))
	for serviceName, value := range rawServices {
		section, ok := value.(map[string]any)
		if !ok || section == nil {
			return agentServiceAddProject{}, invalidAgentServiceAddProjectFile(
				projectFile,
				fmt.Errorf("service %q must be a mapping", serviceName),
			)
		}
		rawServiceSections[serviceName] = section
	}

	var parsed struct {
		Services map[string]*agentServiceAddRawService `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return agentServiceAddProject{}, invalidAgentServiceAddProjectFile(projectFile, err)
	}

	services, err := agentServiceAddDependencyServices(parsed.Services)
	if err != nil {
		return agentServiceAddProject{}, invalidAgentServiceAddProjectFile(projectFile, err)
	}

	return agentServiceAddProject{
		rawServices: rawServiceSections,
		services:    services,
	}, nil
}

func invalidAgentServiceAddProjectFile(projectFile string, err error) error {
	return exterrors.ValidationFromError(
		err,
		exterrors.CodeInvalidServiceConfig,
		fmt.Sprintf("current project file %q is invalid", filepath.Base(projectFile)),
		"fix the project configuration in azure.yaml before adding an Agent service",
	)
}

func agentServiceAddDependencyServices(
	services map[string]*agentServiceAddRawService,
) (map[string]*azdext.ServiceConfig, error) {
	result := make(map[string]*azdext.ServiceConfig, len(services))
	for serviceName, service := range services {
		if service == nil {
			return nil, fmt.Errorf("service %q has no configuration", serviceName)
		}

		properties, err := agentServiceAddStruct(service.AdditionalProperties)
		if err != nil {
			return nil, fmt.Errorf("reading service %q properties: %w", serviceName, err)
		}
		config, err := agentServiceAddStruct(service.Config)
		if err != nil {
			return nil, fmt.Errorf("reading service %q config: %w", serviceName, err)
		}
		environment, err := agentServiceAddEnvironment(service.Environment)
		if err != nil {
			return nil, fmt.Errorf("reading service %q environment: %w", serviceName, err)
		}

		result[serviceName] = &azdext.ServiceConfig{
			Name:                 serviceName,
			ResourceGroupName:    service.ResourceGroupName,
			ResourceName:         service.ResourceName,
			ApiVersion:           service.ApiVersion,
			RelativePath:         service.RelativePath,
			Host:                 service.Host,
			Language:             service.Language,
			OutputPath:           service.OutputPath,
			Image:                service.Image,
			Config:               config,
			AdditionalProperties: properties,
			Uses:                 slices.Clone(service.Uses),
			Environment:          environment,
		}
	}
	return result, nil
}

func agentServiceAddEnvironment(values map[string]any) (map[string]string, error) {
	if values == nil {
		return nil, nil
	}

	environment := make(map[string]string, len(values))
	for name, value := range values {
		stringValue, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("environment variable %q must be a string", name)
		}
		environment[name] = stringValue
	}
	return environment, nil
}

func agentServiceAddStruct(values map[string]any) (*structpb.Struct, error) {
	if values == nil {
		return nil, nil
	}

	data, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encoding project values as JSON: %w", err)
	}
	var jsonValues map[string]any
	if err := json.Unmarshal(data, &jsonValues); err != nil {
		return nil, fmt.Errorf("decoding project JSON values: %w", err)
	}
	result, err := structpb.NewStruct(jsonValues)
	if err != nil {
		return nil, fmt.Errorf("converting project values to a protobuf struct: %w", err)
	}
	return result, nil
}

func validateAgentServiceAddProjectFlag(
	requested string,
	explicit bool,
	services map[string]*azdext.ServiceConfig,
) error {
	projectServiceName, err := uniqueAgentProjectService(services)
	if err != nil {
		return err
	}
	if !explicit {
		return nil
	}

	requested = strings.TrimSpace(requested)
	if requested != projectServiceName {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			fmt.Sprintf(
				"--project %q does not match the sole azure.ai.project service %q",
				requested,
				projectServiceName,
			),
			fmt.Sprintf("use --project %q or omit --project", projectServiceName),
		)
	}
	return nil
}

func validateAgentServiceAddSource(
	projectRoot string,
	serviceName string,
	source string,
) error {
	relativePath, err := normalizeAgentServiceSource(projectRoot, source, true)
	if err != nil {
		return invalidAgentServiceSource(serviceName, source, err)
	}
	resolvedPath, err := paths.JoinAllowRoot(projectRoot, relativePath)
	if err != nil {
		return invalidAgentServiceSource(serviceName, source, err)
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return invalidAgentServiceSource(
			serviceName,
			source,
			fmt.Errorf("cannot inspect source directory: %w", err),
		)
	}
	if !info.IsDir() {
		return invalidAgentServiceSource(
			serviceName,
			source,
			errors.New("path is not a directory"),
		)
	}
	return nil
}

func agentServiceAddResultFromPlan(
	serviceName string,
	plan agentServiceDeclarationPlan,
) (agentServiceAddResult, error) {
	if plan.DesiredService == nil {
		return agentServiceAddResult{}, fmt.Errorf("Agent add plan is missing the desired service")
	}
	stringField := func(name string) (string, error) {
		value := plan.DesiredService.GetFields()[name]
		if value == nil {
			return "", fmt.Errorf("Agent add plan is missing the %q field", name)
		}
		stringValue, ok := value.Kind.(*structpb.Value_StringValue)
		if !ok {
			return "", fmt.Errorf("Agent add plan field %q is not a string", name)
		}
		return stringValue.StringValue, nil
	}

	reference, err := stringField("$ref")
	if err != nil {
		return agentServiceAddResult{}, err
	}
	project, err := stringField("project")
	if err != nil {
		return agentServiceAddResult{}, err
	}

	usesValue := plan.DesiredService.GetFields()["uses"]
	if usesValue == nil {
		return agentServiceAddResult{}, fmt.Errorf("Agent add plan is missing the %q field", "uses")
	}
	usesList, ok := usesValue.Kind.(*structpb.Value_ListValue)
	if !ok {
		return agentServiceAddResult{}, fmt.Errorf("Agent add plan field %q is not a list", "uses")
	}
	dependencies := make([]string, len(usesList.ListValue.GetValues()))
	for index, value := range usesList.ListValue.GetValues() {
		if value == nil {
			return agentServiceAddResult{}, fmt.Errorf(
				"Agent add plan field %q contains a null item",
				"uses",
			)
		}
		stringValue, ok := value.Kind.(*structpb.Value_StringValue)
		if !ok {
			return agentServiceAddResult{}, fmt.Errorf(
				"Agent add plan field %q contains a non-string item",
				"uses",
			)
		}
		dependencies[index] = stringValue.StringValue
	}

	return agentServiceAddResult{
		Name:         serviceName,
		Host:         AiAgentHost,
		Mutation:     string(plan.Mutation),
		Ref:          reference,
		Project:      project,
		Dependencies: dependencies,
	}, nil
}

func writeAgentServiceAddResult(
	out io.Writer,
	outputFormat string,
	result agentServiceAddResult,
) error {
	switch outputFormat {
	case "json":
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return fmt.Errorf("writing Agent service JSON result: %w", err)
		}
		return nil
	case "default", "":
		return writeAgentServiceAddHumanResult(out, result)
	default:
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unsupported output format %q", outputFormat),
			"use --output default or --output json",
		)
	}
}

func writeAgentServiceAddHumanResult(out io.Writer, result agentServiceAddResult) error {
	var message string
	switch agentServiceMutation(result.Mutation) {
	case agentServiceMutationAdded:
		message = fmt.Sprintf("Added Agent service %q", result.Name)
	case agentServiceMutationUpdated:
		message = fmt.Sprintf("Updated Agent service %q", result.Name)
	case agentServiceMutationUnchanged:
		message = fmt.Sprintf("Agent service %q is unchanged", result.Name)
	default:
		return fmt.Errorf("Agent add result has unknown mutation %q", result.Mutation)
	}
	if _, err := fmt.Fprintf(
		out,
		"%s.\nRun `azd deploy %s` or `azd up` to deploy it.\n",
		message,
		result.Name,
	); err != nil {
		return fmt.Errorf("writing Agent service result: %w", err)
	}
	return nil
}
