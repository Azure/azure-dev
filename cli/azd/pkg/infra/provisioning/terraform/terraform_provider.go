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
func NewTerraformProvider(ctx context.Context, env *environment.Environment, projectPath string, infraOptions Options) Provider {
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
			t.init(ctx, "-upgrade")

			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: "Generating terraform parameters", Timestamp: time.Now()})
			err := t.createParametersFile()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("creating parameters file: %w", err))
				return
			}

			modulePath := t.modulePath()
			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: "Validate terraform template", Timestamp: time.Now()})
			//validate the terraform template
			validated, err := t.terraformCli.Validate(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("failed to validate terraform template: %w", err))
				return
			}
			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: fmt.Sprintf("terraform validate result : %s", validated), Timestamp: time.Now()})

			// discuss: -input=false arg force the cmd to fail in inputs for module variables were missing
			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: "Plan terraform template", Timestamp: time.Now()})
			parametersFilePath := t.parametersFilePath()
			planFilePath := t.planFilePath()

			cmd := []string{
				fmt.Sprintf("-chdir=%s", modulePath), "plan",
				fmt.Sprintf("-var-file=%s", parametersFilePath),
				fmt.Sprintf("-out=%s", planFilePath),
				"-input=false", "-lock=false"}

			//check if local vs remote state file :
			isRemoteModule := t.isRemoteBackendConfig()
			if !isRemoteModule {
				cmd = append(cmd, fmt.Sprintf("-state=%s", t.localStateFilePath()))
			}

			runResult, err := t.terraformCli.RunCommand(ctx, cmd...)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("template preview failed: %w", err))
				return
			}

			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: fmt.Sprintf("terraform plan result : %s", runResult.Stdout), Timestamp: time.Now()})

			//create deployment plan
			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: "create terraform template", Timestamp: time.Now()})
			deployment, err := t.createDeployment(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("create terraform template failed: %w", err))
				return
			}

			deploymentDetails := TerraformDeploymentDetails{
				ParameterFilePath: parametersFilePath,
				PlanFilePath:      planFilePath,
			}
			if !isRemoteModule {
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

			parametersFilePath := terraformDeploymentData.ParameterFilePath
			planFilePath := terraformDeploymentData.PlanFilePath

			var cmdArgs strings.Builder
			if _, err := os.Stat(planFilePath); err == nil {
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
			cmd := []string{fmt.Sprintf("-chdir=%s", modulePath), "apply", "-lock=false", "-input=false", "-auto-approve"}
			if !t.isRemoteBackendConfig() {
				cmd = append(cmd, fmt.Sprintf("-state=%s", terraformDeploymentData.localStateFilePath))
			}

			if cmdArgs.Len() > 0 {
				cmd = append(cmd, cmdArgs.String())
			}

			runResult, err := t.terraformCli.RunCommand(ctx, cmd...)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("template Deploy failed: %s", runResult.Stderr))
				return
			}

			asyncContext.SetProgress(&DeployProgress{Message: fmt.Sprintf("terraform apply result: %s", runResult.Stdout), Timestamp: time.Now()})

			//set the deployment result
			outputs, err := t.createOutputParameters(ctx, modulePath)
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
			t.init(ctx, "-upgrade")

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
			outputs, err := t.createOutputParameters(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("load terraform template output failed: %w", err))
				return
			}

			asyncContext.SetProgress(&DestroyProgress{Message: "Destroy terraform deployment", Timestamp: time.Now()})
			cmd := []string{fmt.Sprintf("-chdir=%s", modulePath), "destroy", "-input=false", "-auto-approve", cmdArgs.String()}

			if !t.isRemoteBackendConfig() {
				asyncContext.SetProgress(&DestroyProgress{Message: "Locating state file...", Timestamp: time.Now()})
				cmd = append(cmd, fmt.Sprintf("-state=%s", t.localStateFilePath()))
			}

			runResult, err := t.terraformCli.RunCommand(ctx, cmd...)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("template Deploy failed: %w", err))
				return
			}
			asyncContext.SetProgress(&DestroyProgress{Message: fmt.Sprintf("Destroy terraform result:%s", runResult.Stdout), Timestamp: time.Now()})
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

			os.Setenv("TF_DATA_DIR", t.dataDirPath())
			t.init(ctx, "-upgrade")

			modulePath := t.modulePath()
			deployment, err := t.createDeployment(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("compiling terraform deployment: %w", err))
				return
			}

			asyncContext.SetProgress(&DeployProgress{Message: "Retrieving deployment output", Timestamp: time.Now()})
			outputs, err := t.createOutputParameters(ctx, modulePath)
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
func (t *TerraformProvider) init(ctx context.Context, initArgs ...string) error {

	modulePath := t.modulePath()
	cmd := []string{fmt.Sprintf("-chdir=%s", modulePath), "init", "-input=false"}

	t.console.Message(ctx, "initialize terraform...")
	if t.isRemoteBackendConfig() {
		t.console.Message(ctx, "Generating terraform backend config file")

		err := t.createBackendConfigFile()
		if err != nil {
			t.console.Message(ctx, fmt.Sprintf("creating terraform backend config file: %w", err))
			return err
		}
		cmd = append(cmd, fmt.Sprintf("--backend-config=%s", t.backendConfigFilePath()))
	}
	cmd = append(cmd, initArgs...)
	runResult, err := t.terraformCli.RunCommand(ctx, cmd...)
	if err != nil {
		t.console.Message(ctx, fmt.Sprintf("terraform init failed: %s", runResult.Stderr))
		return err
	}
	t.console.Message(ctx, runResult.Stdout)
	return nil
}

// Creates a normalized view of the terraform output.
func (t *TerraformProvider) createOutputParameters(ctx context.Context, modulePath string) (map[string]OutputParameter, error) {

	cmd := []string{
		fmt.Sprintf("-chdir=%s", modulePath),
		"output", "-json"}

	if !t.isRemoteBackendConfig() {
		cmd = append(cmd, fmt.Sprintf("-state=%s", t.localStateFilePath()))
	}

	runResult, err := t.terraformCli.RunCommand(ctx, cmd...)
	if err != nil {
		return nil, fmt.Errorf("reading deployment output failed: %s", runResult.Stderr)
	}

	var outputMap map[string]map[string]interface{}
	if err := json.Unmarshal([]byte(runResult.Stdout), &outputMap); err != nil {
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

	log.Printf("Reading parameters template file from: %s", parametersFilePath)
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
	if err := os.MkdirAll(writeDir, 0755); err != nil {
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
	if err := os.MkdirAll(writeDir, 0755); err != nil {
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
func (t *TerraformProvider) isRemoteBackendConfig() bool {
	modulePath := t.modulePath()
	infraDir, _ := os.Open(modulePath)
	files, err := infraDir.ReadDir(0)

	if err != nil {
		fmt.Println(fmt.Errorf("reading .tf files contents: %w", err))
	}

	for index := range files {
		if !files[index].IsDir() && filepath.Ext(files[index].Name()) == ".tf" {
			fileContent, err := os.ReadFile(filepath.Join(modulePath, files[index].Name()))

			if err != nil {
				fmt.Println(fmt.Errorf("error reading .tf files: %w", err))
			}

			if found := strings.Contains(string(fileContent), `backend "azurerm"`); found {
				return true
			}
		}
	}
	return false
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
