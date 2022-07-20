package provisioning

import (
	"context"
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
	template, err := infraProvider.Compile(context.Background())

	require.Nil(t, err)
	require.NotNil(t, *template)

	require.Equal(t, env.Values["AZURE_LOCATION"], template.Parameters["location"].Value)
	require.Equal(t, env.Values["AZURE_ENV_NAME"], template.Parameters["name"].Value)
}
