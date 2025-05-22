package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/drone/envsubst"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	_ azdext.ProvisioningProvider = &ScriptProvider{}
)

type Config struct {
	Provision []*ProvisionScript `json:"provision"`
	Destroy   []*ProvisionScript `json:"destroy"`
}

type InputParameter struct {
	Type         string `json:"type"`
	Name         string `json:"name,omitempty"`
	Value        any    `json:"value,omitempty"`
	DefaultValue any    `json:"defaultValue,omitempty"`
}

type ParametersFile struct {
	Parameters map[string]*InputParameter `json:"parameters"`
}

type OutputsFile struct {
	Outputs map[string]*azdext.ProvisioningOutputParameter `json:"outputs"`
}

type ProvisionScript struct {
	ShellType      string                     `json:"shell"`
	ScriptPath     string                     `json:"run"`
	ParametersPath string                     `json:"parameters,omitempty"`
	Parameters     map[string]*InputParameter `json:"-"`
}

type ScriptProvider struct {
	azdClient   *azdext.AzdClient
	projectPath string
	options     *azdext.ProvisioningOptions
	config      *Config
	env         map[string]string
}

func NewScriptProvider(azdClient *azdext.AzdClient) azdext.ProvisioningProvider {
	return &ScriptProvider{
		azdClient: azdClient,
		env:       make(map[string]string),
	}
}

func (s *ScriptProvider) Name(ctx context.Context) (string, error) {
	fmt.Println("ScriptProvider.Name called")
	return "script", nil
}

func (s *ScriptProvider) Initialize(ctx context.Context, projectPath string, options *azdext.ProvisioningOptions) error {
	s.projectPath = projectPath
	s.options = options

	// s.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
	// 	Options: &azdext.ConfirmOptions{
	// 		Message:      "Ready to debug",
	// 		DefaultValue: ux.Ptr(true),
	// 	},
	// })

	currentEnvResponse, err := s.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}

	envValuesResponse, err := s.azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: currentEnvResponse.Environment.Name,
	})
	for _, envPair := range envValuesResponse.KeyValues {
		s.env[envPair.Key] = envPair.Value
	}

	if err != nil {
		return fmt.Errorf("failed to get environment values: %w", err)
	}

	configJson, err := protojson.Marshal(options.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := json.Unmarshal(configJson, &s.config); err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if len(s.config.Provision) == 0 {
		return fmt.Errorf("no scripts defined in config")
	}

	// Validate scripts
	for _, script := range s.config.Provision {
		if script.ShellType == "" {
			return fmt.Errorf("script shell type is required")
		}

		if script.ScriptPath == "" {
			return fmt.Errorf("script path is required")
		}

		fullScriptPath := filepath.Join(projectPath, script.ScriptPath)
		if _, err := os.Stat(fullScriptPath); os.IsNotExist(err) {
			return fmt.Errorf("script file does not exist: %s", fullScriptPath)
		}

		if script.ParametersPath != "" {
			fullParametersPath := filepath.Join(projectPath, script.ParametersPath)
			if _, err := os.Stat(fullParametersPath); os.IsNotExist(err) {
				return fmt.Errorf("parameters file does not exist: %s", fullParametersPath)
			}
		}
	}

	return s.EnsureEnv(ctx)
}

func (s *ScriptProvider) EnsureEnv(ctx context.Context) error {
	for _, script := range s.config.Provision {
		if script.ParametersPath == "" {
			continue
		}

		fullParametersPath := filepath.Join(s.projectPath, script.ParametersPath)
		file, err := os.Open(fullParametersPath)
		if err != nil {
			return fmt.Errorf("failed to open parameters file: %w", err)
		}
		defer file.Close()

		var paramsFile ParametersFile
		if err := json.NewDecoder(file).Decode(&paramsFile); err != nil {
			return fmt.Errorf("failed to decode parameters file: %w", err)
		}

		for name, param := range paramsFile.Parameters {
			resolved := param
			resolved.Name = name

			// Interpolate environment variables using envsubst if value is a string
			if strVal, ok := resolved.Value.(string); ok {
				if interpolated, err := envsubst.Eval(strVal, func(name string) string {
					if v, ok := s.env[name]; ok && v != "" {
						return v
					}

					return os.Getenv(name)
				}); err == nil {
					resolved.Value = interpolated
				}
			}

			// If value is still empty, use defaultValue if present
			if (resolved.Value == nil || resolved.Value == "") && resolved.DefaultValue != nil && resolved.DefaultValue != "" {
				resolved.Value = resolved.DefaultValue
			}

			// If still empty, prompt the user
			if resolved.Value == nil || resolved.Value == "" {
				if name == "AZURE_LOCATION" {
					subscriptionId := s.env["AZURE_SUBSCRIPTION_ID"]
					locationResp, err := s.azdClient.Prompt().PromptLocation(ctx, &azdext.PromptLocationRequest{
						AzureContext: &azdext.AzureContext{
							Scope: &azdext.AzureScope{
								SubscriptionId: subscriptionId,
							},
						},
					})
					if err != nil {
						return fmt.Errorf("failed to prompt for Azure location: %w", err)
					}
					userVal := locationResp.Location.Name
					// Validate/cast userVal to expected type
					switch resolved.Type {
					case "string":
						resolved.Value = userVal
					default:
						return fmt.Errorf("AZURE_LOCATION parameter must be of type string")
					}
				} else {
					promptMsg := fmt.Sprintf("Enter value for parameter '%s'", name)
					promptOptions := &azdext.PromptOptions{
						Message:  promptMsg,
						Required: true,
					}
					resp, err := s.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
						Options: promptOptions,
					})
					if err != nil {
						return fmt.Errorf("failed to prompt for parameter '%s': %w", name, err)
					}
					userVal := resp.Value

					// Validate/cast userVal to expected type
					switch resolved.Type {
					case "string":
						resolved.Value = userVal
					case "number":
						if _, err := strconv.ParseFloat(userVal, 64); err != nil {
							return fmt.Errorf("parameter '%s' expects a number, got '%s'", name, userVal)
						}
						resolved.Value = userVal
					case "integer":
						if _, err := strconv.ParseInt(userVal, 10, 64); err != nil {
							return fmt.Errorf("parameter '%s' expects an integer, got '%s'", name, userVal)
						}
						resolved.Value = userVal
					case "boolean":
						if _, err := strconv.ParseBool(userVal); err != nil {
							return fmt.Errorf("parameter '%s' expects a boolean, got '%s'", name, userVal)
						}
						resolved.Value = userVal
					default:
						return fmt.Errorf("parameter '%s' has unsupported type '%s'", name, resolved.Type)
					}
				}
			}

			if script.Parameters == nil {
				script.Parameters = make(map[string]*InputParameter)
			}

			script.Parameters[name] = resolved
		}
	}

	return nil
}

func (s *ScriptProvider) State(ctx context.Context, options *azdext.ProvisioningStateOptions) (*azdext.ProvisioningStateResult, error) {
	fmt.Println("ScriptProvider.State called")
	return &azdext.ProvisioningStateResult{}, nil
}

func (s *ScriptProvider) Deploy(ctx context.Context) (*azdext.ProvisioningDeployResult, error) {
	if err := s.runScripts(ctx, s.config.Provision); err != nil {
		return nil, err
	}

	outputs, err := findAndLoadOutputs(s.projectPath, s.config.Provision)
	if err != nil {
		return nil, err
	}

	// Populate Parameters from all resolved script parameters
	parameters := parametersToProvisioningInputs(s.config.Provision)

	return &azdext.ProvisioningDeployResult{
		Deployment: &azdext.ProvisioningDeployment{
			Parameters: parameters,
			Outputs:    outputs,
		},
	}, nil
}

func (s *ScriptProvider) Preview(ctx context.Context) (*azdext.ProvisioningDeployPreviewResult, error) {
	fmt.Println("ScriptProvider.Preview called")
	return &azdext.ProvisioningDeployPreviewResult{}, nil
}

func (s *ScriptProvider) Destroy(ctx context.Context, options *azdext.ProvisioningDestroyOptions) (*azdext.ProvisioningDestroyResult, error) {
	// If force is not set, prompt the user for confirmation
	if options == nil || !options.Force {
		defaultVal := false
		confirmResp, err := s.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      "Are you sure you want to destroy the resources?",
				DefaultValue: &defaultVal,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to prompt for destroy confirmation: %w", err)
		}
		if confirmResp.Value == nil || !*confirmResp.Value {
			return nil, errors.New("destroy operation cancelled by user")
		}
	}

	if err := s.runScripts(ctx, s.config.Destroy); err != nil {
		return nil, err
	}

	return &azdext.ProvisioningDestroyResult{}, nil
}

func (s *ScriptProvider) Parameters(ctx context.Context) ([]*azdext.ProvisioningParameter, error) {
	parameters := []*azdext.ProvisioningParameter{}

	parameters = append(parameters, &azdext.ProvisioningParameter{
		Name:        "test",
		Value:       "",
		LocalPrompt: true,
	})

	fmt.Println("ScriptProvider.Parameters called")
	return parameters, nil
}

// findOutputsJsonPath searches for outputs.json from the given startDir up to the projectDir.
func findOutputsJsonPath(startDir, projectDir string) (string, error) {
	currentDir := startDir
	for {
		candidate := filepath.Join(currentDir, "outputs.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		if currentDir == projectDir {
			break
		}
		parent := filepath.Dir(currentDir)
		if parent == currentDir {
			break
		}
		currentDir = parent
	}
	return "", fmt.Errorf("outputs.json not found from %s up to %s", startDir, projectDir)
}

// Helper to find and load outputs.json as map[string]*azdext.ProvisioningOutputParameter
func findAndLoadOutputs(projectPath string, scripts []*ProvisionScript) (map[string]*azdext.ProvisioningOutputParameter, error) {
	outputs := make(map[string]*azdext.ProvisioningOutputParameter)
	if len(scripts) == 0 {
		return outputs, nil
	}
	lastScript := scripts[len(scripts)-1]
	scriptDir := filepath.Dir(filepath.Join(projectPath, lastScript.ScriptPath))
	outputsPath, err := findOutputsJsonPath(scriptDir, projectPath)
	if err != nil {
		return outputs, nil // not found is not an error
	}
	data, err := os.ReadFile(outputsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read outputs.json: %w", err)
	}
	var outputsFile *OutputsFile
	if err := json.Unmarshal(data, &outputsFile); err != nil {
		return nil, fmt.Errorf("failed to decode outputs.json: %w", err)
	}
	if outputsFile != nil && outputsFile.Outputs != nil {
		outputs = outputsFile.Outputs
	}
	return outputs, nil
}

// runScripts executes the given scripts with parameter and environment handling
func (s *ScriptProvider) runScripts(ctx context.Context, scripts []*ProvisionScript) error {
	for _, script := range scripts {
		fullScriptPath := filepath.Join(s.projectPath, script.ScriptPath)

		// Prepare environment variables for parameters
		env := os.Environ()
		// Always append/override with s.env values (azd env trumps os env)
		for k, v := range s.env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		for _, param := range script.Parameters {
			name := param.Name
			val := ""
			if param.Value != nil {
				switch v := param.Value.(type) {
				case string:
					val = v
				default:
					val = fmt.Sprintf("%v", v)
				}
			}
			env = append(env, fmt.Sprintf("%s=%s", name, val))
		}

		var cmd *exec.Cmd
		switch script.ShellType {
		case "bash", "sh":
			cmd = exec.CommandContext(ctx, "bash", fullScriptPath)
		case "pwsh", "powershell":
			cmd = exec.CommandContext(ctx, "pwsh", "-File", fullScriptPath)
		default:
			return fmt.Errorf("unsupported shell type: %s", script.ShellType)
		}
		cmd.Env = env
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to execute script '%s': %w", script.ScriptPath, err)
		}
	}
	return nil
}

// Converts any value to a string for output/parameter serialization
func toStringValue(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Converts script parameters to map[string]*azdext.ProvisioningInputParameter
func parametersToProvisioningInputs(scripts []*ProvisionScript) map[string]*azdext.ProvisioningInputParameter {
	result := make(map[string]*azdext.ProvisioningInputParameter)
	for _, script := range scripts {
		for name, param := range script.Parameters {
			result[name] = &azdext.ProvisioningInputParameter{Value: toStringValue(param.Value)}
		}
	}
	return result
}
