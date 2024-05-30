// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type authTokenFlags struct {
	tenantID string
	scopes   []string
	global   *internal.GlobalCommandOptions
}

func newAuthTokenFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *authTokenFlags {
	flags := &authTokenFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newAuthTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "token --output json",
		Hidden: true,
	}
}

func (f *authTokenFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
	local.StringArrayVar(&f.scopes, "scope", nil, "The scope to use when requesting an access token")
	local.StringVar(&f.tenantID, "tenant-id", "", "The tenant id to use when requesting an access token.")
}

type CredentialProviderFn func(context.Context, *auth.CredentialForCurrentUserOptions) (azcore.TokenCredential, error)

type authTokenAction struct {
	credentialProvider CredentialProviderFn
	formatter          output.Formatter
	writer             io.Writer
	envResolver        environment.EnvironmentResolver
	subResolver        account.SubscriptionTenantResolver
	flags              *authTokenFlags
	cloud              *cloud.Cloud
}

func newAuthTokenAction(
	credentialProvider CredentialProviderFn,
	formatter output.Formatter,
	writer io.Writer,
	flags *authTokenFlags,
	envResolver environment.EnvironmentResolver,
	subResolver account.SubscriptionTenantResolver,
	cloud *cloud.Cloud,
) actions.Action {
	return &authTokenAction{
		credentialProvider: credentialProvider,
		envResolver:        envResolver,
		subResolver:        subResolver,
		formatter:          formatter,
		writer:             writer,
		flags:              flags,
		cloud:              cloud,
	}
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

func (a *authTokenAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if len(a.flags.scopes) == 0 {
		a.flags.scopes = auth.LoginScopes(a.cloud)
	}

	var cred azcore.TokenCredential

	// 1) flag --tenant-id is the highest priority. If it is not use, azd will check if subscriptionId is set as env var
	tenantId := a.flags.tenantID
	// 2) From azd env
	if tenantId == "" {
		tenantIdFromAzdEnv, err := getTenantIdFromAzdEnv(ctx, a.envResolver, a.subResolver)
		if err != nil {
			return nil, err
		}
		tenantId = tenantIdFromAzdEnv
	}
	// 3) From system env
	if tenantId == "" {
		tenantIdFromSysEnv, err := getTenantIdFromEnv(ctx, a.subResolver)
		if err != nil {
			return nil, err
		}
		tenantId = tenantIdFromSysEnv
	}

	// If tenantId is still empty, the fallback is to use current logged in user's home-tenant id.
	cred, err := a.credentialProvider(ctx, &auth.CredentialForCurrentUserOptions{
		NoPrompt: true,
		TenantID: tenantId,
	})
	if err != nil {
		return nil, err
	}

	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: a.flags.scopes,
	})
	if err != nil {
		return nil, fmt.Errorf("fetching token: %w", err)
	}

	res := contracts.AuthTokenResult{
		Token:     token.Token,
		ExpiresOn: contracts.RFC3339Time(token.ExpiresOn),
	}

	return nil, a.formatter.Format(res, a.writer, nil)
}
