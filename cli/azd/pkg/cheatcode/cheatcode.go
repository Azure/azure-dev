package cheatcode

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	rm_armmsi "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/azure/azure-dev/cli/azd/cmd"
	"github.com/azure/azure-dev/cli/azd/internal"
	azd_armmsi "github.com/azure/azure-dev/cli/azd/pkg/armmsi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

func SetCopilotCodingAgentFederation(ctx context.Context,
	rootContainer *ioc.NestedContainer,
	repoSlug string,
	copilotEnvName string,
	subscriptionId string,
	msiId string, // was *authConfig.msi.ID
) error {
	var deps struct {
		msiService azd_armmsi.ArmMsiService `container:"type"`
	}

	if err := rootContainer.Fill(&deps); err != nil {
		return err
	}

	credentialSafeName := strings.ReplaceAll(repoSlug, "/", "-")

	federatedCredentialOptions := []*graphsdk.FederatedIdentityCredential{
		{
			Name:        url.PathEscape(fmt.Sprintf("%s-copilot-coding-agent-env", credentialSafeName)),
			Issuer:      pipeline.CheatCodeIssuer,
			Subject:     fmt.Sprintf("repo:%s:environment:%s", repoSlug, copilotEnvName),
			Description: to.Ptr("Created by Azure Developer CLI"),
			Audiences:   []string{pipeline.CheatCodeFederatedIdentityAudience},
		},
	}

	// Enable federated credentials if requested
	type fedCredentialData struct{ Name, Subject, Issuer string }

	// TODO: for now, assuming MSI

	// convert fedCredentials from msGraph to armmsi.FederatedIdentityCredential
	armFedCreds := make([]rm_armmsi.FederatedIdentityCredential, len(federatedCredentialOptions))
	for i, fedCred := range federatedCredentialOptions {
		armFedCreds[i] = rm_armmsi.FederatedIdentityCredential{
			Name: to.Ptr(fedCred.Name),
			Properties: &rm_armmsi.FederatedIdentityCredentialProperties{
				Subject:   to.Ptr(fedCred.Subject),
				Issuer:    to.Ptr(fedCred.Issuer),
				Audiences: to.SliceOfPtrs(fedCred.Audiences...),
			},
		}
	}

	if _, err := deps.msiService.ApplyFederatedCredentials(ctx, subscriptionId, msiId, armFedCreds); err != nil {
		return fmt.Errorf("failed to create federated credentials: %w", err)
	}

	return nil
}

func NewRootContainer(ctx context.Context, workingDir string) (*ioc.NestedContainer, error) {
	rootContainer := ioc.NewNestedContainer(nil)
	cmd.CheatCodeRegisterCommonDependencies(rootContainer) // TODO: violating several principles. I think the proper strategy here is to compose a more minimal container, but same basic concept.

	rootContainer.MustRegisterSingleton(func() context.Context {
		// TODO: not sure what the right way to do this is. Perhaps this is the right way, but I'd probably want to scope
		// this to occur just before the calls I make so we're not just holding onto an ancient context.
		return ctx
	})

	rootContainer.MustRegisterSingleton(func() *internal.GlobalCommandOptions {
		// TODO: might not need to worry about this one once we do the real-deal, might just be a dep loading up
		// that I'm not using.
		return &internal.GlobalCommandOptions{
			NoPrompt: false,
		}
	})

	rootContainer.MustRegisterSingleton(func() *cobra.Command {
		// TODO: I'll have a "real" command to register once this is moved into the extension
		cmd := &cobra.Command{}
		return cmd
	})

	if err := os.Chdir(os.ExpandEnv(workingDir)); err != nil {
		return nil, err
	}

	return rootContainer, nil
}

type CheatCodeAuthConfiguration struct {
	*entraid.AzureCredentials
	// SP  *graphsdk.ServicePrincipal
	MSI *armmsi.Identity
}

func PickOrCreateMSI(ctx context.Context, rootContainer *ioc.NestedContainer, projectName string, subscriptionId string, roleNames []string) (*CheatCodeAuthConfiguration, error) {
	var deps struct {
		prompter       azdext.PromptServiceClient `container:"type"`
		msiService     azd_armmsi.ArmMsiService   `container:"type"`
		entraIdService entraid.EntraIdService     `container:"type"`
	}

	if err := rootContainer.Fill(&deps); err != nil {
		return nil, err
	}

	// ************************** Pick or create a new MSI **************************

	// Prompt for pick or create a new MSI
	selectedOption, err := deps.prompter.Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Do you want to create a new User Managed Identity (MSI) or use an existing one?",
			Choices: []*azdext.SelectChoice{
				{Label: "Create new User Managed Identity (MSI)"},
				{Label: "Use existing User Managed Identity (MSI)"},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("prompting for MSI option: %w", err)
	}

	var msIdentity rm_armmsi.Identity

	if *selectedOption.Value == 0 {
		// pick a resource group and location for the new MSI
		location, err := deps.prompter.PromptLocation(ctx, &azdext.PromptLocationRequest{
			AzureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{
					SubscriptionId: subscriptionId,
				},
			},
		})

		if err != nil {
			return nil, fmt.Errorf("prompting for MSI location: %w", err)
		}

		rg, err := deps.prompter.PromptResourceGroup(ctx, &azdext.PromptResourceGroupRequest{
			AzureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{
					SubscriptionId: subscriptionId,
					Location:       location.Location.Name,
				},
			},
		})

		if err != nil {
			return nil, fmt.Errorf("failed trying to get a resource group name: %w", err)
		}

		displayMsg := fmt.Sprintf("Creating User Managed Identity (MSI) for %s", projectName)

		spinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text: displayMsg,
		})

		err = spinner.Run(ctx, func(ctx context.Context) error {
			// Create a new MSI
			newMSI, err := deps.msiService.CreateUserIdentity(ctx, subscriptionId, rg.ResourceGroup.Name, location.Location.Name, "msi-"+projectName)

			if err != nil {
				return err
			}

			msIdentity = newMSI
			return nil
		})

		if err != nil {
			return &CheatCodeAuthConfiguration{}, fmt.Errorf("failed to create User Managed Identity (MSI): %w", err)
		}
	} else {
		// List existing MSIs and let the user select one
		msIdentities, err := deps.msiService.ListUserIdentities(ctx, subscriptionId)
		if err != nil {
			return nil, fmt.Errorf("failed to list User Managed Identities (MSI): %w", err)
		}
		if len(msIdentities) == 0 {
			return nil, fmt.Errorf("no User Managed Identities (MSI) found in subscription %s", subscriptionId)
		}
		// Prompt the user to select an existing MSI
		msiOptions := make([]string, len(msIdentities))
		choices := make([]*azdext.SelectChoice, len(msIdentities))

		for i, msi := range msIdentities {
			msiData, err := arm.ParseResourceID(*msi.ID)
			if err != nil {
				return nil, fmt.Errorf("parsing MSI resource id: %w", err)
			}
			msiOptions[i] = fmt.Sprintf("%2d. %s (%s)", i+1, *msi.Name, msiData.ResourceGroupName)
			choices[i] = &azdext.SelectChoice{
				Label: msiOptions[i],
			}
		}

		selectedOption, err := deps.prompter.Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message: "Select an existing User Managed Identity (MSI) to use:",
				Choices: choices,
			},
		})

		if err != nil {
			return nil, fmt.Errorf("prompting for existing MSI: %w", err)
		}
		msIdentity = msIdentities[*selectedOption.Value]
	}

	roleNameStrings := strings.Join(roleNames, ", ")

	displayMsg := fmt.Sprintf("Assigning roles (%s) to User Managed Identity (MSI) %s", roleNameStrings, *msIdentity.Name)
	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: displayMsg,
	})

	err = spinner.Run(ctx, func(ctx context.Context) error {
		// ************************** Role Assign **************************
		return deps.entraIdService.EnsureRoleAssignments(
			ctx,
			subscriptionId,
			roleNames,
			// EnsureRoleAssignments uses the ServicePrincipal ID and the DisplayName.
			// We are adapting the MSI to work with the same method as a regular Service Principal, by pulling name and ID.
			&graphsdk.ServicePrincipal{
				Id:          msIdentity.Properties.PrincipalID,
				DisplayName: *msIdentity.Name,
			},
		)
	})

	if err != nil {
		return nil, fmt.Errorf("failed to assign role to User Managed Identity (MSI): %w", err)
	}

	return &CheatCodeAuthConfiguration{
		AzureCredentials: &entraid.AzureCredentials{
			ClientId:       *msIdentity.Properties.ClientID,
			TenantId:       *msIdentity.Properties.TenantID,
			SubscriptionId: subscriptionId,
		},
		MSI: &msIdentity,
	}, nil
}
