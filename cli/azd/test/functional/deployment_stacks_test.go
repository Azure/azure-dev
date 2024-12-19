package cli_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/stretchr/testify/require"
)

func Test_DeploymentStacks(t *testing.T) {
	t.Run("Subscription_Scope_Up_Down", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := newTestContext(t)
		defer cancel()

		dir := tempDirWithDiagnostics(t)
		t.Logf("DIR: %s", dir)

		session := recording.Start(t)

		envName := randomOrStoredEnvName(session)
		t.Logf("AZURE_ENV_NAME: %s", envName)

		cli := azdcli.NewCLI(t, azdcli.WithSession(session))
		cli.WorkingDirectory = dir
		cli.Env = append(cli.Env, os.Environ()...)
		cli.Env = append(
			cli.Env,
			"AZD_ALPHA_ENABLE_DEPLOYMENT_STACKS=true",
			"AZURE_LOCATION=eastus2",
		)

		defer cleanupDeployments(ctx, t, cli, session, envName)

		err := copySample(dir, "storage")
		require.NoError(t, err, "failed expanding sample")

		_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
		require.NoError(t, err)

		provisionResult, err := cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
		require.NoError(t, err)
		require.Contains(t, provisionResult.Stdout, "deployment.stacks")

		env, err := envFromAzdRoot(ctx, dir, envName)
		require.NoError(t, err)

		if session != nil {
			session.Variables[recording.SubscriptionIdKey] = env.GetSubscriptionId()
		}

		result, err := cli.RunCommand(ctx, "down", "--force", "--purge")
		require.NoError(t, err)
		require.Contains(t, result.Stdout, "Deleted subscription deployment stack")
	})

	t.Run("ResourceGroup_Scope_Up_Down", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := newTestContext(t)
		defer cancel()

		dir := tempDirWithDiagnostics(t)
		t.Logf("DIR: %s", dir)

		session := recording.Start(t)
		client := http.DefaultClient
		subscriptionId := cfg.SubscriptionID
		if session != nil {
			client = session.ProxyClient

			if session.Playback {
				subscriptionId = session.Variables[recording.SubscriptionIdKey]
			}
		}

		envName := randomOrStoredEnvName(session)
		t.Logf("AZURE_ENV_NAME: %s", envName)

		location := "eastus2"
		resourceGroupName := fmt.Sprintf("rg-%s", envName)

		cli := azdcli.NewCLI(t, azdcli.WithSession(session))
		cli.WorkingDirectory = dir
		cli.Env = append(cli.Env, os.Environ()...)
		cli.Env = append(
			cli.Env,
			fmt.Sprintf("AZURE_LOCATION=%s", location),
			fmt.Sprintf("AZURE_RESOURCE_GROUP=%s", resourceGroupName),
			"AZD_ALPHA_ENABLE_DEPLOYMENT_STACKS=true",
		)

		cred := azdcli.NewTestCredential(cli)

		rgClient, err := armresources.NewResourceGroupsClient(subscriptionId, cred, &arm.ClientOptions{
			ClientOptions: azcore.ClientOptions{
				Transport: client,
			},
		})
		require.NoError(t, err)

		_, err = rgClient.CreateOrUpdate(context.Background(), resourceGroupName, armresources.ResourceGroup{
			Name:     to.Ptr(resourceGroupName),
			Location: &location,
			Tags: map[string]*string{
				"DeleteAfter": to.Ptr(time.Now().Add(60 * time.Minute).UTC().Format(time.RFC3339)),
			},
		}, nil)

		require.NoError(t, err)

		err = copySample(dir, "storage-rg")
		require.NoError(t, err, "failed expanding sample")

		_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
		require.NoError(t, err)

		provisionResult, err := cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
		require.NoError(t, err)
		require.Contains(t, provisionResult.Stdout, "deployment.stacks")

		env, err := envFromAzdRoot(ctx, dir, envName)
		require.NoError(t, err)

		if session != nil {
			session.Variables[recording.SubscriptionIdKey] = env.GetSubscriptionId()
		}

		result, err := cli.RunCommand(ctx, "down", "--force", "--purge")
		require.NoError(t, err)
		require.Contains(t, result.Stdout, "Deleted resource group deployment stack")
	})
}
