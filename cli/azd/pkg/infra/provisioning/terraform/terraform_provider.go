package terraform

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/terraform"
	"github.com/drone/envsubst"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/exp/maps"
)

const (
	defaultModule = "main"
	defaultPath   = "infra"
)

// TerraformProvider exposes infrastructure provisioning using Azure Terraform templates
type TerraformProvider struct {
	envManager   environment.Manager
	env          *environment.Environment
	prompters    prompt.Prompter
	console      input.Console
	cli          terraform.TerraformCli
	curPrincipal CurrentPrincipalIdProvider
	projectPath  string
	options      Options
}

type terraformDeploymentDetails struct {
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
	cli terraform.TerraformCli,
	envManager environment.Manager,
	env *environment.Environment,
	console input.Console,
	curPrincipal CurrentPrincipalIdProvider,
	prompters prompt.Prompter,
) Provider {
	provider := &TerraformProvider{
		envManager:   envManager,
		env:          env,
		console:      console,
		cli:          cli,
		curPrincipal: curPrincipal,
		prompters:    prompters,
	}

	return provider
}

func (t *TerraformProvider) Initialize(ctx context.Context, projectPath string, options Options) error {
	t.projectPath = projectPath
	t.options = options
	if t.options.Module == "" {
		t.options.Module = defaultModule
	}
	if t.options.Path == "" {
		t.options.Path = defaultPath
	}

	requiredTools := t.RequiredExternalTools()
	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return err
	}

	if err := t.EnsureEnv(ctx); err != nil {
		return err
	}

	envVars := []string{
		// Sets the terraform data directory env var that will get set on all terraform CLI commands
		fmt.Sprintf("TF_DATA_DIR=%s", t.dataDirPath()),
		// Required when using service principal login
		fmt.Sprintf("ARM_TENANT_ID=%s", os.Getenv("ARM_TENANT_ID")),
		fmt.Sprintf("ARM_SUBSCRIPTION_ID=%s", t.env.GetSubscriptionId()),
		fmt.Sprintf("ARM_CLIENT_ID=%s", os.Getenv("ARM_CLIENT_ID")),
		fmt.Sprintf("ARM_CLIENT_SECRET=%s", os.Getenv("ARM_CLIENT_SECRET")),
		// Include azd in user agent
		fmt.Sprintf("TF_APPEND_USER_AGENT=%s", internal.UserAgent()),
	}

	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.HasTraceID() {
		envVars = append(envVars, fmt.Sprintf("ARM_CORRELATION_REQUEST_ID=%s", spanCtx.TraceID().String()))
	}

	t.cli.SetEnv(envVars)
	return nil
}

// EnsureEnv ensures that the environment is in a provision-ready state with required values set, prompting the user if
// values are unset.
//
// An environment is considered to be in a provision-ready state if it contains both an AZURE_SUBSCRIPTION_ID and
// AZURE_LOCATION value.
func (t *TerraformProvider) EnsureEnv(ctx context.Context) error {
	return EnsureSubscriptionAndLocation(
		ctx,
		t.envManager,
		t.env,
		t.prompters,
		nil,
	)
}

// Previews the infrastructure through terraform plan
func (t *TerraformProvider) plan(ctx context.Context) (*Deployment, *terraformDeploymentDetails, error) {
	isRemoteBackendConfig, err := t.isRemoteBackendConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("reading backend config: %w", err)
	}

	modulePath := t.modulePath()

	initRes, err := t.init(ctx, isRemoteBackendConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("terraform init failed: %s , err: %w", initRes, err)
	}

	if err != nil {
		return nil, nil, err
	}

	err = t.createInputParametersFile(ctx, t.parametersTemplateFilePath(), t.parametersFilePath())
	if err != nil {
		return nil, nil, fmt.Errorf("creating parameters file: %w", err)
	}

	validated, err := t.cli.Validate(ctx, modulePath)
	if err != nil {
		return nil, nil, fmt.Errorf("terraform validate failed: %s, err %w", validated, err)
	}

	planArgs := t.createPlanArgs(isRemoteBackendConfig)
	runResult, err := t.cli.Plan(ctx, modulePath, t.planFilePath(), planArgs...)
	if err != nil {
		return nil, nil, fmt.Errorf("terraform plan failed:%s err %w", runResult, err)
	}

	//create deployment plan
	deployment, err := t.createDeployment(ctx, modulePath)
	if err != nil {
		return nil, nil, fmt.Errorf("create terraform template failed: %w", err)
	}

	deploymentDetails := terraformDeploymentDetails{
		ParameterFilePath: t.parametersFilePath(),
		PlanFilePath:      t.planFilePath(),
	}
	if !isRemoteBackendConfig {
		deploymentDetails.localStateFilePath = t.localStateFilePath()
	}

	return deployment, &deploymentDetails, nil
}

// Deploy the infrastructure within the specified template through terraform apply
func (t *TerraformProvider) Deploy(ctx context.Context) (*DeployResult, error) {
	t.console.Message(ctx, "Locating plan file...")

	modulePath := t.modulePath()
	deployment, terraformDeploymentData, err := t.plan(ctx)
	if err != nil {
		return nil, err
	}

	isRemoteBackendConfig, err := t.isRemoteBackendConfig()
	if err != nil {
		return nil, fmt.Errorf("reading backend config: %w", err)
	}

	applyArgs, err := t.createApplyArgs(isRemoteBackendConfig, *terraformDeploymentData)
	if err != nil {
		return nil, err
	}

	runResult, err := t.cli.Apply(ctx, modulePath, applyArgs...)
	if err != nil {
		return nil, fmt.Errorf("template Deploy failed: %s , err:%w", runResult, err)
	}

	// Set the deployment result
	outputs, err := t.createOutputParameters(ctx, modulePath, isRemoteBackendConfig)
	if err != nil {
		return nil, fmt.Errorf("create terraform template failed: %w", err)
	}

	deployment.Outputs = outputs
	return &DeployResult{
		Deployment: deployment,
	}, nil
}

func (t *TerraformProvider) Preview(ctx context.Context) (*DeployPreviewResult, error) {
	// terraform uses plan() to display the what-if output
	// no changes are added to the properties
	_, _, err := t.plan(ctx)
	if err != nil {
		return nil, err
	}

	return &DeployPreviewResult{
		Preview: &DeploymentPreview{
			Status:     "done",
			Properties: &DeploymentPreviewProperties{},
		},
	}, nil
}

// Destroys the specified deployment through terraform destroy
func (t *TerraformProvider) Destroy(ctx context.Context, options DestroyOptions) (*DestroyResult, error) {
	isRemoteBackendConfig, err := t.isRemoteBackendConfig()
	if err != nil {
		return nil, fmt.Errorf("reading backend config: %w", err)
	}

	t.console.Message(ctx, "Locating parameters file...")
	err = t.ensureParametersFile(ctx)
	if err != nil {
		return nil, err
	}

	modulePath := t.modulePath()

	//load the deployment result
	outputs, err := t.createOutputParameters(ctx, modulePath, isRemoteBackendConfig)
	if err != nil {
		return nil, fmt.Errorf("load terraform template output failed: %w", err)
	}

	t.console.Message(ctx, "Deleting terraform deployment...")
	// terraform doesn't use the `t.console`, we must ensure no spinner is running before calling Destroy
	// as it could be an interactive operation if it needs confirmation
	t.console.StopSpinner(ctx, "", input.Step)
	destroyArgs := t.createDestroyArgs(isRemoteBackendConfig, options.Force())
	runResult, err := t.cli.Destroy(ctx, modulePath, destroyArgs...)
	if err != nil {
		return nil, fmt.Errorf("template Deploy failed: %s, err: %w", runResult, err)
	}

	return &DestroyResult{
		InvalidatedEnvKeys: maps.Keys(outputs),
	}, nil
}

func (t *TerraformProvider) State(ctx context.Context, options *StateOptions) (*StateResult, error) {
	isRemoteBackendConfig, err := t.isRemoteBackendConfig()
	if err != nil {
		return nil, fmt.Errorf("reading backend config: %w", err)
	}

	t.console.Message(ctx, "Retrieving terraform state...")
	modulePath := t.modulePath()

	terraformState, err := t.showCurrentState(ctx, modulePath, isRemoteBackendConfig)
	if err != nil {
		return nil, fmt.Errorf("fetching terraform state failed: %w", err)
	}

	state := State{}

	state.Outputs = t.convertOutputs(terraformState.Values.Outputs)
	state.Resources = t.collectAzureResources(terraformState.Values.RootModule)

	return &StateResult{
		State: &state,
	}, nil
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
	isRemoteBackendConfig bool, data terraformDeploymentDetails) ([]string, error) {
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
func (t *TerraformProvider) ensureParametersFile(ctx context.Context) error {
	if _, err := os.Stat(t.parametersFilePath()); err != nil {
		err := t.createInputParametersFile(ctx, t.parametersTemplateFilePath(), t.parametersFilePath())
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

		err := t.createInputParametersFile(ctx, t.backendConfigTemplateFilePath(), t.backendConfigFilePath())
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
	if err := t.ensureParametersFile(ctx); err != nil {
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
	return filepath.Join(t.projectPath, ".azure", t.env.Name(), t.options.Path, planFilename)
}

// Gets the path to the staging .azure terraform local state file path
func (t *TerraformProvider) localStateFilePath() string {
	return filepath.Join(t.projectPath, ".azure", t.env.Name(), t.options.Path, "terraform.tfstate")
}

// Gets the path to the staging .azure parameters file path
func (t *TerraformProvider) backendConfigFilePath() string {
	backendConfigFilename := fmt.Sprintf("%s.conf.json", t.env.Name())
	return filepath.Join(t.projectPath, ".azure", t.env.Name(), t.options.Path, backendConfigFilename)
}

// Gets the path to the staging .azure backend config file path
func (t *TerraformProvider) parametersFilePath() string {
	parametersFilename := fmt.Sprintf("%s.tfvars.json", t.options.Module)
	return filepath.Join(t.projectPath, ".azure", t.env.Name(), t.options.Path, parametersFilename)
}

// Gets the path to the current env.
func (t *TerraformProvider) dataDirPath() string {
	return filepath.Join(t.projectPath, ".azure", t.env.Name(), t.options.Path, ".terraform")
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

// Copies the an input parameters file templateFilePath to inputFilePath after replacing environment variable references in
// the contents.
func (t *TerraformProvider) createInputParametersFile(
	ctx context.Context,
	templateFilePath string,
	inputFilePath string,
) error {

	principalId, err := t.curPrincipal.CurrentPrincipalId(ctx)
	if err != nil {
		return fmt.Errorf("fetching current principal id: %w", err)
	}

	// Copy the parameter template file to the environment working directory and do substitutions.
	log.Printf("Reading parameters template file from: %s", templateFilePath)
	parametersBytes, err := os.ReadFile(templateFilePath)
	if err != nil {
		return fmt.Errorf("reading parameter file template: %w", err)
	}
	replaced, err := envsubst.Eval(string(parametersBytes), func(name string) string {
		if name == environment.PrincipalIdEnvVarName {
			return principalId
		}

		return t.env.Getenv(name)
	})

	if err != nil {
		return fmt.Errorf("substituting parameter file: %w", err)
	}

	writeDir := filepath.Dir(inputFilePath)
	if err := os.MkdirAll(writeDir, osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("creating directory structure: %w", err)
	}

	log.Printf("Writing parameters file to: %s", inputFilePath)
	err = os.WriteFile(inputFilePath, []byte(replaced), 0600)
	if err != nil {
		return fmt.Errorf("writing parameter file: %w", err)
	}

	return nil
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
