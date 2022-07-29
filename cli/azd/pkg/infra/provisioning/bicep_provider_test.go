package provisioning

import (
	"context"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestBicepPlan(t *testing.T) {
	azCli := tools.NewAzCli(tools.NewAzCliArgs{})
	projectDir := "../../../test/samples/webapp"
	options := Options{
		Module: "main",
	}

	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")

	console := &mocks.MockConsole{}
	infraProvider := NewBicepProvider(&env, projectDir, options, console, azCli)
	planTask := infraProvider.Plan(context.Background())

	go func() {
		for progressReport := range planTask.Progress() {
			fmt.Println(progressReport.Timestamp)
		}
	}()

	go func() {
		for planInteractive := range planTask.Interactive() {
			fmt.Println(planInteractive)
		}
	}()

	planResult := planTask.Result()

	require.Nil(t, planTask.Error)
	require.NotNil(t, planResult.Plan)

	require.Equal(t, env.Values["AZURE_LOCATION"], planResult.Plan.Parameters["location"].Value)
	require.Equal(t, env.Values["AZURE_ENV_NAME"], planResult.Plan.Parameters["name"].Value)
}

func TestBicepApply(t *testing.T) {
	ctx := context.Background()
	azCli := tools.NewAzCli(tools.NewAzCliArgs{})
	projectDir := "../../../test/samples/webapp"
	options := Options{
		Module: "main",
	}

	console := &mocks.MockConsole{}

	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "westus2"
	env.SetSubscriptionId("faa080af-c1d8-40ad-9cce-e1a450ca5b57")
	env.SetEnvName("wabrez-test-env2")

	scope := NewSubscriptionProvisioningScope(azCli, env.Values["AZURE_LOCATION"], env.GetSubscriptionId(), env.GetEnvName())
	infraProvider := NewBicepProvider(&env, projectDir, options, console, azCli)
	planTask := infraProvider.Plan(ctx)

	go func() {
		for planProgress := range planTask.Progress() {
			fmt.Println(planProgress.Message)
		}
	}()

	go func() {
		for planInteractive := range planTask.Interactive() {
			fmt.Println(planInteractive)
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

	go func() {
		for applyInteractive := range applyTask.Interactive() {
			fmt.Println(applyInteractive)
		}
	}()

	applyResult := applyTask.Result()
	require.NotNil(t, applyResult)
}
