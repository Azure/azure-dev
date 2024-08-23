package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	bicepCli "github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/fatih/color"

	"github.com/spf13/cobra"
)

func NewInfraModCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mod <name>",
		Short: "Modify an app component.",
	}
	cmd.Args = cobra.ExactArgs(1)
	return cmd
}

type ModifyAction struct {
	azdCtx     *azdcontext.AzdContext
	console    input.Console
	bicep      provisioning.Provider `container:"name"`
	args       []string
	env        *environment.Environment
	envManager environment.Manager
	im         *project.ImportManager
	bicepCli   *bicepCli.Cli
}

func NewInfraModAction(
	azdCtx *azdcontext.AzdContext,
	console input.Console,
	env *environment.Environment,
	envManager environment.Manager,
	im *project.ImportManager,
	bicepCli *bicepCli.Cli,
	args []string,
	ioc ioc.ServiceLocator,
) actions.Action {
	var bicep provisioning.Provider
	err := ioc.ResolveNamed("bicep", &bicep)
	if err != nil {
		log.Panicf("failed to resolve bicep: %v", err)
	}
	return &ModifyAction{
		azdCtx:     azdCtx,
		console:    console,
		args:       args,
		bicep:      bicep,
		env:        env,
		im:         im,
		envManager: envManager,
		bicepCli:   bicepCli,
	}
}

func (a *ModifyAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	prjConfig, err := project.Load(ctx, a.azdCtx.ProjectPath())
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}

	res, has := prjConfig.Resources[a.args[0]]
	if !has {
		return nil, fmt.Errorf("resource %s not found", a.args[0])
	}

	infraPathPrefix := project.DefaultPath
	if prjConfig.Infra.Path != "" {
		infraPathPrefix = prjConfig.Infra.Path
	}

	infraDirExists := false
	if _, err := os.Stat(filepath.Join(a.azdCtx.ProjectDirectory(), infraPathPrefix, "main.bicep")); err == nil {
		infraDirExists = true
	}

	synthFS, err := a.im.SynthAllInfrastructure(ctx, prjConfig)
	if err != nil {
		return nil, err
	}

	staging, err := os.MkdirTemp("", "infra-synth")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	err = fs.WalkDir(synthFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		contents, err := fs.ReadFile(synthFS, path)
		if err != nil {
			return err
		}

		err = os.MkdirAll(filepath.Join(staging, filepath.Dir(path)), osutil.PermissionDirectoryOwnerOnly)
		if err != nil {
			return err
		}

		return os.WriteFile(filepath.Join(staging, path), contents, d.Type().Perm())
	})
	if err != nil {
		return nil, err
	}

	err = a.bicepCli.Restore(ctx, filepath.Join(staging, infraPathPrefix, "main.bicep"))
	if err != nil {
		return nil, fmt.Errorf("restoring bicep: %w", err)
	}

	bicepModule, bicepVersion := res.DefaultModule()
	restorePath, err := bicepCli.ModuleRestoredPath("mcr.microsoft.com", bicepModule, bicepVersion)
	if err != nil {
		return nil, fmt.Errorf("getting module restored path: %w", err)
	}

	contents, err := os.ReadFile(filepath.Join(restorePath, "main.json"))
	if err != nil {
		return nil, fmt.Errorf("reading main.json: %w", err)
	}

	template := azure.ArmTemplate{}
	err = json.Unmarshal(contents, &template)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling template json: %w", err)
	}

	bicepProvider := a.bicep.(*bicep.BicepProvider)

	parameters := []string{}
	for key := range template.Parameters {
		// prototype code to skip parameters based on resource type.
		// we can't do this yet since we're not doing an in-memory merge
		switch res.Type {
		case project.ResourceTypeDbMongo:
			if key == "mongodbDatabases" || key == "secretsKeyVault" || key == "locations" || key == "location" {
				continue
			}
		case project.ResourceTypeDbRedis:
			if key == "location" {
				continue
			}
		case project.ResourceTypeDbPostgres:
			if key == "databases" || key == "firewallRules" || key == "skuName" || key == "tier" || key == "passwordAuth" ||
				key == "administratorLogin" || key == "administratorLoginPassword" ||
				key == "geoRedundantBackup" || key == "location" {
				continue
			}
		}
		parameters = append(parameters, key)
	}

	slices.Sort(parameters)

	type parameter struct {
		key   string
		param azure.ArmTemplateParameterDefinition
	}

	parameterDefinition := make(map[int]parameter)
	for i, key := range parameters {
		description, _ := template.Parameters[key].Description()
		parameters[i] = fmt.Sprintf("%s\t%s", key, description)
		parameterDefinition[i] = parameter{
			key:   key,
			param: template.Parameters[key],
		}
	}

	parameters, err = tabWrite(parameters, 3)
	if err != nil {
		return nil, fmt.Errorf("writing parameters: %w", err)
	}

	selection, err := a.console.Select(ctx, input.ConsoleOptions{
		Message: "What would you like to modify?",
		Options: parameters,
	})
	if err != nil {
		return nil, err
	}

	param := parameterDefinition[selection]

	var currentVal string
	var configuredVal json.RawMessage
	if ok, _ := a.env.Config.GetSection(configInfraParametersKey+res.Name+"."+param.key, &configuredVal); ok {
		currentValBytes, err := configuredVal.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("marshalling current value: %w", err)
		}

		currentVal = strings.ReplaceAll(string(currentValBytes), "\n", "")
	} else if param.param.DefaultValue != nil {
		currentValBytes, err := json.Marshal(param.param.DefaultValue)
		if err != nil {
			return nil, fmt.Errorf("marshalling default value: %w", err)
		}

		currentVal = strings.ReplaceAll(string(currentValBytes), "\n", "")
	}

	if len(currentVal) > 0 {
		currentVal = fmt.Sprintf("(Current value: %s)", currentVal)
	}

	val, err := bicepProvider.PromptForParameter(ctx, param.key, param.param, currentVal)
	if err != nil {
		return nil, fmt.Errorf("prompting for parameter: %w", err)
	}

	mustSetParamAsConfig(res.Name+"."+param.key, val, a.env.Config, param.param.Secure())

	if err := a.envManager.Save(ctx, a.env); err != nil {
		return nil, fmt.Errorf("saving parameter value: %w", err)
	}

	followUp := ""
	if infraDirExists {
		followUp = fmt.Sprintf(
			"The value for '%s' has been updated. You can now run '"+
				color.BlueString("azd infra synth")+"' to re-synthesize the infrastructure.",
			param.key)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   "Saved infrastructure configuration.",
			FollowUp: followUp,
		},
	}, err
}

// tabWrite transforms tabbed output into formatted strings with a given minimal padding.
// For more information, refer to the tabwriter package.
func tabWrite(selections []string, padding int) ([]string, error) {
	tabbed := strings.Builder{}
	tabW := tabwriter.NewWriter(&tabbed, 0, 0, padding, ' ', 0)
	_, err := tabW.Write([]byte(strings.Join(selections, "\n")))
	if err != nil {
		return nil, err
	}
	err = tabW.Flush()
	if err != nil {
		return nil, err
	}

	return strings.Split(tabbed.String(), "\n"), nil
}

var configInfraParametersKey = "infra.synthParameters."

// mustSetParamAsConfig sets the specified key-value pair in the given config.Config object.
// If the isSecured flag is set to true, the value is set as a secret using config.SetSecret,
// otherwise it is set using config.Set.
// If an error occurs while setting the value, the function panics with a warning message.
func mustSetParamAsConfig(key string, value any, config config.Config, isSecured bool) {
	configKey := configInfraParametersKey + key

	if !isSecured {
		if err := config.Set(configKey, value); err != nil {
			log.Panicf("failed setting config value: %v", err)
		}
		return
	}

	secretString, castOk := value.(string)
	if !castOk {
		log.Panic("tried to set a non-string as secret. This is not supported.")
	}
	if err := config.SetSecret(configKey, secretString); err != nil {
		log.Panicf("failed setting a secret in config: %v", err)
	}
}
