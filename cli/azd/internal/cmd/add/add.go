// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
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
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/azure/azure-dev/cli/azd/pkg/yamlnode"
	"github.com/braydonk/yaml"
	"github.com/spf13/cobra"
)

func NewAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add",
		Short: "Add a component to your project.",
	}
}

type AddAction struct {
	azd              workflow.AzdCommandRunner
	azdCtx           *azdcontext.AzdContext
	env              *environment.Environment
	envManager       environment.Manager
	subManager       *account.SubscriptionsManager
	alphaManager     *alpha.FeatureManager
	creds            account.SubscriptionCredentialProvider
	rm               infra.ResourceManager
	resourceService  *azapi.ResourceService
	armClientOptions *arm.ClientOptions
	prompter         prompt.Prompter
	console          input.Console
	accountManager   account.Manager
	azureClient      *azapi.AzureClient
	importManager    *project.ImportManager
}

func (a *AddAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	prjConfig, err := project.Load(ctx, a.azdCtx.ProjectPath())
	if err != nil {
		return nil, err
	}

	// Having a subscription is required for any azd compose (add)
	err = provisioning.EnsureSubscription(ctx, a.envManager, a.env, a.prompter)
	if err != nil {
		return nil, err
	}

	err = ensureCompatibleProject(ctx, a.importManager, prjConfig)
	if err != nil {
		return nil, err
	}

	selectMenu := a.selectMenu()
	slices.SortFunc(selectMenu, func(a, b Menu) int {
		return strings.Compare(a.Label, b.Label)
	})

	selections := make([]string, 0, len(selectMenu))
	for _, menu := range selectMenu {
		selections = append(selections, menu.Label)
	}
	idx, err := a.console.Select(ctx, input.ConsoleOptions{
		Message: "What would you like to add?",
		Options: selections,
	})
	if err != nil {
		return nil, err
	}

	selected := selectMenu[idx]

	resourceToAdd := &project.ResourceConfig{}
	var serviceToAdd *project.ServiceConfig

	promptOpts := PromptOptions{PrjConfig: prjConfig}
	r, err := selected.SelectResource(a.console, ctx, promptOpts)
	if err != nil {
		return nil, err
	}
	resourceToAdd = r

	if strings.EqualFold(selected.Namespace, "host") {
		svc, r, err := a.configureHost(a.console, ctx, promptOpts, r.Type)
		if err != nil {
			return nil, err
		}
		serviceToAdd = svc
		resourceToAdd = r
	}

	resourceToAdd, err = a.ConfigureLive(ctx, resourceToAdd, a.console, promptOpts)
	if err != nil {
		return nil, err
	}

	resourceToAdd, err = Configure(ctx, resourceToAdd, a.console, promptOpts)
	if err != nil {
		return nil, err
	}

	usedBy, err := promptUsedBy(ctx, resourceToAdd, a.console, promptOpts)
	if err != nil {
		return nil, err
	}

	if r, exists := prjConfig.Resources[resourceToAdd.Name]; exists && r.Type != project.ResourceTypeAiProject {
		log.Panicf("unhandled validation: resource with name %s already exists", resourceToAdd.Name)
	}

	if serviceToAdd != nil {
		if _, exists := prjConfig.Services[serviceToAdd.Name]; exists {
			log.Panicf("unhandled validation: service with name %s already exists", serviceToAdd.Name)
		}
	}

	file, err := os.OpenFile(a.azdCtx.ProjectPath(), os.O_RDWR, osutil.PermissionFile)
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.SetScanBlockScalarAsLiteral(true)

	var doc yaml.Node
	err = decoder.Decode(&doc)
	if err != nil {
		return nil, fmt.Errorf("failed to decode: %w", err)
	}

	if serviceToAdd != nil {
		serviceNode, err := yamlnode.Encode(serviceToAdd)
		if err != nil {
			panic(fmt.Sprintf("encoding yaml node: %v", err))
		}

		err = yamlnode.Set(&doc, fmt.Sprintf("services?.%s", serviceToAdd.Name), serviceNode)
		if err != nil {
			return nil, fmt.Errorf("adding service: %w", err)
		}
	}

	resourcesToAdd := []*project.ResourceConfig{resourceToAdd}
	dependentResources := project.DependentResourcesOf(resourceToAdd)
	requiredByMessages := make([]string, 0)
	// Find any dependent resources that are not already in the project
	for _, dep := range dependentResources {
		if prjConfig.Resources[dep.Name] == nil {
			resourcesToAdd = append(resourcesToAdd, dep)
			requiredByMessages = append(requiredByMessages,
				fmt.Sprintf("(%s is required by %s)",
					output.WithHighLightFormat(dep.Name),
					output.WithHighLightFormat(resourceToAdd.Name)))
		}
	}

	// Add resource and any non-existing dependent resources
	for _, resource := range resourcesToAdd {
		resourceNode, err := yamlnode.Encode(resource)
		if err != nil {
			panic(fmt.Sprintf("encoding resource yaml node: %v", err))
		}

		err = yamlnode.Set(&doc, fmt.Sprintf("resources?.%s", resource.Name), resourceNode)
		if err != nil {
			return nil, fmt.Errorf("setting resource: %w", err)
		}
	}

	for _, svc := range usedBy {
		if slices.Contains(prjConfig.Resources[svc].Uses, resourceToAdd.Name) {
			continue
		}
		err = yamlnode.Append(&doc, fmt.Sprintf("resources.%s.uses[]?", svc), &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: resourceToAdd.Name,
		})
		if err != nil {
			return nil, fmt.Errorf("appending resource: %w", err)
		}
	}

	new, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("marshalling yaml: %w", err)
	}

	newCfg, err := project.Parse(ctx, string(new))
	if err != nil {
		return nil, fmt.Errorf("re-parsing yaml: %w", err)
	}

	a.console.Message(ctx, fmt.Sprintf("\nPreviewing changes to %s:\n", output.WithHighLightFormat("azure.yaml")))
	diffString, diffErr := DiffBlocks(prjConfig.Resources, newCfg.Resources)
	if diffErr != nil {
		a.console.Message(ctx, "Preview unavailable. Pass --debug for more details.\n")
		log.Printf("add-diff: preview failed: %v", diffErr)
	} else {
		a.console.Message(ctx, diffString)
		if len(requiredByMessages) > 0 {
			for _, msg := range requiredByMessages {
				a.console.Message(ctx, msg)
			}
			a.console.Message(ctx, "")
		}
	}

	confirm, err := a.console.Confirm(ctx, input.ConsoleOptions{
		Message:      "Accept changes to azure.yaml?",
		DefaultValue: true,
	})
	if err != nil || !confirm {
		return nil, err
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

	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(2)
	// preserve multi-line blocks style
	encoder.SetAssumeBlockAsLiteral(true)
	err = encoder.Encode(&doc)
	if err != nil {
		return nil, fmt.Errorf("failed to encode: %w", err)
	}

	err = file.Close()
	if err != nil {
		return nil, fmt.Errorf("closing file: %w", err)
	}

	envModified := false
	for _, resource := range resourcesToAdd {
		if resource.ResourceId != "" {
			a.env.DotenvSet(infra.ResourceIdName(resource.Name), resource.ResourceId)
			envModified = true
		}
	}

	if envModified {
		err = a.envManager.Save(ctx, a.env)
		if err != nil {
			return nil, fmt.Errorf("saving environment: %w", err)
		}
	}

	a.console.MessageUxItem(ctx, &ux.ActionResult{
		SuccessMessage: "azure.yaml updated.",
	})

	infraOptions, err := prjConfig.Infra.GetWithDefaults()
	if err != nil {
		return nil, fmt.Errorf("getting infra options: %w", err)
	}

	infraRoot := infraOptions.Path
	if !filepath.IsAbs(infraRoot) {
		infraRoot = filepath.Join(prjConfig.Path, infraRoot)
	}

	var followUpMessage string
	addedKeyVault := slices.ContainsFunc(resourcesToAdd, func(resource *project.ResourceConfig) bool {
		return strings.EqualFold(resource.Name, "vault")
	})
	keyVaultFollowUpMessage := fmt.Sprintf(
		"\nRun '%s' to add a secret to the key vault.",
		output.WithHighLightFormat("azd env set-secret <name>"))

	if _, err := pathHasInfraModule(infraRoot, infraOptions.Module); err == nil {
		followUpMessage = fmt.Sprintf(
			"Run '%s' to re-generate the infrastructure, "+
				"then run '%s' to provision these changes anytime later.",
			output.WithHighLightFormat("azd infra gen"),
			output.WithHighLightFormat("azd provision"))
		if addedKeyVault {
			followUpMessage += keyVaultFollowUpMessage
		}
		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				FollowUp: followUpMessage,
			},
		}, err
	}

	verb := "provision"
	verbCapitalized := "Provision"
	followUpCmd := "provision"

	if serviceToAdd != nil {
		verb = "provision and deploy"
		verbCapitalized = "Provision and deploy"
		followUpCmd = "up"
	}

	a.console.Message(ctx, "")
	provisionOption, err := selectProvisionOptions(
		ctx,
		a.console,
		fmt.Sprintf("Do you want to %s these changes?", verb))
	if err != nil {
		return nil, err
	}

	if provisionOption == provisionPreview {
		err = a.previewProvision(ctx, prjConfig, resourcesToAdd, usedBy)
		if err != nil {
			return nil, err
		}

		y, err := a.console.Confirm(ctx, input.ConsoleOptions{
			Message:      fmt.Sprintf("%s these changes to Azure?", verbCapitalized),
			DefaultValue: true,
		})
		if err != nil {
			return nil, err
		}

		if !y {
			provisionOption = provisionSkip
		} else {
			provisionOption = provision
		}
	}

	if provisionOption == provision {
		a.azd.SetArgs([]string{followUpCmd})
		err = a.azd.ExecuteContext(ctx)
		if err != nil {
			return nil, err
		}

		followUpMessage = "Run '" +
			output.WithHighLightFormat("azd show %s", resourceToAdd.Name) +
			"' to show details about the newly provisioned resource."
	} else {
		followUpMessage = fmt.Sprintf(
			"Run '%s' to %s these changes anytime later.",
			output.WithHighLightFormat("azd %s", followUpCmd),
			verb)
	}

	if addedKeyVault {
		followUpMessage += keyVaultFollowUpMessage
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			FollowUp: followUpMessage,
		},
	}, err
}

// ensureCompatibleProject checks if the project is compatible with the add command.
// A project is incompatible if:
// - It has an Aspire app host
// - It appears to be a non-compose template (has infra files but no resources defined in azure.yaml)
func ensureCompatibleProject(
	ctx context.Context,
	importManager *project.ImportManager,
	prjConfig *project.ProjectConfig,
) error {
	if hasAppHost := importManager.HasAppHost(ctx, prjConfig); hasAppHost {
		return &internal.ErrorWithSuggestion{
			Err: fmt.Errorf("incompatible project: found Aspire app host"),
			Suggestion: fmt.Sprintf("%s does not support .NET Aspire projects.",
				output.WithHighLightFormat("azd add")),
		}
	}

	mergedOptions, err := prjConfig.Infra.GetWithDefaults()
	if err != nil {
		return err
	}

	infraRoot := mergedOptions.Path
	if !filepath.IsAbs(infraRoot) {
		infraRoot = filepath.Join(prjConfig.Path, infraRoot)
	}

	hasResources := len(prjConfig.Resources) > 0
	hasInfra, err := pathHasInfraModule(infraRoot, mergedOptions.Module)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			hasInfra = false
		} else {
			return err
		}
	}

	if hasInfra && !hasResources {
		return &internal.ErrorWithSuggestion{
			Err: fmt.Errorf("incompatible project: found infra directory and azure.yaml without resources"),
			Suggestion: fmt.Sprintf("%s does not support most azd templates.",
				output.WithHighLightFormat("azd add")),
		}
	}

	return nil
}

type provisionSelection int

const (
	provisionUnknown = iota
	provision
	provisionPreview
	provisionSkip
)

func selectProvisionOptions(
	ctx context.Context,
	console input.Console,
	msg string) (provisionSelection, error) {
	selection, err := console.Select(ctx, input.ConsoleOptions{
		Message: msg,
		Options: []string{
			"Yes (preview changes)", // 0 - preview
			"Yes",                   // 1 - provision
			"No",                    // 2 - no
		},
	})
	if err != nil {
		return provisionUnknown, err
	}

	switch selection {
	case 0:
		return provisionPreview, nil
	case 1:
		return provision, nil
	case 2:
		return provisionSkip, nil
	default:
		panic("unhandled")
	}
}

func NewAddAction(
	azdCtx *azdcontext.AzdContext,
	envManager environment.Manager,
	subManager *account.SubscriptionsManager,
	alphaManager *alpha.FeatureManager,
	env *environment.Environment,
	creds account.SubscriptionCredentialProvider,
	prompter prompt.Prompter,
	rm infra.ResourceManager,
	resourceService *azapi.ResourceService,
	armClientOptions *arm.ClientOptions,
	azd workflow.AzdCommandRunner,
	accountManager account.Manager,
	console input.Console,
	azureClient *azapi.AzureClient,
	importManager *project.ImportManager) actions.Action {
	return &AddAction{
		azdCtx:           azdCtx,
		console:          console,
		envManager:       envManager,
		subManager:       subManager,
		alphaManager:     alphaManager,
		env:              env,
		prompter:         prompter,
		rm:               rm,
		resourceService:  resourceService,
		armClientOptions: armClientOptions,
		creds:            creds,
		azd:              azd,
		accountManager:   accountManager,
		azureClient:      azureClient,
		importManager:    importManager,
	}
}
