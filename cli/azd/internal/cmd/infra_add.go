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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	armruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
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

	resources := project.AllCategories()
	displayOptions := []string{}
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

	selectedCategory := project.ResourceKind(displayOptions[continueOption])

	// Get the resource types for the selected category
	resourceTypes := resources[selectedCategory]
	resourceTypesDisplay := make([]string, 0, len(resourceTypes))
	resourceTypesDisplayMap := make(map[string]project.ResourceType)
	for _, resourceType := range resourceTypes {
		resourceTypesDisplay = append(resourceTypesDisplay, resourceType.String())
		resourceTypesDisplayMap[resourceType.String()] = resourceType
	}
	slices.Sort(resourceTypesDisplay)

	resourceToAdd := &project.ResourceConfig{}
	switch selectedCategory {
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

			allModels = response.Value
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

		displayModels := make([]string, 0, len(allModels))
		models := make([]Model, 0, len(allModels))
		slices.SortFunc(allModels, func(a ModelList, b ModelList) int {
			return strings.Compare(b.Model.SystemData.CreatedAt, a.Model.SystemData.CreatedAt)
		})

		for _, model := range allModels {
			if model.Kind != "OpenAI" {
				continue
			}

			switch aiOption {
			case 0:
				// this filter logic is currently in the CLI, perhaps it should be moved server-side
				if model.Model.Name == "gpt-4o" || model.Model.Name == "gpt-4" {
					models = append(models, model.Model)
					displayModels = append(displayModels, fmt.Sprintf("%s\t%s", model.Model.Name, model.Model.Version))
				}
			case 1:
				if strings.HasPrefix(model.Model.Name, "text-embedding") {
					models = append(models, model.Model)
					displayModels = append(displayModels, fmt.Sprintf("%s\t%s", model.Model.Name, model.Model.Version))
				}
			}
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

	svc := make([]string, 0, len(prjConfig.Services))
	for _, service := range prjConfig.Services {
		svc = append(svc, service.Name)
	}
	slices.Sort(svc)

	svcOptions := []string{}
	if len(svc) > 0 {
		svcOptions, err = a.console.MultiSelect(ctx, input.ConsoleOptions{
			Message: "Select the service(s) that uses this resource",
			Options: svc,
		})
		if err != nil {
			return nil, err
		}
	}

	configureRes, err := a.Configure(ctx, resourceToAdd)
	if err != nil {
		return nil, err
	}

	resourceNode, err := EncodeAsYamlNode(map[string]*project.ResourceConfig{resourceToAdd.Name: resourceToAdd})
	if err != nil {
		panic(fmt.Sprintf("encoding yaml node: %v", err))
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

	err = AppendNode(&doc, "resources?", resourceNode)
	if err != nil {
		return nil, fmt.Errorf("updating resources: %w", err)
	}

	for _, svc := range svcOptions {
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

	if len(svcOptions) > 0 {
		followUp = "The following environment variables will be set in " +
			color.BlueString(ux.ListAsText(svcOptions)) + ":\n\n"
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
	console input.Console) actions.Action {
	return &AddAction{
		azdCtx:           azdCtx,
		console:          console,
		envManager:       envManager,
		env:              env,
		prompter:         prompter,
		rm:               rm,
		armClientOptions: armClientOptions,
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
	Version    string          `json:"version"`
	SystemData ModelSystemData `json:"systemData"`
}

type ModelSystemData struct {
	CreatedAt string `json:"createdAt"`
}
