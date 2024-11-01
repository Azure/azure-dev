package add

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	armruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func (a *AddAction) selectOpenAi(
	console input.Console,
	ctx context.Context,
	p promptOptions) (r *project.ResourceConfig, err error) {
	resourceToAdd := &project.ResourceConfig{}
	aiOption, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Which type of Azure OpenAI service?",
		Options: []string{
			"Chat (GPT)",                   // 0 - chat
			"Embeddings (Document search)", // 1 - embeddings
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

		console.ShowSpinner(
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

		console.StopSpinner(ctx, "", input.Step)
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
		if len(allModels) > 0 {
			break
		}

		_, err = a.rm.FindResourceGroupForEnvironment(
			ctx, a.env.GetSubscriptionId(), a.env.Name())
		var notFoundError *azureutil.ResourceNotFoundError
		if errors.As(err, &notFoundError) { // not yet provisioned, we're safe here
			console.MessageUxItem(ctx, &ux.WarningMessage{
				Description: fmt.Sprintf("No models found in %s", a.env.GetLocation()),
			})
			confirm, err := console.Confirm(ctx, input.ConsoleOptions{
				Message: "Try a different location?",
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

	if console.IsSpinnerInteractive() {
		displayModels, err = output.TabAlign(displayModels, 5)
		if err != nil {
			return nil, fmt.Errorf("writing models: %w", err)
		}
	}

	sel, err := console.Select(ctx, input.ConsoleOptions{
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

	return resourceToAdd, nil
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
