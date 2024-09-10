package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	armruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/repository"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/braydonk/yaml"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func NewInfraAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add",
		Short: "Add a component to your app.",
	}
}

type AddAction struct {
	azdCtx           *azdcontext.AzdContext
	env              *environment.Environment
	envManager       environment.Manager
	creds            account.SubscriptionCredentialProvider
	rm               infra.ResourceManager
	appInit          *repository.Initializer
	armClientOptions *arm.ClientOptions
	prompter         prompt.Prompter
	console          input.Console
}

func (a *AddAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	prjConfig, err := project.Load(ctx, a.azdCtx.ProjectPath())
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}

	infraPathPrefix := project.DefaultPath
	if prjConfig.Infra.Path != "" {
		infraPathPrefix = prjConfig.Infra.Path
	}

	infraDirExists := false
	if _, err := os.Stat(filepath.Join(a.azdCtx.ProjectDirectory(), infraPathPrefix, "main.bicep")); err == nil {
		infraDirExists = true
	}

	const localService = "Local service"
	resources := project.AllCategories()
	displayOptions := []string{localService}
	for category := range resources {
		displayOptions = append(displayOptions, string(category))
	}
	slices.Sort(displayOptions)

	continueOption, err := a.console.Select(ctx, input.ConsoleOptions{
		Message: "What would you like to add?",
		Options: displayOptions,
	})
	if err != nil {
		return nil, err
	}

	resourceToAdd := &project.ResourceConfig{}
	var serviceToAdd *project.ServiceConfig

	var selectedCategory project.ResourceKind
	if displayOptions[continueOption] == localService {
		// local services are kinda like hosts -- except for the hosting part
		selectedCategory = project.ResourceKindHosts
	} else {
		selectedCategory = project.ResourceKind(displayOptions[continueOption])
	}

	// Get the resource types for the selected category
	resourceTypes := resources[selectedCategory]
	resourceTypesDisplay := make([]string, 0, len(resourceTypes))
	resourceTypesDisplayMap := make(map[string]project.ResourceType)
	for _, resourceType := range resourceTypes {
		resourceTypesDisplay = append(resourceTypesDisplay, resourceType.String())
		resourceTypesDisplayMap[resourceType.String()] = resourceType
	}
	slices.Sort(resourceTypesDisplay)

	switch selectedCategory {
	case project.ResourceKindHosts:
		if displayOptions[continueOption] == localService {
			prj, err := a.addLocalProject(ctx)
			if err != nil {
				return nil, err
			}

			svcSpec, err := a.projectAsService(ctx, prj)
			if err != nil {
				return nil, err
			}

			serviceToAdd = svcSpec
		} else if len(prjConfig.Services) == 0 {
			a.console.MessageUxItem(ctx, &ux.WarningMessage{
				Description: fmt.Sprintf("No services found in %s.", output.WithHighLightFormat("azure.yaml")),
				HidePrefix:  true,
			})
			confirm, err := a.console.Confirm(ctx, input.ConsoleOptions{
				Message:      "Would you like to first add a local project as a service?",
				DefaultValue: true,
			})
			if err != nil || !confirm {
				return nil, err
			}

			prj, err := a.addLocalProject(ctx)
			if err != nil {
				return nil, err
			}

			svcSpec, err := a.projectAsService(ctx, prj)
			if err != nil {
				return nil, err
			}

			confirm, err = a.console.Confirm(ctx, input.ConsoleOptions{
				//nolint:lll
				Message:      "azd will use " + color.MagentaString("Azure Container App") + " to host this project. Continue?",
				DefaultValue: true,
			})
			if err != nil || !confirm {
				return nil, err
			}

			resSpec, err := addServiceAsResource(ctx, a.console, svcSpec, project.ResourceTypeHostContainerApp)
			if err != nil {
				return nil, err
			}

			serviceToAdd = svcSpec
			resourceToAdd = resSpec
		} else {
			serviceOptions := make([]string, 0, len(prjConfig.Services))
			for _, service := range prjConfig.Services {
				serviceOptions = append(serviceOptions, service.Name)
			}
			slices.Sort(serviceOptions)

			serviceOption, err := a.console.Select(ctx, input.ConsoleOptions{
				Message: "Which service would you like to host in Azure?",
				Options: serviceOptions,
			})
			if err != nil {
				return nil, err
			}

			confirm, err := a.console.Confirm(ctx, input.ConsoleOptions{
				Message: "azd will use " + color.MagentaString("Azure Container App") + " to host this project. Continue?",
			})
			if err != nil || !confirm {
				return nil, err
			}

			svc := prjConfig.Services[serviceOptions[serviceOption]]
			resSpec, err := addServiceAsResource(ctx, a.console, svc, project.ResourceTypeHostContainerApp)
			if err != nil {
				return nil, err
			}
			resourceToAdd = resSpec
		}
	case project.ResourceKindDatabase:
		dbOption, err := a.console.Select(ctx, input.ConsoleOptions{
			Message: "Which type of database?",
			Options: resourceTypesDisplay,
		})
		if err != nil {
			return nil, err
		}

		resourceToAdd.Type = resourceTypesDisplayMap[resourceTypesDisplay[dbOption]]
	case project.ResourceKindAI:
		aiOption, err := a.console.Select(ctx, input.ConsoleOptions{
			Message: "Which type of Azure OpenAI service?",
			Options: []string{
				"Chat (GPT)",
				"Embeddings (Document search)",
			}})
		if err != nil {
			return nil, err
		}

		resourceToAdd.Type = project.ResourceTypeOpenAiModel
		var allModels []ModelList
		for {
			err = provisioning.EnsureSubscriptionAndLocation(ctx, a.envManager, a.env, a.prompter, nil)
			if err != nil {
				return nil, err
			}

			cred, err := a.creds.CredentialForSubscription(ctx, a.env.GetSubscriptionId())
			if err != nil {
				return nil, fmt.Errorf("getting credentials: %w", err)
			}

			pipeline, err := armruntime.NewPipeline(
				"cognitive-list", "1.0.0", cred, runtime.PipelineOptions{}, a.armClientOptions)
			if err != nil {
				return nil, fmt.Errorf("failed creating HTTP pipeline: %w", err)
			}

			a.console.ShowSpinner(
				ctx,
				fmt.Sprintf("Fetching available models in %s...", a.env.GetLocation()),
				input.Step)

			location := fmt.Sprintf(
				//nolint:lll
				"https://management.azure.com/subscriptions/%s/providers/Microsoft.CognitiveServices/locations/%s/models?api-version=2023-05-01",
				a.env.GetSubscriptionId(),
				a.env.GetLocation())
			req, err := runtime.NewRequest(ctx, http.MethodGet, location)
			if err != nil {
				return nil, fmt.Errorf("creating request: %w", err)
			}

			resp, err := pipeline.Do(req)
			if err != nil {
				return nil, fmt.Errorf("making request: %w", err)
			}

			if resp.StatusCode != http.StatusOK {
				return nil, runtime.NewResponseError(resp)
			}

			body, err := runtime.Payload(resp)
			if err != nil {
				return nil, fmt.Errorf("reading response: %w", err)
			}

			a.console.StopSpinner(ctx, "", input.Step)
			var response ModelResponse
			err = json.Unmarshal(body, &response)
			if err != nil {
				return nil, fmt.Errorf("decoding response: %w", err)
			}

			for _, model := range response.Value {
				if model.Kind == "OpenAI" && slices.ContainsFunc(model.Model.Skus, func(sku ModelSku) bool {
					return sku.Name == "Standard"
				}) {
					switch aiOption {
					case 0:
						if model.Model.Name == "gpt-4o" || model.Model.Name == "gpt-4" {
							allModels = append(allModels, model)
						}
					case 1:
						if strings.HasPrefix(model.Model.Name, "text-embedding") {
							allModels = append(allModels, model)
						}
					}
				}

			}
			fmt.Printf("Length of all models: %d", len(allModels))
			if len(allModels) > 0 {
				break
			}

			_, err = a.rm.FindResourceGroupForEnvironment(
				ctx, a.env.GetSubscriptionId(), a.env.Name())
			var notFoundError *azureutil.ResourceNotFoundError
			if errors.As(err, &notFoundError) { // not yet provisioned, we're safe here
				a.console.MessageUxItem(ctx, &ux.WarningMessage{
					Description: fmt.Sprintf("No models found in this %s", a.env.GetLocation()),
				})
				confirm, err := a.console.Confirm(ctx, input.ConsoleOptions{
					Message: "Would you like to try a different location?",
				})
				if err != nil {
					return nil, err
				}
				if confirm {
					a.env.SetLocation("")
					continue
				}
			} else if err != nil {
				return nil, fmt.Errorf("finding resource group: %w", err)
			}

			return nil, fmt.Errorf("no models found in %s", a.env.GetLocation())
		}

		slices.SortFunc(allModels, func(a ModelList, b ModelList) int {
			return strings.Compare(b.Model.SystemData.CreatedAt, a.Model.SystemData.CreatedAt)
		})

		displayModels := make([]string, 0, len(allModels))
		models := make([]Model, 0, len(allModels))
		for _, model := range allModels {
			models = append(models, model.Model)
			displayModels = append(displayModels, fmt.Sprintf("%s\t%s", model.Model.Name, model.Model.Version))
		}

		if a.console.IsSpinnerInteractive() {
			displayModels, err = tabWrite(displayModels, 3)
			if err != nil {
				return nil, fmt.Errorf("writing models: %w", err)
			}
		}

		sel, err := a.console.Select(ctx, input.ConsoleOptions{
			Message: "Select the model",
			Options: displayModels,
		})
		if err != nil {
			return nil, err
		}

		resourceToAdd.Props = project.AIModelProps{
			Model: project.AIModelPropsModel{
				Name:    models[sel].Name,
				Version: models[sel].Version,
			},
		}

		resourceToAdd.Name = models[sel].Name
		i := 1
		for {
			if _, exists := prjConfig.Resources[resourceToAdd.Name]; exists {
				i++
				resourceToAdd.Name = fmt.Sprintf("%s-%d", models[sel].Name, i)
			} else {
				break
			}
		}
	default:
		return nil, fmt.Errorf("not implemented")
	}

	resourceToAddUses := []string{}
	if serviceToAdd != nil && string(resourceToAdd.Type) != "" {
		type resourceDisplay struct {
			Resource *project.ResourceConfig
			Display  string
		}
		res := make([]resourceDisplay, 0, len(prjConfig.Resources))
		for _, r := range prjConfig.Resources {
			res = append(res, resourceDisplay{
				Resource: r,
				Display: fmt.Sprintf(
					"[%s]\t%s",
					r.Type.String(),
					r.Name),
			})
		}
		slices.SortFunc(res, func(a, b resourceDisplay) int {
			comp := strings.Compare(a.Display, b.Display)
			if comp == 0 {
				return strings.Compare(a.Resource.Name, b.Resource.Name)
			}
			return comp
		})

		if len(res) > 0 {
			labels := make([]string, 0, len(res))
			for _, r := range res {
				labels = append(labels, r.Display)
			}
			if a.console.IsSpinnerInteractive() {
				labels, err = tabWrite(labels, 3)
				if err != nil {
					return nil, fmt.Errorf("writing models: %w", err)
				}
			}
			uses, err := a.console.MultiSelect(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("Select the resources that uses %s", color.BlueString(serviceToAdd.Name)),
				Options: labels,
			})
			if err != nil {
				return nil, err
			}

			// MultiSelect returns string[] not int[]
			for _, use := range uses {
				for i := len(use) - 1; i >= 0; i-- {
					if unicode.IsSpace(rune(use[i])) {
						resourceToAdd.Uses = append(resourceToAdd.Uses, use[i+1:])
						break
					}
				}
			}
		}
	} else {
		svc := make([]string, 0, len(prjConfig.Services))
		for _, service := range prjConfig.Services {
			svc = append(svc, service.Name)
		}
		slices.Sort(svc)

		if len(svc) > 0 {
			resourceToAddUses, err = a.console.MultiSelect(ctx, input.ConsoleOptions{
				Message: "Select the service(s) that uses this resource",
				Options: svc,
			})
			if err != nil {
				return nil, err
			}
		}
	}

	file, err := os.OpenFile(a.azdCtx.ProjectPath(), os.O_RDWR, osutil.PermissionFile)
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)

	var doc yaml.Node
	err = decoder.Decode(&doc)
	if err != nil {
		return nil, fmt.Errorf("failed to decode: %w", err)
	}

	if serviceToAdd != nil {
		serviceNode, err := EncodeAsYamlNode(map[string]*project.ServiceConfig{serviceToAdd.Name: serviceToAdd})
		if err != nil {
			panic(fmt.Sprintf("encoding yaml node: %v", err))
		}

		err = AppendNode(&doc, "services?", serviceNode)
		if err != nil {
			return nil, fmt.Errorf("updating resources: %w", err)
		}
	}

	configureRes := configureResult{}
	// TODO(weilim): make the flow of adding resource/service more streamlined
	if string(resourceToAdd.Type) != "" {
		configureRes, err = a.Configure(ctx, resourceToAdd)
		if err != nil {
			return nil, err
		}

		resourceNode, err := EncodeAsYamlNode(map[string]*project.ResourceConfig{resourceToAdd.Name: resourceToAdd})
		if err != nil {
			panic(fmt.Sprintf("encoding yaml node: %v", err))
		}

		err = AppendNode(&doc, "resources?", resourceNode)
		if err != nil {
			return nil, fmt.Errorf("updating resources: %w", err)
		}
	}

	for _, svc := range resourceToAddUses {
		err = AppendNode(&doc, fmt.Sprintf("resources.%s.uses[]?", svc), &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: resourceToAdd.Name,
		})
		if err != nil {
			return nil, fmt.Errorf("updating services: %w", err)
		}
	}

	// Write modified YAML back to file
	err = file.Truncate(0)
	if err != nil {
		return nil, fmt.Errorf("truncating file: %w", err)
	}
	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("seeking to start of file: %w", err)
	}

	indentation := CalcIndentation(&doc)
	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(indentation)
	encoder.SetAssumeBlockAsLiteral(true)
	// encoder.SetIndentlessBlockSequence(true)

	err = encoder.Encode(&doc)
	if err != nil {
		return nil, fmt.Errorf("failed to encode: %w", err)
	}

	err = file.Close()
	if err != nil {
		return nil, fmt.Errorf("closing file: %w", err)
	}

	var followUp string
	defaultFollowUp := "You can run '" + color.BlueString("azd provision") + "' to provision these infrastructure changes."
	if infraDirExists {
		defaultFollowUp = "You can run '" + color.BlueString("azd infra synth") + "' to re-synthesize the infrastructure, "
		defaultFollowUp += "and then '" + color.BlueString("azd provision") + "' to provision these changes."
	}

	if len(resourceToAddUses) > 0 {
		followUp = "The following environment variables will be set in " +
			color.BlueString(ux.ListAsText(resourceToAddUses)) + ":\n\n"
		for _, envVar := range configureRes.ConnectionEnvVars {
			followUp += "  - " + envVar + "\n"
		}

		if configureRes.LearnMoreLink != "" {
			if configureRes.LearnMoreTopic != "" {
				followUp += "\n" + fmt.Sprintf(
					"Learn more about %s: %s",
					configureRes.LearnMoreTopic,
					output.WithHyperlink(configureRes.LearnMoreLink, configureRes.LearnMoreLink))
			} else {
				followUp += "\n" + fmt.Sprintf(
					"Learn more: %s",
					output.WithHyperlink(configureRes.LearnMoreLink, configureRes.LearnMoreLink))
			}
		}
		followUp += "\n" + defaultFollowUp + "\n" + "You may also run '" +
			color.BlueString("azd show <service> env") +
			"' to show environment variables of the currently provisioned instance."
	} else {
		followUp = defaultFollowUp
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   "azure.yaml has been updated to include the new resource.",
			FollowUp: followUp,
		},
	}, err
}

func NewInfraAddAction(
	azdCtx *azdcontext.AzdContext,
	envManager environment.Manager,
	env *environment.Environment,
	creds account.SubscriptionCredentialProvider,
	prompter prompt.Prompter,
	rm infra.ResourceManager,
	armClientOptions *arm.ClientOptions,
	appInit *repository.Initializer,
	console input.Console) actions.Action {
	return &AddAction{
		azdCtx:           azdCtx,
		console:          console,
		envManager:       envManager,
		env:              env,
		prompter:         prompter,
		rm:               rm,
		armClientOptions: armClientOptions,
		appInit:          appInit,
		creds:            creds,
	}
}

type configureResult struct {
	ConnectionEnvVars []string
	LearnMoreTopic    string
	LearnMoreLink     string
}

func (a *AddAction) Configure(ctx context.Context, r *project.ResourceConfig) (configureResult, error) {
	if r.Type == project.ResourceTypeDbRedis {
		r.Name = "redis"
		// this can be moved to central location for resource types
		return configureResult{
			ConnectionEnvVars: []string{
				"REDIS_HOST",
				"REDIS_PORT",
				"REDIS_ENDPOINT",
				"REDIS_PASSWORD",
			},
		}, nil
	}

	if r.Name == "" {
		dbName, err := a.console.Prompt(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf("Input the name of the app database (%s)", r.Type.String()),
			Help: "Hint: App database name\n\n" +
				"Name of the database that the app connects to. " +
				"This database will be created after running azd provision or azd up.",
		})
		if err != nil {
			return configureResult{}, err
		}

		r.Name = dbName
	}

	res := configureResult{}
	switch r.Type {
	case project.ResourceTypeDbPostgres:
		res.ConnectionEnvVars = []string{
			"POSTGRES_HOST",
			"POSTGRES_USERNAME",
			"POSTGRES_DATABASE",
			"POSTGRES_PASSWORD",
			"POSTGRES_PORT",
		}
	case project.ResourceTypeDbMongo:
		res.ConnectionEnvVars = []string{
			"AZURE_COSMOS_MONGODB_CONNECTION_STRING",
		}
	case project.ResourceTypeOpenAiModel:
		res.ConnectionEnvVars = []string{
			"AZURE_OPENAI_ENDPOINT",
			"AZURE_OPENAI_API_KEY",
		}
		res.LearnMoreTopic = "configuring your app to use Azure OpenAI"
		res.LearnMoreLink = "https://learn.microsoft.com/en-us/azure/ai-services/openai/supported-languages"
	}
	return res, nil
}

// addLocalProject prompts the user to add a local project as a service.
func (a *AddAction) addLocalProject(ctx context.Context) (*appdetect.Project, error) {
	// how does WD work here?
	path, err := repository.PromptDir(ctx, a.console, "Where is your project located?")
	if err != nil {
		return nil, err
	}

	prj, err := appdetect.DetectDirectory(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("detecting project: %w", err)
	}

	if prj == nil {
		return nil, errors.New("no supported project found")
	}

	_, supported := repository.LanguageMap[prj.Language]
	if !supported {
		return nil, errors.New("no supported project found")
	}

	confirm, err := a.console.Confirm(ctx, input.ConsoleOptions{
		Message:      fmt.Sprintf("Detected %s project. Continue?", color.BlueString(prj.Language.Display())),
		DefaultValue: true,
	})
	if err != nil {
		return nil, err
	}

	if !confirm {
		return nil, errors.New("cancelled")
	}

	return prj, nil
}

// projectAsService prompts the user for enough information to create a service.
func (a *AddAction) projectAsService(
	ctx context.Context,
	prj *appdetect.Project,
) (*project.ServiceConfig, error) {
	language, supported := repository.LanguageMap[prj.Language]
	if !supported {
		return nil, fmt.Errorf("unsupported language: %s", prj.Language)
	}

	name := filepath.Base(prj.Path)
	if prj.Path == "." {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getting working directory: %w", err)
		}
		name = filepath.Base(wd)
	}
	name = strings.ReplaceAll(name, ".", "-")

	// TODO:(weilim): allowed values for name
	name, err := a.console.Prompt(ctx, input.ConsoleOptions{
		Message:      "What should we call this project?",
		DefaultValue: name,
	})
	if err != nil {
		return nil, err
	}

	if prj.Docker == nil {
		confirm, err := a.console.Confirm(ctx, input.ConsoleOptions{
			Message:      "No Dockerfile found. Allow azd to automatically build a container image?",
			DefaultValue: true,
		})
		if err != nil {
			return nil, err
		}

		if !confirm {
			_, err := repository.PromptDir(ctx, a.console, "Where is your Dockerfile located?")
			if err != nil {
				return nil, err
			}

			panic("unimplemented")
		}
	}

	rel, err := filepath.Rel(a.azdCtx.ProjectDirectory(), prj.Path)
	if err != nil {
		return nil, fmt.Errorf("calculating relative path: %w", err)
	}

	svcSpec := project.ServiceConfig{}
	svcSpec.Name = name
	svcSpec.Host = project.ContainerAppTarget
	svcSpec.RelativePath = rel
	svcSpec.Language = language

	if prj.Docker != nil {
		relDocker, err := filepath.Rel(prj.Path, prj.Docker.Path)
		if err != nil {
			return nil, err
		}

		svcSpec.Docker = project.DockerProjectOptions{
			Path: relDocker,
		}
	}

	return &svcSpec, nil
}

func addServiceAsResource(
	ctx context.Context,
	console input.Console,
	svc *project.ServiceConfig,
	resourceType project.ResourceType) (*project.ResourceConfig, error) {
	resSpec := project.ResourceConfig{
		Name: svc.Name,
		Type: resourceType,
	}
	props := project.ContainerAppProps{
		Port: -1,
	}
	if svc.Docker.Path == "" {
		if _, err := os.Stat(filepath.Join(svc.RelativePath, "Dockerfile")); errors.Is(err, os.ErrNotExist) {
			// default builder always specifies port 80
			props.Port = 80
			if svc.Language == project.ServiceLanguageJava {
				props.Port = 8080
			}
		}
	}

	if props.Port == -1 {
		var port int
		for {
			val, err := console.Prompt(ctx, input.ConsoleOptions{
				Message: "What port does '" + resSpec.Name + "' listen on?",
			})
			if err != nil {
				return nil, err
			}

			port, err = strconv.Atoi(val)
			if err != nil {
				console.Message(ctx, "Port must be an integer.")
				continue
			}

			if port < 1 || port > 65535 {
				console.Message(ctx, "Port must be a value between 1 and 65535.")
				continue
			}

			break
		}
		props.Port = port
	}

	resSpec.Props = props
	return &resSpec, nil
}

func EncodeAsYamlNode(v interface{}) (*yaml.Node, error) {
	var node yaml.Node
	err := node.Encode(v)
	if err != nil {
		return nil, fmt.Errorf("encoding yaml node: %w", err)
	}

	// By default, the node will be a document node that represents a YAML document,
	// but we are only interested in the content of the document.
	return &node, nil
}

func AppendNode(root *yaml.Node, path string, node *yaml.Node) error {
	parts := strings.Split(path, ".")
	return modifyNodeRecursive(root, parts, node)
}

func modifyNodeRecursive(current *yaml.Node, parts []string, node *yaml.Node) error {
	if len(parts) == 0 {
		return appendNode(current, node)
	}

	optional := strings.HasSuffix(parts[0], "?")
	seek := strings.TrimSuffix(parts[0], "?")

	isArr := strings.HasSuffix(seek, "[]")
	seek = strings.TrimSuffix(seek, "[]")

	switch current.Kind {
	case yaml.DocumentNode:
		return modifyNodeRecursive(current.Content[0], parts, node)
	case yaml.MappingNode:
		for i := 0; i < len(current.Content); i += 2 {
			if current.Content[i].Value == seek {
				return modifyNodeRecursive(current.Content[i+1], parts[1:], node)
			}
		}
	case yaml.SequenceNode:
		index, err := strconv.Atoi(seek)
		if err != nil {
			return err
		}
		if index >= 0 && index < len(current.Content) {
			return modifyNodeRecursive(current.Content[index], parts[1:], node)
		}
	}

	if optional {
		current.Content = append(current.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: seek})
		if isArr {
			current.Content = append(current.Content, &yaml.Node{
				Kind:    yaml.SequenceNode,
				Content: []*yaml.Node{},
			})
		} else {
			current.Content = append(current.Content, &yaml.Node{
				Kind:    yaml.MappingNode,
				Content: []*yaml.Node{},
			})
		}

		return modifyNodeRecursive(current.Content[len(current.Content)-1], parts[1:], node)
	}

	return fmt.Errorf("path not found: %s", strings.Join(parts, "."))
}

func appendNode(current *yaml.Node, node *yaml.Node) error {
	// get the content of the node to append
	contents := []*yaml.Node{}
	switch node.Kind {
	case yaml.MappingNode, yaml.SequenceNode, yaml.DocumentNode:
		contents = append(contents, node.Content...)
	case yaml.ScalarNode:
		contents = append(contents, node)
	default:
		return fmt.Errorf("cannot append node of kind %d", node.Kind)
	}

	switch current.Kind {
	case yaml.MappingNode:
		current.Content = append(current.Content, contents...)
	case yaml.SequenceNode:
		current.Content = append(current.Content, contents...)
	default:
		return fmt.Errorf("cannot append to node of kind %d", current.Kind)
	}
	return nil
}

// CalcIndentation calculates the indentation level of the first mapping node in the document.
// If the document does not contain a mapping node that is indented, it returns 2.
func CalcIndentation(doc *yaml.Node) int {
	var curr *yaml.Node
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		curr = doc.Content[0]
	}

	if curr.Kind == yaml.MappingNode {
		for i := 0; i < len(curr.Content); i += 2 {
			if curr.Content[i+1].Kind == yaml.MappingNode &&
				curr.Content[i+1].Line > curr.Content[i].Line &&
				curr.Content[i+1].Column > curr.Content[i].Column {
				return curr.Content[i+1].Column - curr.Content[i].Column
			}
		}
	}

	return 2
}

type ModelResponse struct {
	Value []ModelList `json:"value"`
}

type ModelList struct {
	Kind  string `json:"kind"`
	Model Model  `json:"model"`
}

type Model struct {
	Name       string          `json:"name"`
	Skus       []ModelSku      `json:"skus"`
	Version    string          `json:"version"`
	SystemData ModelSystemData `json:"systemData"`
}

type ModelSku struct {
	Name string `json:"name"`
}

type ModelSystemData struct {
	CreatedAt string `json:"createdAt"`
}
