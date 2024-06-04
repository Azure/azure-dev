// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulator

import (

	// Importing for infrastructure provider plugin registrations

	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcloud "github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/spf13/cobra"
)

func accountCommands() *cobra.Command {
	accountGroup := &cobra.Command{
		Use: "account",
	}
	accountGroup.AddCommand(showCmd())
	accountGroup.AddCommand(accessTokenCmd())
	return accountGroup
}

type accountShowOutput struct {
	Id       string `json:"id"`
	TenantId string `json:"tenantId"`
}

func showCmd() *cobra.Command {
	showCmd := &cobra.Command{
		Use: "show",
		RunE: func(cmd *cobra.Command, args []string) error {

			ctx := context.Background()
			rootContainer := ioc.NewNestedContainer(nil)
			ioc.RegisterInstance(rootContainer, ctx)
			registerCommonDependencies(rootContainer)

			var subManager *account.SubscriptionsManager
			if err := rootContainer.Resolve(&subManager); err != nil {
				return err
			}
			var env *environment.Environment
			if err := rootContainer.Resolve(&env); err != nil {
				return err
			}

			subId := env.GetSubscriptionId()
			tenantId, err := subManager.LookupTenant(ctx, subId)
			if err != nil {
				return err
			}
			o := accountShowOutput{
				Id:       subId,
				TenantId: tenantId,
			}
			output, err := json.Marshal(o)
			if err != nil {
				return err
			}
			fmt.Println(string(output))
			return nil
		},
	}
	return showCmd
}

func LoginScopes(cloud *cloud.Cloud) []string {
	resourceManagerUrl := cloud.Configuration.Services[azcloud.ResourceManager].Endpoint
	return []string{
		fmt.Sprintf("%s//.default", resourceManagerUrl),
	}
}

func accessTokenCmd() *cobra.Command {
	accessTokenCmd := &cobra.Command{
		Use: "get-access-token",
		RunE: func(cmd *cobra.Command, args []string) error {

			ctx := context.Background()
			rootContainer := ioc.NewNestedContainer(nil)
			ioc.RegisterInstance(rootContainer, ctx)
			registerCommonDependencies(rootContainer)

			var cloud *cloud.Cloud
			if err := rootContainer.Resolve(&cloud); err != nil {
				return err
			}
			var envResolver environment.EnvironmentResolver
			if err := rootContainer.Resolve(&envResolver); err != nil {
				return err
			}
			var subResolver account.SubscriptionTenantResolver
			if err := rootContainer.Resolve(&subResolver); err != nil {
				return err
			}
			var credentialProvider CredentialProviderFn
			if err := rootContainer.Resolve(&credentialProvider); err != nil {
				return err
			}

			scopes, err := cmd.Flags().GetStringArray("scope")
			if err != nil {
				return err
			}
			if len(scopes) == 0 {
				scopes = auth.LoginScopes(cloud)
			}

			var cred azcore.TokenCredential
			tenantId := cmd.Flag("tenant").Value.String()
			// 2) From azd env
			if tenantId == "" {
				tenantIdFromAzdEnv, err := getTenantIdFromAzdEnv(ctx, envResolver, subResolver)
				if err != nil {
					return err
				}
				tenantId = tenantIdFromAzdEnv
			}
			// 3) From system env
			if tenantId == "" {
				tenantIdFromSysEnv, err := getTenantIdFromEnv(ctx, subResolver)
				if err != nil {
					return err
				}
				tenantId = tenantIdFromSysEnv
			}

			// If tenantId is still empty, the fallback is to use current logged in user's home-tenant id.
			cred, err = credentialProvider(ctx, &auth.CredentialForCurrentUserOptions{
				NoPrompt: true,
				TenantID: tenantId,
			})
			if err != nil {
				return err
			}

			token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
				Scopes: scopes,
			})
			if err != nil {
				return fmt.Errorf("fetching token: %w", err)
			}

			type azEmulateAuthTokenResult struct {
				AccessToken string `json:"accessToken"`
				ExpiresOn   string `json:"expiresOn"`
			}
			res := azEmulateAuthTokenResult{
				AccessToken: token.Token,
				ExpiresOn:   token.ExpiresOn.Format("2006-01-02 15:04:05.000000"),
			}
			output, err := json.Marshal(res)
			if err != nil {
				return err
			}
			fmt.Println(string(output))
			return nil
		},
	}
	accessTokenCmd.Flags().StringP("output", "o", "", "Output format.")
	accessTokenCmd.Flags().StringArray(
		"scope", []string{}, "Space-separated AAD scopes in AAD v2.0. Default to Azure Resource Manager")
	accessTokenCmd.Flags().StringP("tenant", "t", "",
		"Tenant ID for which the token is acquired. Only available for user"+
			" and service principal account, not for MSI or Cloud Shell account.")
	return accessTokenCmd
}

func getTenantIdFromAzdEnv(
	ctx context.Context,
	envResolver environment.EnvironmentResolver,
	subResolver account.SubscriptionTenantResolver) (tenantId string, err error) {
	azdEnv, err := envResolver(ctx)
	if err != nil {
		// No azd env, return empty tenantId
		return tenantId, nil
	}

	subIdAtAzdEnv := azdEnv.GetSubscriptionId()
	if subIdAtAzdEnv == "" {
		// azd env found, but missing or empty subscriptionID
		return tenantId, nil
	}

	tenantId, err = subResolver.LookupTenant(ctx, subIdAtAzdEnv)
	if err != nil {
		return tenantId, fmt.Errorf(
			"resolving the Azure Directory from azd environment (%s): %w",
			azdEnv.Name(),
			err)
	}

	return tenantId, nil
}

func getTenantIdFromEnv(
	ctx context.Context,
	subResolver account.SubscriptionTenantResolver) (tenantId string, err error) {

	subIdAtSysEnv, found := os.LookupEnv(environment.SubscriptionIdEnvVarName)
	if !found {
		// no env var from system
		return tenantId, nil
	}

	tenantId, err = subResolver.LookupTenant(ctx, subIdAtSysEnv)
	if err != nil {
		return tenantId, fmt.Errorf(
			"resolving the Azure Directory from system environment (%s): %w", environment.SubscriptionIdEnvVarName, err)
	}

	return tenantId, nil
}
