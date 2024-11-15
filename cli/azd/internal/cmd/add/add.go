// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/repository"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/azure/azure-dev/cli/azd/pkg/yamlnode"
	"github.com/braydonk/yaml"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func NewAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add",
		Short: fmt.Sprintf("Add a component to your project. %s", output.WithWarningFormat("(Alpha)")),
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
	appInit          *repository.Initializer
	armClientOptions *arm.ClientOptions
	prompter         prompt.Prompter
	console          input.Console
}

var composeFeature = alpha.MustFeatureKey("compose")

func (a *AddAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if !a.alphaManager.IsEnabled(composeFeature) {
		return nil, fmt.Errorf(
			"compose is currently under alpha support and must be explicitly enabled."+
				" Run `%s` to enable this feature", alpha.GetEnableCommand(composeFeature),
		)
	}

	prjConfig, err := project.Load(ctx, a.azdCtx.ProjectPath())
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

	promptOpts := promptOptions{prj: prjConfig}
	if strings.EqualFold(selected.Namespace, "host") {
		svc, r, err := a.configureHost(a.console, ctx, promptOpts)
		if err != nil {
			return nil, err
		}

		resourceToAdd = r
		serviceToAdd = svc
	} else {
		r, err := selected.SelectResource(a.console, ctx, promptOpts)
		if err != nil {
			return nil, err
		}

		resourceToAdd = r
	}

	resourceToAdd, err = configure(ctx, resourceToAdd, a.console, promptOpts)
	if err != nil {
		return nil, err
	}

	usedBy, err := promptUsedBy(ctx, resourceToAdd, a.console, promptOpts)
	if err != nil {
		return nil, err
	}

	if _, exists := prjConfig.Resources[resourceToAdd.Name]; exists {
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

	resourceNode, err := yamlnode.Encode(resourceToAdd)
	if err != nil {
		panic(fmt.Sprintf("encoding yaml node: %v", err))
	}

	err = yamlnode.Set(&doc, fmt.Sprintf("resources?.%s", resourceToAdd.Name), resourceNode)
	if err != nil {
		return nil, fmt.Errorf("setting resource: %w", err)
	}

	for _, svc := range usedBy {
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

	a.console.Message(ctx, fmt.Sprintf("\nPreviewing changes to %s:\n", color.BlueString("azure.yaml")))
	diffString, diffErr := DiffBlocks(prjConfig.Resources, newCfg.Resources)
	if diffErr != nil {
		a.console.Message(ctx, "Preview unavailable. Pass --debug for more details.\n")
		log.Printf("add-diff: preview failed: %v", diffErr)
	} else {
		a.console.Message(ctx, diffString)
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

	a.console.MessageUxItem(ctx, &ux.ActionResult{
		SuccessMessage: "azure.yaml updated.",
	})

	// Use default project values for Infra when not specified in azure.yaml
	if prjConfig.Infra.Module == "" {
		prjConfig.Infra.Module = project.DefaultModule
	}
	if prjConfig.Infra.Path == "" {
		prjConfig.Infra.Path = project.DefaultPath
	}

	infraRoot := prjConfig.Infra.Path
	if !filepath.IsAbs(infraRoot) {
		infraRoot = filepath.Join(prjConfig.Path, infraRoot)
	}

	if _, err := pathHasInfraModule(infraRoot, prjConfig.Infra.Module); err == nil {
		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				FollowUp: "Run '" + color.BlueString("azd infra synth") + "' to re-synthesize the infrastructure, " +
					"then run '" + color.BlueString("azd provision") + "' to provision these changes anytime later.",
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
		err = a.previewProvision(ctx, prjConfig, resourceToAdd, usedBy)
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

		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				FollowUp: "Run '" +
					color.BlueString(fmt.Sprintf("azd show %s", resourceToAdd.Name)) +
					"' to show details about the newly provisioned resource.",
			},
		}, nil
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			FollowUp: fmt.Sprintf(
				"Run '%s' to %s these changes anytime later.",
				color.BlueString("azd %s", followUpCmd),
				verb),
		},
	}, err
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
	armClientOptions *arm.ClientOptions,
	appInit *repository.Initializer,
	azd workflow.AzdCommandRunner,
	console input.Console) actions.Action {
	return &AddAction{
		azdCtx:           azdCtx,
		console:          console,
		envManager:       envManager,
		subManager:       subManager,
		alphaManager:     alphaManager,
		env:              env,
		prompter:         prompter,
		rm:               rm,
		armClientOptions: armClientOptions,
		appInit:          appInit,
		creds:            creds,
		azd:              azd,
	}
}
