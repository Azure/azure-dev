// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
)

var parameterExpression = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-[^}]*)?\}`)

type bicepCompiler interface {
	Build(context.Context, string) (bicep.BuildResult, error)
}

type moduleDeploymentRequest struct {
	SubscriptionID   string
	ResourceGroup    string
	ServiceName      string
	Project          *azdext.ProjectConfig
	Service          *azdext.ServiceConfig
	EnvironmentName  string
	EnvironmentValue map[string]string
	ImageName        string
}

type moduleDeploymentResult struct {
	Outputs map[string]string
}

type moduleDeployer interface {
	Deploy(context.Context, *moduleDeploymentRequest, azdext.ProgressReporter) (*moduleDeploymentResult, error)
}

type armDeploymentExecutor interface {
	Deploy(
		context.Context,
		string,
		string,
		string,
		map[string]any,
		map[string]any,
	) (map[string]any, error)
}

type azureModuleDeployer struct {
	compiler bicepCompiler
	executor armDeploymentExecutor
}

func newAzureModuleDeployer(client *azdext.AzdClient, writer io.Writer) moduleDeployer {
	if writer == nil {
		writer = io.Discard
	}
	console := input.NewConsole(
		true,
		false,
		input.Writers{Output: writer, Spinner: writer},
		input.ConsoleHandles{
			Stdin:  strings.NewReader(""),
			Stdout: writer,
			Stderr: writer,
		},
		nil,
		nil,
	)
	runner := azdexec.NewCommandRunner(&azdexec.RunnerOptions{
		Stdin:  strings.NewReader(""),
		Stdout: writer,
		Stderr: writer,
	})
	return &azureModuleDeployer{
		compiler: bicep.NewCli(console, runner),
		executor: &azureARMDeploymentExecutor{
			client: client,
			credential: func(
				ctx context.Context,
				client *azdext.AzdClient,
				subscriptionID string,
			) (azcore.TokenCredential, error) {
				return credentialForSubscription(ctx, client, subscriptionID)
			},
		},
	}
}

func (d *azureModuleDeployer) Deploy(
	ctx context.Context,
	request *moduleDeploymentRequest,
	progress azdext.ProgressReporter,
) (*moduleDeploymentResult, error) {
	bicepPath, parametersPath, err := resolveServiceModulePaths(request.Project, request.Service)
	if err != nil {
		return nil, err
	}
	parameters, err := loadServiceModuleParameters(parametersPath, request.EnvironmentValue)
	if err != nil {
		return nil, err
	}

	progress("Compiling Spring Boot service module")
	build, err := d.compiler.Build(ctx, bicepPath)
	if err != nil {
		return nil, fmt.Errorf("compiling service Bicep module: %w", nonDisclosureError(err))
	}
	var template map[string]any
	if err := json.Unmarshal([]byte(build.Compiled), &template); err != nil {
		return nil, fmt.Errorf("decoding compiled service Bicep module: %w", err)
	}

	progress("Deploying Spring Boot service module")
	deploymentName := serviceDeploymentName(request.ServiceName)
	outputs, err := d.executor.Deploy(
		ctx,
		request.SubscriptionID,
		request.ResourceGroup,
		deploymentName,
		template,
		parameters,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"deploying the authored Spring Boot service module: %w",
			nonDisclosureError(err),
		)
	}
	environmentOutputs, err := deploymentOutputs(outputs)
	if err != nil {
		return nil, err
	}
	return &moduleDeploymentResult{Outputs: environmentOutputs}, nil
}

func resolveServiceModulePaths(
	project *azdext.ProjectConfig,
	service *azdext.ServiceConfig,
) (string, string, error) {
	if project == nil || strings.TrimSpace(project.GetPath()) == "" {
		return "", "", validationError(
			"missing_project_path",
			"project path is unavailable for service module deployment",
			"run the command from an initialized azd project",
		)
	}
	if project.GetInfra() == nil || !strings.EqualFold(project.GetInfra().GetProvider(), "bicep") {
		return "", "", validationError(
			"unsupported_infrastructure_provider",
			"azure.springboot requires an authored Bicep service module",
			"set infra.provider to bicep",
		)
	}

	projectPath, err := filepath.Abs(project.GetPath())
	if err != nil {
		return "", "", fmt.Errorf("resolving project path: %w", err)
	}
	projectPath, err = filepath.EvalSymlinks(projectPath)
	if err != nil {
		return "", "", fmt.Errorf("resolving project path links: %w", err)
	}

	infraPath := strings.TrimSpace(project.GetInfra().GetPath())
	if infraPath == "" {
		infraPath = "infra"
	}
	if filepath.IsAbs(infraPath) {
		return "", "", validationError(
			"invalid_infra_path",
			"infra.path must be relative to the azd project",
			"use a relative Bicep infrastructure path",
		)
	}
	infraRoot := filepath.Join(projectPath, filepath.Clean(infraPath))
	infraRoot, err = filepath.EvalSymlinks(infraRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolving Bicep infrastructure path: %w", err)
	}
	if !osutil.IsPathContained(projectPath, infraRoot) {
		return "", "", validationError(
			"invalid_infra_path",
			"infra.path resolves outside the azd project",
			"use a Bicep infrastructure path contained within the project",
		)
	}

	moduleName, err := serviceModuleName(service)
	if err != nil {
		return "", "", err
	}
	modulePath := filepath.Join(infraRoot, filepath.Clean(filepath.FromSlash(moduleName)))
	bicepPath := modulePath + ".bicep"
	parametersPath := modulePath + ".parameters.json"
	bicepParamPath := modulePath + ".bicepparam"
	if _, err := os.Stat(bicepParamPath); err == nil {
		return "", "", validationError(
			"bicepparam_service_module_unsupported",
			fmt.Sprintf("service %q uses an unsupported .bicepparam file", service.GetName()),
			"use a canonical <module>.parameters.json file for this milestone",
		)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("reading service Bicep parameter path: %w", err)
	}
	for _, path := range []string{bicepPath, parametersPath} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", "", validationError(
				"missing_service_module",
				fmt.Sprintf("service %q is missing required module file %q", service.GetName(), path),
				"add the authored service Bicep module and canonical parameter JSON",
			)
		}
		if !osutil.IsPathContained(infraRoot, resolved) {
			return "", "", validationError(
				"invalid_service_module_path",
				fmt.Sprintf("service %q module resolves outside infra.path", service.GetName()),
				"use a service module contained within the Bicep infrastructure directory",
			)
		}
	}
	return bicepPath, parametersPath, nil
}

func serviceModuleName(service *azdext.ServiceConfig) (string, error) {
	if service == nil {
		return "", validationError("missing_service_config", "service configuration is missing", "check azure.yaml")
	}
	moduleName := ""
	if properties := service.GetAdditionalProperties(); properties != nil {
		if value, present := properties.AsMap()["module"]; present {
			var ok bool
			moduleName, ok = value.(string)
			if !ok {
				return "", validationError(
					"invalid_service_module",
					fmt.Sprintf("service %q module must be a string", service.GetName()),
					"set module to a relative Bicep module name",
				)
			}
		}
	}
	if strings.TrimSpace(moduleName) == "" {
		moduleName = service.GetName()
	}
	moduleName = filepath.ToSlash(filepath.Clean(moduleName))
	if moduleName == "." || moduleName == ".." || strings.HasPrefix(moduleName, "../") ||
		filepath.IsAbs(moduleName) || filepath.Ext(moduleName) != "" {
		return "", validationError(
			"invalid_service_module",
			fmt.Sprintf("service %q has invalid module %q", service.GetName(), moduleName),
			"set module to a relative extensionless path contained within infra.path",
		)
	}
	return moduleName, nil
}

func loadServiceModuleParameters(path string, values map[string]string) (map[string]any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading service module parameters: %w", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(content, &document); err != nil {
		return nil, validationError(
			"invalid_service_parameters",
			fmt.Sprintf("service module parameters are invalid JSON: %v", err),
			"use the canonical parameters.<name>.value JSON shape",
		)
	}
	for key := range document {
		if key != "$schema" && key != "contentVersion" && key != "parameters" {
			return nil, validationError(
				"unsupported_service_parameters",
				fmt.Sprintf("service module parameter file contains unsupported root field %q", key),
				"use only $schema, contentVersion, and parameters",
			)
		}
	}
	rawParameters, present := document["parameters"]
	if !present {
		return nil, validationError(
			"missing_service_parameters",
			"service module parameter file is missing parameters",
			"use the canonical parameters.<name>.value JSON shape",
		)
	}
	var authored map[string]map[string]json.RawMessage
	if err := json.Unmarshal(rawParameters, &authored); err != nil {
		return nil, validationError(
			"invalid_service_parameters",
			"service module parameters must use the canonical nested parameters.<name>.value object shape",
			"use the canonical parameters.<name>.value JSON shape",
		)
	}

	resolved := make(map[string]any, len(authored))
	lookup := environmentLookup(values)
	for name, definition := range authored {
		rawValue, present := definition["value"]
		if !present && len(definition) > 0 {
			return nil, validationError(
				"unsupported_service_parameter",
				fmt.Sprintf("service module parameter %q contains unsupported fields; only value is supported", name),
				"use the canonical parameters.<name>.value JSON shape",
			)
		}
		if len(definition) != 1 {
			return nil, validationError(
				"unsupported_service_parameter",
				fmt.Sprintf("service module parameter %q must contain only value", name),
				"use the canonical parameters.<name>.value JSON shape",
			)
		}
		if !present {
			return nil, validationError(
				"unsupported_service_parameter",
				fmt.Sprintf("service module parameter %q is missing value", name),
				"use the canonical parameters.<name>.value JSON shape",
			)
		}
		var value any
		if err := json.Unmarshal(rawValue, &value); err != nil {
			return nil, fmt.Errorf("decoding service module parameter %q: %w", name, err)
		}
		if stringValue, ok := value.(string); ok {
			for _, match := range parameterExpression.FindAllStringSubmatch(stringValue, -1) {
				if match[2] == "" {
					if current, present := lookup(match[1]); !present || current == "" {
						return nil, validationError(
							"missing_service_parameter_value",
							fmt.Sprintf("service module parameter %q requires environment value %s", name, match[1]),
							"restore the required azd environment output and retry",
						)
					}
				}
			}
			expanded, err := osutil.NewExpandableString(stringValue).Envsubst(func(key string) string {
				value, _ := lookup(key)
				return value
			})
			if err != nil {
				return nil, validationError(
					"invalid_service_parameter_expression",
					fmt.Sprintf("service module parameter %q has an invalid environment expression", name),
					"use a valid azd environment expression",
				)
			}
			value = expanded
		}
		resolved[name] = map[string]any{"value": value}
	}
	return resolved, nil
}

func serviceDeploymentName(serviceName string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(serviceName) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '-' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('-')
		}
	}
	name := strings.Trim(builder.String(), "-")
	if name == "" {
		name = "service"
	}
	const maxServiceLength = 38
	if len(name) > maxServiceLength {
		name = name[:maxServiceLength]
	}
	return fmt.Sprintf("azd-%s-%d", name, time.Now().Unix())
}

func deploymentOutputs(outputs map[string]any) (map[string]string, error) {
	result := make(map[string]string, len(outputs))
	for name, raw := range outputs {
		definition, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("deployment output %q has an invalid shape", name)
		}
		if outputType, _ := definition["type"].(string); strings.HasPrefix(strings.ToLower(outputType), "secure") {
			continue
		}
		value, present := definition["value"]
		if !present {
			return nil, fmt.Errorf("deployment output %q is missing value", name)
		}
		switch typed := value.(type) {
		case string:
			result[name] = typed
		case bool, float64:
			result[name] = fmt.Sprint(typed)
		default:
			encoded, err := json.Marshal(typed)
			if err != nil {
				return nil, fmt.Errorf("encoding deployment output %q: %w", name, err)
			}
			result[name] = string(encoded)
		}
	}
	return result, nil
}

type azureARMDeploymentExecutor struct {
	client     *azdext.AzdClient
	credential func(context.Context, *azdext.AzdClient, string) (azcore.TokenCredential, error)
	options    *arm.ClientOptions
}

func (e *azureARMDeploymentExecutor) Deploy(
	ctx context.Context,
	subscriptionID string,
	resourceGroup string,
	deploymentName string,
	template map[string]any,
	parameters map[string]any,
) (map[string]any, error) {
	credential, err := e.credential(ctx, e.client, subscriptionID)
	if err != nil {
		return nil, err
	}
	deployments, err := armresources.NewDeploymentsClient(subscriptionID, credential, e.options)
	if err != nil {
		return nil, fmt.Errorf("creating ARM deployments client: %w", err)
	}
	mode := armresources.DeploymentModeIncremental
	poller, err := deployments.BeginCreateOrUpdate(
		ctx,
		resourceGroup,
		deploymentName,
		armresources.Deployment{
			Properties: &armresources.DeploymentProperties{
				Mode:       &mode,
				Template:   template,
				Parameters: parameters,
			},
		},
		nil,
	)
	if err != nil {
		return nil, err
	}
	response, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, err
	}
	if response.Properties == nil {
		return nil, errors.New("ARM deployment returned no properties")
	}
	outputs := map[string]any{}
	if response.Properties.Outputs == nil {
		return outputs, nil
	}
	encoded, err := json.Marshal(response.Properties.Outputs)
	if err != nil {
		return nil, fmt.Errorf("encoding ARM deployment outputs: %w", err)
	}
	if err := json.Unmarshal(encoded, &outputs); err != nil {
		return nil, fmt.Errorf("decoding ARM deployment outputs: %w", err)
	}
	return outputs, nil
}
