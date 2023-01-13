// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package terraform contains an implementation of provider.Provider for Terraform. This
// provider is registered for use when this package is imported, and can be imported for
// side effects only to register the provider, e.g.:
//
// require(
//
//	_ "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/terraform"
//
// )
package terraform

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/terraform"
)

// TerraformProvider exposes infrastructure provisioning using Azure Terraform templates
type TerraformProvider struct {
	env         *environment.Environment
	projectPath string
	options     Options
	console     input.Console
	cli         terraform.TerraformCli
}

type TerraformDeploymentDetails struct {
	ParameterFilePath  string
	PlanFilePath       string
	localStateFilePath string
}

// Name gets the name of the infra provider
func (t *TerraformProvider) Name() string {
	return "Terraform"
}

func (t *TerraformProvider) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{t.cli}
}

// NewTerraformProvider creates a new instance of a Terraform Infra provider
func NewTerraformProvider(
	ctx context.Context,
	env *environment.Environment,
	projectPath string,
	infraOptions Options,
	console input.Console,
	commandRunner exec.CommandRunner,
) *TerraformProvider {
	terraformCli := terraform.NewTerraformCli(commandRunner)

	// Default to a module named "main" if not specified.
	if strings.TrimSpace(infraOptions.Module) == "" {
		infraOptions.Module = "main"
	}

	provider := &TerraformProvider{
		env:         env,
		projectPath: projectPath,
		options:     infraOptions,
		console:     console,
		cli:         terraformCli,
	}

	envVars := []string{
		// Sets the terraform data directory env var that will get set on all terraform CLI commands
		fmt.Sprintf("TF_DATA_DIR=%s", provider.dataDirPath()),
		// Required when using service principal login
		fmt.Sprintf("ARM_TENANT_ID=%s", os.Getenv("ARM_TENANT_ID")),
		fmt.Sprintf("ARM_SUBSCRIPTION_ID=%s", env.GetSubscriptionId()),
		fmt.Sprintf("ARM_CLIENT_ID=%s", os.Getenv("ARM_CLIENT_ID")),
		fmt.Sprintf("ARM_CLIENT_SECRET=%s", os.Getenv("ARM_CLIENT_SECRET")),
	}
	terraformCli.SetEnv(envVars)

	return provider
}

// Previews the infrastructure through terraform plan
func (t *TerraformProvider) Plan(
	ctx context.Context,
) *async.InteractiveTaskWithProgress[*DeploymentPlan, *DeploymentPlanningProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeploymentPlan, *DeploymentPlanningProgress]) {
			isRemoteBackendConfig, err := t.isRemoteBackendConfig()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("reading backend config: %w", err))
				return
			}

			modulePath := t.modulePath()

			initRes, err := t.init(ctx, isRemoteBackendConfig)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("terraform init failed: %s , err: %w", initRes, err))
				return
			}

			if err != nil {
				asyncContext.SetError(err)
				return
			}

			err = CreateInputParametersFile(t.parametersTemplateFilePath(), t.parametersFilePath(), t.env.Values)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("creating parameters file: %w", err))
				return
			}

			validated, err := t.cli.Validate(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("terraform validate failed: %s, err %w", validated, err))
				return
			}

			planArgs := t.createPlanArgs(isRemoteBackendConfig)
			runResult, err := t.cli.Plan(ctx, modulePath, t.planFilePath(), planArgs...)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("terraform plan failed:%s err %w", runResult, err))
				return
			}

			//create deployment plan
			deployment, err := t.createDeployment(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("create terraform template failed: %w", err))
				return
			}

			deploymentDetails := TerraformDeploymentDetails{
				ParameterFilePath: t.parametersFilePath(),
				PlanFilePath:      t.planFilePath(),
			}
			if !isRemoteBackendConfig {
				deploymentDetails.localStateFilePath = t.localStateFilePath()
			}

			result := DeploymentPlan{
				Deployment: *deployment,
				Details:    deploymentDetails,
			}

			asyncContext.SetResult(&result)
		})
}

// Deploy the infrastructure within the specified template through terraform apply
func (t *TerraformProvider) Deploy(
	ctx context.Context,
	deployment *DeploymentPlan,
	scope infra.Scope,
) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeployResult, *DeployProgress]) {
			t.console.Message(ctx, "Locating plan file...")

			modulePath := t.modulePath()
			terraformDeploymentData := deployment.Details.(TerraformDeploymentDetails)
			isRemoteBackendConfig, err := t.isRemoteBackendConfig()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("reading backend config: %w", err))
				return
			}

			applyArgs, err := t.createApplyArgs(isRemoteBackendConfig, terraformDeploymentData)
			if err != nil {
				asyncContext.SetError(err)
				return
			}

			runResult, err := t.cli.Apply(ctx, modulePath, applyArgs...)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("template Deploy failed: %s , err:%w", runResult, err))
				return
			}

			// Set the deployment result
			outputs, err := t.createOutputParameters(ctx, modulePath, isRemoteBackendConfig)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("create terraform template failed: %w", err))
				return
			}

			currentDeployment := deployment.Deployment
			currentDeployment.Outputs = outputs
			result := &DeployResult{
				Deployment: &currentDeployment,
			}

			asyncContext.SetResult(result)
		})
}

// Destroys the specified deployment through terraform destroy
func (t *TerraformProvider) Destroy(
	ctx context.Context,
	deployment *Deployment,
	options DestroyOptions,
) *async.InteractiveTaskWithProgress[*DestroyResult, *DestroyProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress]) {

			isRemoteBackendConfig, err := t.isRemoteBackendConfig()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("reading backend config: %w", err))
				return
			}

			t.console.Message(ctx, "Locating parameters file...")
			err = t.ensureParametersFile()
			if err != nil {
				asyncContext.SetError(err)
				return
			}

			modulePath := t.modulePath()

			//load the deployment result
			outputs, err := t.createOutputParameters(ctx, modulePath, isRemoteBackendConfig)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("load terraform template output failed: %w", err))
				return
			}

			t.console.Message(ctx, "Destroying terraform deployment...")
			err = asyncContext.Interact(func() error {
				destroyArgs := t.createDestroyArgs(isRemoteBackendConfig, options.Force())
				runResult, err := t.cli.Destroy(ctx, modulePath, destroyArgs...)
				if err != nil {
					return fmt.Errorf("template Deploy failed:%s , err :%w", runResult, err)
				}

				return nil
			})

			if err != nil {
				asyncContext.SetError(err)
				return
			}

			result := DestroyResult{
				Outputs: outputs,
			}
			asyncContext.SetResult(&result)
		})
}

func (t *TerraformProvider) State(
	ctx context.Context,
	_ infra.Scope,
) *async.InteractiveTaskWithProgress[*StateResult, *StateProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*StateResult, *StateProgress]) {
			isRemoteBackendConfig, err := t.isRemoteBackendConfig()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("reading backend config: %w", err))
				return
			}

			t.console.Message(ctx, "Retrieving terraform state...")
			modulePath := t.modulePath()

			terraformState, err := t.showCurrentState(ctx, modulePath, isRemoteBackendConfig)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("fetching terraform state failed: %w", err))
				return
			}

			state := State{}

			state.Outputs = t.convertOutputs(terraformState.Values.Outputs)
			state.Resources = t.collectAzureResources(terraformState.Values.RootModule)

			result := StateResult{
				State: &state,
			}

			asyncContext.SetResult(&result)
		})
}

// Creates the terraform plan CLI arguments
func (t *TerraformProvider) createPlanArgs(isRemoteBackendConfig bool) []string {
	args := []string{fmt.Sprintf("-var-file=%s", t.parametersFilePath())}

	if !isRemoteBackendConfig {
		args = append(args, fmt.Sprintf("-state=%s", t.localStateFilePath()))
	}

	return args
}

// Creates the terraform apply CLI arguments
func (t *TerraformProvider) createApplyArgs(
	isRemoteBackendConfig bool, data TerraformDeploymentDetails) ([]string, error) {
	args := []string{}
	if !isRemoteBackendConfig {
		args = append(args, fmt.Sprintf("-state=%s", data.localStateFilePath))
	}

	if _, err := os.Stat(data.PlanFilePath); err == nil {
		args = append(args, data.PlanFilePath)
	} else {
		if _, err := os.Stat(data.ParameterFilePath); err != nil {
			return nil, fmt.Errorf("parameters file not found:: %w", err)
		}
		args = append(args, fmt.Sprintf("-var-file=%s", data.ParameterFilePath))
	}

	return args, nil
}

// Creates the terraform destroy CLI arguments
func (t *TerraformProvider) createDestroyArgs(isRemoteBackendConfig bool, autoApprove bool) []string {
	args := []string{fmt.Sprintf("-var-file=%s", t.parametersFilePath())}

	if !isRemoteBackendConfig {
		args = append(args, fmt.Sprintf("-state=%s", t.localStateFilePath()))
	}

	if autoApprove {
		args = append(args, "-auto-approve")
	}

	return args
}

// Checks if the parameters file already exists and creates if as needed.
func (t *TerraformProvider) ensureParametersFile() error {
	if _, err := os.Stat(t.parametersFilePath()); err != nil {
		err := CreateInputParametersFile(t.parametersTemplateFilePath(), t.parametersFilePath(), t.env.Values)
		if err != nil {
			return fmt.Errorf("creating parameters file: %w", err)
		}
	}

	return nil
}

// initialize template terraform provider through terraform init
func (t *TerraformProvider) init(ctx context.Context, isRemoteBackendConfig bool) (string, error) {

	modulePath := t.modulePath()
	cmd := []string{}

	if isRemoteBackendConfig {
		t.console.Message(ctx, "Generating terraform backend config file...")

		err := CreateInputParametersFile(t.backendConfigTemplateFilePath(), t.backendConfigFilePath(), t.env.Values)
		if err != nil {
			return fmt.Sprintf("creating terraform backend config file: %s", err), err
		}
		cmd = append(cmd, fmt.Sprintf("--backend-config=%s", t.backendConfigFilePath()))
	}

	runResult, err := t.cli.Init(ctx, modulePath, cmd...)
	if err != nil {
		return runResult, err
	}

	return runResult, nil
}

// Creates a normalized view of the terraform output.
func (t *TerraformProvider) createOutputParameters(
	ctx context.Context,
	modulePath string,
	isRemoteBackend bool,
) (map[string]OutputParameter, error) {
	cmd := []string{}

	if !isRemoteBackend {
		cmd = append(cmd, fmt.Sprintf("-state=%s", t.localStateFilePath()))
	}

	runResult, err := t.cli.Output(ctx, modulePath, cmd...)
	if err != nil {
		return nil, fmt.Errorf("reading deployment output failed: %s, err:%w", runResult, err)
	}

	var outputMap map[string]terraformOutput
	if err := json.Unmarshal([]byte(runResult), &outputMap); err != nil {
		return nil, err
	}

	return t.convertOutputs(outputMap), nil
}

func (t *TerraformProvider) mapTerraformTypeToInterfaceType(typ any) ParameterType {
	// in the JSON output, the type property maps to either a string (for a primitive type) or an
	// array of things which describe a complex type.
	switch v := typ.(type) {
	case string:
		switch v {
		case "string":
			return ParameterTypeString
		case "bool":
			return ParameterTypeBoolean
		case "number":
			return ParameterTypeNumber
		default:
			panic(fmt.Sprintf("unknown primitive type: %s", v))
		}
	case []any:
		// in this case we have a complex type, which in json looked like ["type", <schema parts>...], just pull out the
		// first part and map to either and object or array.
		switch v[0].(string) {
		case "list", "tuple", "set":
			return ParameterTypeArray
		case "object", "map":
			return ParameterTypeObject
		default:
			panic(fmt.Sprintf("unknown complex type tag: %s (full type: %+v)", v, typ))
		}
	}

	return ParameterTypeString
}

// convertOutputs converts a terraform output map to the canonical format shared by all provider implementations.
func (t *TerraformProvider) convertOutputs(outputMap map[string]terraformOutput) map[string]OutputParameter {
	outputParameters := make(map[string]OutputParameter)
	for k, v := range outputMap {
		if val, ok := v.Value.(string); ok && val == "null" {
			// omit null
			continue
		}

		outputParameters[k] = OutputParameter{
			Type:  t.mapTerraformTypeToInterfaceType(v.Type),
			Value: v.Value,
		}
	}
	return outputParameters
}

func (t *TerraformProvider) showCurrentState(
	ctx context.Context,
	modulePath string,
	isRemoteBackend bool,
) (*terraformShowOutput, error) {
	cmd := []string{}

	if !isRemoteBackend {
		cmd = append(cmd, t.localStateFilePath())
	}

	runResult, err := t.cli.Show(ctx, modulePath, cmd...)
	if err != nil {
		return nil, fmt.Errorf("showing current state failed: %s, err:%w", runResult, err)
	}

	var showOutput terraformShowOutput
	if err := json.Unmarshal([]byte(runResult), &showOutput); err != nil {
		return nil, err
	}

	return &showOutput, nil
}

// Creates the deployment object from the specified module path
func (t *TerraformProvider) createDeployment(ctx context.Context, modulePath string) (*Deployment, error) {
	templateParameters := make(map[string]InputParameter)

	//build the template parameters.
	parameters := make(map[string]any)
	parametersFilePath := t.parametersFilePath()

	// check if the file does not exist to create it --> for shared env scenario
	log.Printf("Reading parameters template file from: %s", parametersFilePath)
	if err := t.ensureParametersFile(); err != nil {
		return nil, err
	}

	parametersBytes, err := os.ReadFile(parametersFilePath)
	if err != nil {
		return nil, fmt.Errorf("reading parameter file template: %w", err)
	}

	if err := json.Unmarshal(parametersBytes, &parameters); err != nil {
		return nil, fmt.Errorf("error unmarshalling template parameters: %w", err)
	}

	for key, param := range parameters {
		templateParameters[key] = InputParameter{
			Type:  key,
			Value: param,
		}
	}

	template := Deployment{
		Parameters: templateParameters,
	}

	return &template, nil
}

// collectAzureResources collects the set of resources from the root module of a terraform state file, including
// resources from all child modules. Only resources managed by azure providers are considered (today, that's
// just resources from the `registry.terraform.io/hashicorp/azurerm` provider). Only "managed" resources are
// considered.
func (t *TerraformProvider) collectAzureResources(rootModule terraformRootModule) []Resource {
	// the set of resources we've seen (keyed by their id)
	azureResources := make(map[string]struct{})

	// Walk over all the modules (starting at the root) and mark each resource we see from the azure
	// provider.
	visitResource := func(r terraformResource) {
		if r.Mode == terraformModeManaged && r.ProviderName == "registry.terraform.io/hashicorp/azurerm" {
			if id, err := t.getIdForManagedResource(r); err != nil {
				log.Printf("error determining id for resource: %v, ignoring...", err)
			} else {
				azureResources[id] = struct{}{}
			}
		}
	}

	var visitChildModule func(c terraformChildModule)
	visitChildModule = func(c terraformChildModule) {
		for _, r := range c.Resources {
			visitResource(r)
		}

		for _, c := range c.ChildModules {
			visitChildModule(c)
		}
	}

	for _, r := range rootModule.Resources {
		visitResource(r)
	}

	for _, c := range rootModule.ChildModules {
		visitChildModule(c)
	}

	// At this point, allResources contains the ids of all the resources we discovered.
	resources := make([]Resource, 0, len(azureResources))
	for id := range azureResources {
		resources = append(resources, Resource{
			Id: id,
		})
	}

	return resources
}

func (t *TerraformProvider) getIdForManagedResource(r terraformResource) (string, error) {
	// Most azure resources use "id" as the key in the values bag that holds the resource id. However, some
	// resources are special and use a different key for the Azure Resource Id.
	idKey := "id"

	switch r.Type {
	case "azurerm_key_vault_secret":
		idKey = "resource_id"
	}

	if val, has := r.Values[idKey]; !has {
		return "", fmt.Errorf("resource %s has no %s property", r.Address, idKey)
	} else if id, ok := val.(string); !ok {
		return "", fmt.Errorf("resource %s has %s property with type %T not string", r.Address, idKey, val)
	} else {
		return id, nil
	}
}

// Gets the path to the project parameters file path
func (t *TerraformProvider) parametersTemplateFilePath() string {
	infraPath := t.options.Path
	if strings.TrimSpace(infraPath) == "" {
		infraPath = "infra"
	}

	parametersFilename := fmt.Sprintf("%s.tfvars.json", t.options.Module)
	return filepath.Join(t.projectPath, infraPath, parametersFilename)
}

// Gets the path to the project backend config file path
func (t *TerraformProvider) backendConfigTemplateFilePath() string {
	infraPath := t.options.Path
	if strings.TrimSpace(infraPath) == "" {
		infraPath = "infra"
	}

	return filepath.Join(t.projectPath, infraPath, "provider.conf.json")
}

// Gets the folder path to the specified module
func (t *TerraformProvider) modulePath() string {
	infraPath := t.options.Path
	if strings.TrimSpace(infraPath) == "" {
		infraPath = "infra"
	}

	return filepath.Join(t.projectPath, infraPath)
}

// Gets the path to the staging .azure terraform plan file path
func (t *TerraformProvider) planFilePath() string {
	planFilename := fmt.Sprintf("%s.tfplan", t.options.Module)
	return filepath.Join(t.projectPath, ".azure", t.env.GetEnvName(), t.options.Path, planFilename)
}

// Gets the path to the staging .azure terraform local state file path
func (t *TerraformProvider) localStateFilePath() string {
	return filepath.Join(t.projectPath, ".azure", t.env.GetEnvName(), t.options.Path, "terraform.tfstate")
}

// Gets the path to the staging .azure parameters file path
func (t *TerraformProvider) backendConfigFilePath() string {
	backendConfigFilename := fmt.Sprintf("%s.conf.json", t.env.GetEnvName())
	return filepath.Join(t.projectPath, ".azure", t.env.GetEnvName(), t.options.Path, backendConfigFilename)
}

// Gets the path to the staging .azure backend config file path
func (t *TerraformProvider) parametersFilePath() string {
	parametersFilename := fmt.Sprintf("%s.tfvars.json", t.options.Module)
	return filepath.Join(t.projectPath, ".azure", t.env.GetEnvName(), t.options.Path, parametersFilename)
}

// Gets the path to the current env.
func (t *TerraformProvider) dataDirPath() string {
	return filepath.Join(t.projectPath, ".azure", t.env.GetEnvName(), t.options.Path, ".terraform")
}

// Check terraform file for remote backend provider
func (t *TerraformProvider) isRemoteBackendConfig() (bool, error) {
	modulePath := t.modulePath()
	infraDir, _ := os.Open(modulePath)
	files, err := infraDir.ReadDir(0)

	if err != nil {
		return false, fmt.Errorf("reading .tf files contents: %w", err)
	}

	for index := range files {
		if !files[index].IsDir() && filepath.Ext(files[index].Name()) == ".tf" {
			fileContent, err := os.ReadFile(filepath.Join(modulePath, files[index].Name()))

			if err != nil {
				return false, fmt.Errorf("error reading .tf files: %w", err)
			}

			if found := strings.Contains(string(fileContent), `backend "azurerm"`); found {
				return true, nil
			}
		}
	}
	return false, nil
}

func init() {
	err := RegisterProvider(
		Terraform,
		func(
			ctx context.Context,
			env *environment.Environment,
			projectPath string,
			options Options,
			console input.Console,
			_ azcli.AzCli,
			commandRunner exec.CommandRunner,
		) (Provider, error) {
			return NewTerraformProvider(ctx, env, projectPath, options, console, commandRunner), nil
		},
	)

	if err != nil {
		panic(err)
	}
}

// terraformShowOutput is a model type for the output of `terraform show` for a tfstate file.
// see https://www.terraform.io/internals/json-format#state-representation for more information on the shape
// of the JSON data
type terraformShowOutput struct {
	FormatVersion string          `json:"format_version"`
	Values        terraformValues `json:"values"`
}

// terraformValues is a model type for the `values-representation` object in a JSON output from terraform.
// see https://www.terraform.io/internals/json-format#values-representation for more information on the shape
// of the JSON data.
type terraformValues struct {
	Outputs    map[string]terraformOutput `json:"outputs"`
	RootModule terraformRootModule        `json:"root_module"`
}

// terraformOutput is a model type for the value in the output map.
type terraformOutput struct {
	Value any `json:"value"`
	// This is either a string or an array objects for a complex type
	Type      any  `json:"type"`
	Sensitive bool `json:"sensitive"`
}

// terraformRootModule is a model type for the "root_module" property the JSON output of a state file.
type terraformRootModule struct {
	Resources    []terraformResource    `json:"resources"`
	ChildModules []terraformChildModule `json:"child_modules"`
}

const terraformModeManaged = "managed"

// terraformResource is the model type for a resource in a terraform state file. The "values"
// array contains provider specific values (for azurerm, this includes "id" which is the resource id).
type terraformResource struct {
	Address      string `json:"address"`
	ProviderName string `json:"provider_name"`
	// "mode" can be "managed", for resources, or "data", for data resources
	Mode   string         `json:"mode"`
	Type   string         `json:"type"`
	Values map[string]any `json:"values"`
}

// terraformChildModule is the model type for a child module in the state file. It may contain
// further child modules which contain additional resources.
type terraformChildModule struct {
	Address      string                 `json:"address"`
	Resources    []terraformResource    `json:"resources"`
	ChildModules []terraformChildModule `json:"child_modules"`
}
