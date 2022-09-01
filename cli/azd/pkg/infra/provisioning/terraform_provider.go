package provisioning

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

// Name gets the name of the infra provider
func (p *TerraformProvider) Name() string {
	return "Terraform"
}

func (p *TerraformProvider) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{p.terraformCli}
}

// NewTerraformProvider creates a new instance of a Terraform Infra provider
// infraOptions Options
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
func (t *TerraformProvider) Preview(ctx context.Context) *async.InteractiveTaskWithProgress[*PreviewResult, *PreviewProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*PreviewResult, *PreviewProgress]) {
			asyncContext.SetProgress(&PreviewProgress{Message: "Initialize terraform", Timestamp: time.Now()})

			os.Setenv("TF_DATA_DIR", t.dataDirPath()) //t.envFilePath())
			//t.Init(ctx, "-upgrade").Await()
			t.init(ctx, "-upgrade")
			asyncContext.SetProgress(&PreviewProgress{Message: "Generating terraform parameters", Timestamp: time.Now()})
			err := t.createParametersFile()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("creating parameters file: %w", err))
				return
			}

			modulePath := t.modulePath()
			asyncContext.SetProgress(&PreviewProgress{Message: "Validate terraform template", Timestamp: time.Now()})
			//validate the terraform template
			validated, err := t.terraformCli.Validate(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("failed to validate terraform template: %w", err))
				return
			}
			asyncContext.SetProgress(&PreviewProgress{Message: fmt.Sprintf("terraform validate result : %s", validated), Timestamp: time.Now()})

			// discuss: -input=false arg force the cmd to fail in inputs for module variables were missing
			asyncContext.SetProgress(&PreviewProgress{Message: "Plan terraform template", Timestamp: time.Now()})
			// todo : explore moving -chdir= and -input=false to be a part of the RumCommand function
			runResult, err := t.terraformCli.RunCommand(ctx, fmt.Sprintf("-chdir=%s", modulePath),
				"plan", fmt.Sprintf("-state=%s", t.localStateFilePath()), fmt.Sprintf("-var-file=%s", t.parametersFilePath()), fmt.Sprintf("-out=%s", t.planFilePath()), "-input=false", "-lock=false")
			if err != nil {
				asyncContext.SetError(fmt.Errorf("template preview failed: %w", err))
				return
			}

			asyncContext.SetProgress(&PreviewProgress{Message: fmt.Sprintf("terraform plan result : %s", runResult.Stdout), Timestamp: time.Now()})

			//create deployment object to respect the function signature in the contract
			asyncContext.SetProgress(&PreviewProgress{Message: "create terraform template", Timestamp: time.Now()})
			deployment, err := t.createDeployment(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("create terraform template failed: %w", err))
				return
			}

			result := PreviewResult{
				Deployment: *deployment,
			}
			asyncContext.SetResult(&result)
		})
}

// Provisioning the infrastructure within the specified template
func (t *TerraformProvider) Deploy(ctx context.Context, deployment *Deployment, scope Scope) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeployResult, *DeployProgress]) {
			asyncContext.SetProgress(&DeployProgress{Message: "Locating plan file...", Timestamp: time.Now()})

			modulePath := t.modulePath()
			parametersFilePath := t.parametersFilePath()
			planFilePath := t.planFilePath()

			var stateArgs strings.Builder
			//check if local vs remote state file :
			if !t.isRemoteBackendConfig() {
				stateArgs.WriteString(fmt.Sprintf("-state=%s", t.localStateFilePath()))
			}
			var cmdArgs strings.Builder
			if _, err := os.Stat(planFilePath); err == nil {
				cmdArgs.WriteString(t.planFilePath())
				asyncContext.SetProgress(&DeployProgress{Message: "plan file found", Timestamp: time.Now()})
			} else {
				asyncContext.SetProgress(&DeployProgress{Message: "plan file not found, locating parameters file...", Timestamp: time.Now()})
				if _, err := os.Stat(parametersFilePath); err != nil {
					asyncContext.SetProgress(&DeployProgress{Message: "parameters file not found, creating parameters file...", Timestamp: time.Now()})
					err := t.createParametersFile()
					if err != nil {
						asyncContext.SetError(fmt.Errorf("creating parameters file: %w", err))
						return
					}
				}
				//use parameter file content
				cmdArgs.WriteString(fmt.Sprintf("-var-file=%s", t.parametersFilePath()))
			}

			//run the deploy  pass variable to write to a file. and set progress for the file link
			asyncContext.SetProgress(&DeployProgress{Message: "Deploy terraform template", Timestamp: time.Now()})
			// todo : explore moving -chdir= and -input=false to be a part of the RumCommand function
			runResult, err := t.terraformCli.RunCommand(ctx, fmt.Sprintf("-chdir=%s", modulePath),
				"apply", "-lock=false", "-input=false", "-auto-approve", stateArgs.String(), cmdArgs.String())
			if err != nil {
				asyncContext.SetError(fmt.Errorf("template Deploy failed: %s", runResult.Stderr)) //err))
				return
			}

			asyncContext.SetProgress(&DeployProgress{Message: fmt.Sprintf("terraform apply result: %s", runResult.Stdout), Timestamp: time.Now()})

			//set the deployment result
			outputs, err := t.createOutputParameters(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("create terraform template failed: %w", err))
				return
			}

			deployment.Outputs = outputs
			result := &DeployResult{
				Deployment: deployment,
			}
			asyncContext.SetResult(result)
		})
}

func (t *TerraformProvider) Destroy(ctx context.Context, deployment *Deployment, options DestroyOptions) *async.InteractiveTaskWithProgress[*DestroyResult, *DestroyProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress]) {
			// discuss : check if you want to set auto-destroy . CAN WE PASS VALUES BACK AND FORTH BETWEEN THE AZD CONTEXT AND PROCESS
			// agreed on adding a prompt for confirmation, but keep the auto-approve
			// keep the auto approve for now

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

			var stateArgs strings.Builder
			if !t.isRemoteBackendConfig() {
				asyncContext.SetProgress(&DestroyProgress{Message: "Locating state file...", Timestamp: time.Now()})
				stateArgs.WriteString(fmt.Sprintf("-state=%s", t.localStateFilePath()))
			}

			//load the deployment result
			outputs, err := t.createOutputParameters(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("load terraform template output failed: %w", err))
				return
			}

			asyncContext.SetProgress(&DestroyProgress{Message: "Destroy terraform deployment", Timestamp: time.Now()})
			// todo : explore moving -chdir= and -input=false to be a part of the RumCommand function

			runResult, err := t.terraformCli.RunCommand(ctx, fmt.Sprintf("-chdir=%s", modulePath),
				"destroy", cmdArgs.String(), stateArgs.String(), "-input=false", "-auto-approve")
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

// Gets the latest deployment details for the specified scope
func (t *TerraformProvider) GetDeployment(ctx context.Context, scope Scope) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeployResult, *DeployProgress]) {
			asyncContext.SetProgress(&DeployProgress{Message: "Loading terraform module", Timestamp: time.Now()})
			modulePath := t.modulePath()
			deployment, err := t.createDeployment(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("compiling terraform template: %w", err))
				return
			}

			asyncContext.SetProgress(&DeployProgress{Message: "Retrieving deployment output", Timestamp: time.Now()})
			outputs, err := t.createOutputParameters(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("create terraform template failed: %w", err))
				return
			}

			deployment.Outputs = outputs
			result := &DeployResult{
				Deployment: deployment,
			}
			asyncContext.SetResult(result)
		})
}

//initialize terraform
func (t *TerraformProvider) init(ctx context.Context, initArgs ...string) {
	modulePath := t.modulePath()
	//Run terraform init.
	t.console.Message(ctx, "initialize terraform...")
	runResult, err := t.terraformCli.RunCommand(ctx, fmt.Sprintf("-chdir=%s", modulePath), "init", "-input=false", strings.Join(initArgs, " "))
	if err != nil {
		t.console.Message(ctx, fmt.Sprintf("template terraform init failed: %s", runResult.Stderr))
		return
	}
	t.console.Message(ctx, runResult.Stdout)
}

// Creates a normalized view of the terraform output.
func (t *TerraformProvider) createOutputParameters(ctx context.Context, modulePath string) (map[string]OutputParameter, error) {

	//Run terraform show
	var cmdArgs strings.Builder
	if !t.isRemoteBackendConfig() {
		cmdArgs.WriteString(fmt.Sprintf("-state=%s", t.localStateFilePath()))
	}

	runResult, err := t.terraformCli.RunCommand(ctx, fmt.Sprintf("-chdir=%s", modulePath),
		"output", cmdArgs.String(), "-json")
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

// Copies the Bicep parameters file from the project template into the .azure environment folder
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

// Gets the path to the project parameters file path
func (t *TerraformProvider) parametersTemplateFilePath() string {
	infraPath := t.options.Path
	if strings.TrimSpace(infraPath) == "" {
		infraPath = "infra"
	}

	parametersFilename := fmt.Sprintf("%s.tfvars.json", t.options.Module)
	return filepath.Join(t.projectPath, infraPath, parametersFilename)
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
	path := t.modulePath()

	infraDir, _ := os.Open(path)
	files, err := infraDir.ReadDir(0)

	if err != nil {
		fmt.Println(fmt.Errorf("reading .tf files contents: %w", err))
	}
	for index := range files {
		if !files[index].IsDir() && filepath.Ext(files[index].Name()) == ".tf" {
			fileContent, err := os.ReadFile(filepath.Join(path, files[index].Name()))

			if err != nil {
				fmt.Println(fmt.Errorf("error reading .tf files: %w", err))
			}

			return strings.Contains(string(fileContent), `backend "azurerm"`)
		}
	}
	return false
}
