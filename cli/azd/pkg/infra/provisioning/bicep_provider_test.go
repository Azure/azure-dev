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
	projectDir := "../../../test/samples/webapp"
	options := InfrastructureOptions{
		Module: "main",
	}

	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")

	infraProvider := NewBicepInfraProvider(&env, projectDir, options, azCli)
	planTask := infraProvider.Plan(context.Background())

	go func() {
		for progressReport := range planTask.Progress() {
			fmt.Println(progressReport.Timestamp)
		}
	}()

	planResult := planTask.Result()

	require.Nil(t, planTask.Error)
	require.NotNil(t, planResult.Plan)

	require.Equal(t, env.Values["AZURE_LOCATION"], planResult.Plan.Parameters["location"].Value)
	require.Equal(t, env.Values["AZURE_ENV_NAME"], planResult.Plan.Parameters["name"].Value)
}

func TestBicepDeploy(t *testing.T) {
	ctx := context.Background()
	azCli := tools.NewAzCli(tools.NewAzCliArgs{})
	projectDir := "../../../test/samples/webapp"
	options := InfrastructureOptions{
		Module: "main",
	}

	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "westus2"
	env.SetSubscriptionId("faa080af-c1d8-40ad-9cce-e1a450ca5b57")
	env.SetEnvName("wabrez-test-env2")

	scope := NewSubscriptionProvisioningScope(azCli, env.Values["AZURE_LOCATION"], env.GetSubscriptionId(), env.GetEnvName())
	infraProvider := NewBicepInfraProvider(&env, projectDir, options, azCli)
	planTask := infraProvider.Plan(ctx)

	go func() {
		for planProgress := range planTask.Progress() {
			fmt.Println(planProgress.Message)
		}
	}()

	require.Nil(t, planTask.Error)
	planResult := planTask.Result()
	require.NotNil(t, planResult.Plan)

	applyProgressMsg := "Deploying..."
	fmt.Println(applyProgressMsg)
	applyTask := infraProvider.Apply(ctx, &planResult.Plan, scope)

	go func() {
		for applyProgress := range applyTask.Progress() {
			fmt.Println(applyProgress.Timestamp)
		}
	}()

	applyResult := applyTask.Result()
	require.NotNil(t, applyResult)
}
