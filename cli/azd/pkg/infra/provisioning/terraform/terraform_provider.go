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
	azCli       azcli.AzCli
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
func NewTerraformProvider(ctx context.Context, env *environment.Environment, projectPath string, infraOptions Options) *TerraformProvider {
	terraformCli := terraform.GetTerraformCli(ctx)
	console := input.GetConsole(ctx)
	azCli := azcli.GetAzCli(ctx)

	// Default to a module named "main" if not specified.
	if strings.TrimSpace(infraOptions.Module) == "" {
		infraOptions.Module = "main"
	}

	return &TerraformProvider{
		env:         env,
		projectPath: projectPath,
		options:     infraOptions,
		console:     console,
		cli:         terraformCli,
		azCli:       azCli,
	}
}

// Previews the infrastructure through terraform plan
func (t *TerraformProvider) Plan(ctx context.Context) *async.InteractiveTaskWithProgress[*DeploymentPlan, *DeploymentPlanningProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeploymentPlan, *DeploymentPlanningProgress]) {
			os.Setenv("TF_DATA_DIR", t.dataDirPath())
			isRemoteBackendConfig, err := t.isRemoteBackendConfig()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("reading backend config: %w", err))
				return
			}

			modulePath := t.modulePath()

			t.console.Message(ctx, "Initializing terraform...")
			err = asyncContext.Interact(func() error {
				initRes, err := t.init(ctx, isRemoteBackendConfig)
				if err != nil {
					return fmt.Errorf("terraform init failed: %s , err: %w", initRes, err)
				}

				return nil
			})

			if err != nil {
				asyncContext.SetError(err)
				return
			}

			t.console.Message(ctx, "\nGenerating terraform parameters...")
			currentSubscription, err := t.ensureEnvSubscription(ctx)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("failed to set az subscription , err: %w", err))
				return
			}
			if currentSubscription != "" {
				defer t.setAZSubscription(ctx, currentSubscription)
			}

			err = CreateInputParametersFile(t.parametersTemplateFilePath(), t.parametersFilePath(), t.env.Values)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("creating parameters file: %w", err))
				return
			}

			t.console.Message(ctx, "Validating terraform template...")
			validated, err := t.cli.Validate(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("terraform validate failed: %s, err %w", validated, err))
				return
			}

			t.console.Message(ctx, "Generating terraform plan...\n")
			err = asyncContext.Interact(func() error {
				planArgs := t.createPlanArgs(isRemoteBackendConfig)
				runResult, err := t.cli.Plan(ctx, modulePath, t.planFilePath(), planArgs...)
				if err != nil {
					return fmt.Errorf("terraform plan failed:%s err %w", runResult, err)
				}

				return nil
			})

			if err != nil {
				asyncContext.SetError(err)
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
func (t *TerraformProvider) Deploy(ctx context.Context, deployment *DeploymentPlan, scope infra.Scope) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeployResult, *DeployProgress]) {
			t.console.Message(ctx, "Locating plan file...")
			currentSubscription, err := t.ensureEnvSubscription(ctx)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("failed to set az subscription , err: %w", err))
				return
			}
			if currentSubscription != "" {
				defer t.setAZSubscription(ctx, currentSubscription)
			}

			modulePath := t.modulePath()
			terraformDeploymentData := deployment.Details.(TerraformDeploymentDetails)
			isRemoteBackendConfig, err := t.isRemoteBackendConfig()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("reading backend config: %w", err))
				return
			}

			t.console.Message(ctx, "Deploying terraform template...")
			err = asyncContext.Interact(func() error {
				applyArgs, err := t.createApplyArgs(isRemoteBackendConfig, terraformDeploymentData)
				if err != nil {
					return err
				}

				runResult, err := t.cli.Apply(ctx, modulePath, applyArgs...)
				if err != nil {
					return fmt.Errorf("template Deploy failed: %s , err:%w", runResult, err)
				}

				return nil
			})

			if err != nil {
				asyncContext.SetError(err)
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
func (t *TerraformProvider) Destroy(ctx context.Context, deployment *Deployment, options DestroyOptions) *async.InteractiveTaskWithProgress[*DestroyResult, *DestroyProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress]) {
			// discuss : check if you want to set auto-destroy . CAN WE PASS VALUES BACK AND FORTH BETWEEN THE AZD CONTEXT AND PROCESS
			os.Setenv("TF_DATA_DIR", t.dataDirPath())

			currentSubscription, err := t.ensureEnvSubscription(ctx)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("failed to set az subscription , err: %w", err))
				return
			}
			if currentSubscription != "" {
				defer t.setAZSubscription(ctx, currentSubscription)
			}

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

// Gets the latest deployment details
func (t *TerraformProvider) GetDeployment(ctx context.Context, scope infra.Scope) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeployResult, *DeployProgress]) {
			os.Setenv("TF_DATA_DIR", t.dataDirPath())
			t.console.Message(ctx, "Loading terraform module...")

			isRemoteBackendConfig, err := t.isRemoteBackendConfig()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("reading backend config: %w", err))
				return
			}

			err = asyncContext.Interact(func() error {
				initRes, err := t.init(ctx, isRemoteBackendConfig)
				if err != nil {
					return fmt.Errorf("terraform init failed: %s , err: %w", initRes, err)
				}

				return nil
			})

			if err != nil {
				asyncContext.SetError(err)
				return
			}

			t.console.Message(ctx, "Retrieving deployment output...")
			modulePath := t.modulePath()
			deployment, err := t.createDeployment(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("compiling terraform deployment: %w", err))
				return
			}

			outputs, err := t.createOutputParameters(ctx, modulePath, isRemoteBackendConfig)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("create terraform output failed: %w", err))
				return
			}

			deployment.Outputs = outputs
			result := &DeployResult{
				Deployment: deployment,
			}

			asyncContext.SetResult(result)
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
func (t *TerraformProvider) createApplyArgs(isRemoteBackendConfig bool, data TerraformDeploymentDetails) ([]string, error) {
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
func (t *TerraformProvider) createOutputParameters(ctx context.Context, modulePath string, isRemoteBackend bool) (map[string]OutputParameter, error) {
	cmd := []string{}

	if !isRemoteBackend {
		cmd = append(cmd, fmt.Sprintf("-state=%s", t.localStateFilePath()))
	}

	runResult, err := t.cli.Output(ctx, modulePath, cmd...)
	if err != nil {
		return nil, fmt.Errorf("reading deployment output failed: %s, err:%w", runResult, err)
	}

	var outputMap map[string]map[string]interface{}
	if err := json.Unmarshal([]byte(runResult), &outputMap); err != nil {
		return nil, err
	}

	outputParameters := make(map[string]OutputParameter)
	for k, v := range outputMap {
		outputParameters[k] = OutputParameter{
			Type:  fmt.Sprint(v["type"]),
			Value: v["value"],
		}
	}
	return outputParameters, nil
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

// Registers the Terraform provider with the provisioning module
func Register() {
	err := RegisterProvider(Terraform, func(ctx context.Context, env *environment.Environment, projectPath string, options Options) (Provider, error) {
		return NewTerraformProvider(ctx, env, projectPath, options), nil
	})

	if err != nil {
		panic(err)
	}
}

func (t *TerraformProvider) ensureEnvSubscription(ctx context.Context) (string, error) {
	envSubscriptionId := t.env.GetSubscriptionId()
	currentSubscriptionId, err := t.azCli.GetCurrentSubscriptionId(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get current subscription Id. err :%w", err)
	}
	if currentSubscriptionId == envSubscriptionId {
		return "", nil
	}

	err = t.setAZSubscription(ctx, envSubscriptionId)
	if err != nil {
		return "", fmt.Errorf("failed to set current subscription Id. err :%w", err)
	}
	return currentSubscriptionId, nil
}

func (t *TerraformProvider) setAZSubscription(ctx context.Context, subscriptionId string) error {
	//set the subscription Id
	err := t.azCli.SetCurrentSubscriptionId(ctx, subscriptionId)
	if err != nil {
		return fmt.Errorf("failed to set subscription Id. err :%w", err)
	}
	return nil
}
