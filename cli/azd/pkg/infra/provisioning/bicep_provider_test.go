package provisioning

import (
	"context"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/stretchr/testify/require"
)

func TestBicepCompile(t *testing.T) {
	azCli := tools.NewAzCli(tools.NewAzCliArgs{})
	bicepCli := tools.NewBicepCli(azCli)
	projectDir := "../../../test/samples/webapp"
	options := InfrastructureOptions{
		Module: "main",
	}

	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")

	infraProvider := NewBicepInfraProvider(&env, projectDir, options, bicepCli, azCli)
	template, err := infraProvider.Plan(context.Background())

	require.Nil(t, err)
	require.NotNil(t, *template)

	require.Equal(t, env.Values["AZURE_LOCATION"], template.Parameters["location"].Value)
	require.Equal(t, env.Values["AZURE_ENV_NAME"], template.Parameters["name"].Value)
}

func TestBicepDeploy(t *testing.T) {
	ctx := context.Background()
	azCli := tools.NewAzCli(tools.NewAzCliArgs{})
	bicepCli := tools.NewBicepCli(azCli)
	projectDir := "../../../test/samples/webapp"
	options := InfrastructureOptions{
		Module: "main",
	}

	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "westus2"
	env.SetSubscriptionId("faa080af-c1d8-40ad-9cce-e1a450ca5b57")
	env.SetEnvName("wabrez-test-env2")

	scope := NewSubscriptionProvisioningScope(azCli, env.Values["AZURE_LOCATION"], env.GetSubscriptionId(), env.GetEnvName())
	infraProvider := NewBicepInfraProvider(&env, projectDir, options, bicepCli, azCli)
	template, err := infraProvider.Plan(ctx)

	require.Nil(t, err)
	require.NotNil(t, *template)

	progressMsg := "Deploying..."
	fmt.Println(progressMsg)
	deployChannel, progressChannel := infraProvider.Apply(ctx, template, scope)

	go func() {
		for progressReport := range progressChannel {
			fmt.Println(progressReport.Timestamp)
		}
	}()

	result := <-deployChannel
	require.NotNil(t, result)
}
