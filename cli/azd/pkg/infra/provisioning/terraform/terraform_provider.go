package terraform

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/terraform"
	"github.com/drone/envsubst"
)

// TerraformProvider exposes infrastructure provisioning using Azure Terraform templates
type TerraformProvider struct {
	env          *environment.Environment
	projectPath  string
	options      Options
	console      input.Console
	terraformCli terraform.TerraformCli
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
	return []tools.ExternalTool{t.terraformCli}
}

// NewTerraformProvider creates a new instance of a Terraform Infra provider
func NewTerraformProvider(ctx context.Context, env *environment.Environment, projectPath string, infraOptions Options) *TerraformProvider {
	terraformCli := terraform.GetTerraformCli(ctx)
	console := input.GetConsole(ctx)

	// Default to a module named "main" if not specified.
	if strings.TrimSpace(infraOptions.Module) == "" {
		infraOptions.Module = "main"
	}

	return &TerraformProvider{
		env:          env,
		projectPath:  projectPath,
		options:      infraOptions,
		console:      console,
		terraformCli: terraformCli,
	}
}

// Previews the infrastructure through terraform plan
func (t *TerraformProvider) Plan(ctx context.Context) *async.InteractiveTaskWithProgress[*DeploymentPlan, *DeploymentPlanningProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeploymentPlan, *DeploymentPlanningProgress]) {
			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: "Initialize terraform", Timestamp: time.Now()})

			os.Setenv("TF_DATA_DIR", t.dataDirPath())

			//check if local vs remote state file :
			isRemoteBackendConfig, err := t.isRemoteBackendConfig()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("reading backend config: %w", err))
				return
			}

			initRes, err := t.init(ctx, isRemoteBackendConfig)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("terraform init failed: %s , err: %w", initRes, err))
				return
			}

			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: "Generating terraform parameters", Timestamp: time.Now()})
			err = t.createParametersFile()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("creating parameters file: %w", err))
				return
			}

			modulePath := t.modulePath()
			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: "Validate terraform template", Timestamp: time.Now()})
			//validate the terraform template
			validated, err := t.terraformCli.Validate(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("terraform validate failed: %s, err %w", validated, err))
				return
			}
			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: fmt.Sprintf("terraform validate result : %s", validated), Timestamp: time.Now()})

			// discuss: -input=false arg force the cmd to fail in inputs for module variables were missing
			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: "Plan terraform template", Timestamp: time.Now()})
			parametersFilePath := t.parametersFilePath()
			planFilePath := t.planFilePath()

			cmd := []string{
				fmt.Sprintf("-var-file=%s", parametersFilePath)}

			if !isRemoteBackendConfig {
				cmd = append(cmd, fmt.Sprintf("-state=%s", t.localStateFilePath()))
			}

			runResult, err := t.terraformCli.Plan(ctx, modulePath, planFilePath, cmd...)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("terraform plan failed:%s err %w", runResult, err))
				return
			}

			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: fmt.Sprintf("terraform plan result : %s", runResult), Timestamp: time.Now()})

			//create deployment plan
			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: "Create terraform template", Timestamp: time.Now()})
			deployment, err := t.createDeployment(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("create terraform template failed: %w", err))
				return
			}

			deploymentDetails := TerraformDeploymentDetails{
				ParameterFilePath: parametersFilePath,
				PlanFilePath:      planFilePath,
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
			asyncContext.SetProgress(&DeployProgress{Message: "Locating plan file...", Timestamp: time.Now()})

			modulePath := t.modulePath()
			terraformDeploymentData := deployment.Details.(TerraformDeploymentDetails)
			isRemoteBackendConfig, err := t.isRemoteBackendConfig()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("reading backend config: %w", err))
				return
			}

			parametersFilePath := terraformDeploymentData.ParameterFilePath
			planFilePath := terraformDeploymentData.PlanFilePath

			var cmdArgs strings.Builder
			if _, err = os.Stat(planFilePath); err == nil {
				cmdArgs.WriteString(planFilePath)
				asyncContext.SetProgress(&DeployProgress{Message: "plan file found", Timestamp: time.Now()})
			} else {
				asyncContext.SetProgress(&DeployProgress{Message: "plan file not found, locating parameters file...", Timestamp: time.Now()})
				if _, err := os.Stat(parametersFilePath); err != nil {
					asyncContext.SetError(fmt.Errorf("parameters file not found:: %w", err))
					return
				}
				cmdArgs.WriteString(fmt.Sprintf("-var-file=%s", parametersFilePath))
			}

			//run the deploy cmd
			asyncContext.SetProgress(&DeployProgress{Message: "Deploy terraform template", Timestamp: time.Now()})
			cmd := []string{}

			if !isRemoteBackendConfig {
				cmd = append(cmd, fmt.Sprintf("-state=%s", terraformDeploymentData.localStateFilePath))
			}

			if cmdArgs.Len() > 0 {
				cmd = append(cmd, cmdArgs.String())
			}

			runResult, err := t.terraformCli.Apply(ctx, modulePath, cmd...)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("template Deploy failed: %s , err:%w", runResult, err))
				return
			}

			asyncContext.SetProgress(&DeployProgress{Message: fmt.Sprintf("terraform apply result: %s", runResult), Timestamp: time.Now()})

			//set the deployment result
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

			isRemoteBackendConfig, err := t.isRemoteBackendConfig()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("reading backend config: %w", err))
				return
			}

			initRes, err := t.init(ctx, isRemoteBackendConfig)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("terraform init failed: %s , err: %w", initRes, err))
				return
			}

			asyncContext.SetProgress(&DestroyProgress{Message: "locating parameters file", Timestamp: time.Now()})
			var cmdArgs strings.Builder
			parametersFilePath := t.parametersFilePath()
			modulePath := t.modulePath()

			if _, err := os.Stat(parametersFilePath); err != nil {
				asyncContext.SetProgress(&DestroyProgress{Message: "parameters file not found, creating parameters file...", Timestamp: time.Now()})
				err := t.createParametersFile()
				if err != nil {
					asyncContext.SetError(fmt.Errorf("creating parameters file: %w", err))
					return
				}
			}
			cmdArgs.WriteString(fmt.Sprintf("-var-file=%s", t.parametersFilePath()))

			//load the deployment result
			outputs, err := t.createOutputParameters(ctx, modulePath, isRemoteBackendConfig)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("load terraform template output failed: %w", err))
				return
			}

			asyncContext.SetProgress(&DestroyProgress{Message: "Destroy terraform deployment", Timestamp: time.Now()})
			cmd := []string{cmdArgs.String()}

			if !isRemoteBackendConfig {
				asyncContext.SetProgress(&DestroyProgress{Message: "Locating state file...", Timestamp: time.Now()})
				cmd = append(cmd, fmt.Sprintf("-state=%s", t.localStateFilePath()))
			}

			runResult, err := t.terraformCli.Destroy(ctx, modulePath, cmd...)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("template Deploy failed:%s , err :%w", runResult, err))
				return
			}
			asyncContext.SetProgress(&DestroyProgress{Message: fmt.Sprintf("Destroy terraform result:%s", runResult), Timestamp: time.Now()})
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
			asyncContext.SetProgress(&DeployProgress{Message: "Loading terraform module", Timestamp: time.Now()})

			isRemoteBackendConfig, err := t.isRemoteBackendConfig()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("reading backend config: %w", err))
				return
			}

			os.Setenv("TF_DATA_DIR", t.dataDirPath())
			initRes, err := t.init(ctx, isRemoteBackendConfig)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("terraform init failed: %s , err: %w", initRes, err))
				return
			}

			modulePath := t.modulePath()
			deployment, err := t.createDeployment(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("compiling terraform deployment: %w", err))
				return
			}

			asyncContext.SetProgress(&DeployProgress{Message: "Retrieving deployment output", Timestamp: time.Now()})
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

//initialize template terraform provider through terraform init
func (t *TerraformProvider) init(ctx context.Context, isRemoteBackendConfig bool) (string, error) {

	modulePath := t.modulePath()
	cmd := []string{}

	t.console.Message(ctx, "initialize terraform...")
	if isRemoteBackendConfig {
		t.console.Message(ctx, "Generating terraform backend config file")

		err := t.createBackendConfigFile()
		if err != nil {
			return fmt.Sprintf("creating terraform backend config file: %s", err), err
		}
		cmd = append(cmd, fmt.Sprintf("--backend-config=%s", t.backendConfigFilePath()))
	}

	runResult, err := t.terraformCli.Init(ctx, modulePath, cmd...)
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

	runResult, err := t.terraformCli.Output(ctx, modulePath, cmd...)
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
	if _, err := os.Stat(parametersFilePath); err != nil {
		err = t.createParametersFile()
		if err != nil {
			return nil, fmt.Errorf("creating parameters file: %w", err)
		}
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

// Copies the Terraform parameters file from the project template into the .azure environment folder
func (t *TerraformProvider) createParametersFile() error {
	// Copy the parameter template file to the environment working directory and do substitutions.
	parametersTemplateFilePath := t.parametersTemplateFilePath()

	log.Printf("Reading parameters template file from: %s", parametersTemplateFilePath)
	parametersBytes, err := os.ReadFile(parametersTemplateFilePath)
	if err != nil {
		return fmt.Errorf("reading parameter file template: %w", err)
	}
	replaced, err := envsubst.Eval(string(parametersBytes), func(name string) string {
		if val, has := t.env.Values[name]; has {
			return val
		}
		return os.Getenv(name)
	})

	if err != nil {
		return fmt.Errorf("substituting parameter file: %w", err)
	}

	parametersFilePath := t.parametersFilePath()
	writeDir := filepath.Dir(parametersFilePath)
	if err := os.MkdirAll(writeDir, osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("creating directory structure: %w", err)
	}

	log.Printf("Writing parameters file to: %s", parametersFilePath)
	err = os.WriteFile(parametersFilePath, []byte(replaced), 0644)
	if err != nil {
		return fmt.Errorf("writing parameter file: %w", err)
	}

	return nil
}

// Copies the Terraform backend file from the project template into the .azure environment folder
func (t *TerraformProvider) createBackendConfigFile() error {
	// Copy the backend config template file to the environment working directory and do substitutions.
	backendTemplateFilePath := t.backendConfigTemplateFilePath()

	log.Printf("Reading backend config template file from: %s", backendTemplateFilePath)
	backendBytes, err := os.ReadFile(backendTemplateFilePath)
	if err != nil {
		return fmt.Errorf("reading backend config file template: %w", err)
	}
	replaced, err := envsubst.Eval(string(backendBytes), func(name string) string {
		if val, has := t.env.Values[name]; has {
			return val
		}
		return os.Getenv(name)
	})

	if err != nil {
		return fmt.Errorf("substituting backend config file: %w", err)
	}

	backendConfigFilePath := t.backendConfigFilePath()
	writeDir := filepath.Dir(backendConfigFilePath)
	if err := os.MkdirAll(writeDir, osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("creating directory structure: %w", err)
	}

	log.Printf("Writing backend config file to: %s", backendConfigFilePath)
	err = os.WriteFile(backendConfigFilePath, []byte(replaced), 0644)
	if err != nil {
		return fmt.Errorf("writing backend config file: %w", err)
	}

	return nil
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

//Gets the path to the current env.
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
