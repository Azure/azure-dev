// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	bicepprovider "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	bicepcli "github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentSetValueIsVisibleToBicepParametersInSameCommandContext(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	sourceProjectPath := filepath.Join(
		"..", "..", "test", "functional", "testdata", "samples", "spring-petclinic", "overlay",
	)
	projectPath := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectPath, "infra"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectPath, "azure.yaml"),
		[]byte("name: same-command\n"),
		0o600,
	))
	for _, module := range []string{"main", "petclinic"} {
		parameters, err := os.ReadFile(filepath.Join(sourceProjectPath, "infra", module+".parameters.json"))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(
			filepath.Join(projectPath, "infra", module+".parameters.json"),
			parameters,
			0o600,
		))
		require.NoError(t, os.WriteFile(
			filepath.Join(projectPath, "infra", module+".bicep"),
			[]byte("param placeholder string = ''\n"),
			0o600,
		))
	}
	azdContext := azdcontext.NewAzdContextWithDirectory(projectPath)
	local := environment.NewLocalFileDataStore(
		azdContext,
		config.NewFileConfigManager(config.NewManager()),
	)
	envManager, err := environment.NewManager(
		mockContext.Container,
		azdContext,
		mockContext.Console,
		local,
		nil,
	)
	require.NoError(t, err)
	env, err := envManager.Create(t.Context(), environment.Spec{
		Name:         "same-command",
		Subscription: "00000000-0000-0000-0000-000000000000",
		Location:     "westus2",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, envManager.Delete(context.Background(), env.Name()))
	})

	for key, value := range map[string]string{
		"AZURE_CONTAINER_REGISTRY_NAME":      "registry",
		"AZURE_CONTAINER_ENVIRONMENT_NAME":   "environment",
		"AZURE_CONTAINER_APP_NAME":           "petclinic",
		"SERVICE_PETCLINIC_IDENTITY_ID":      "identity",
		"SERVICE_PETCLINIC_IMAGE_NAME":       "registry/petclinic:test",
		"AZURE_POSTGRES_HOST":                "server.postgres.database.azure.com",
		"AZURE_POSTGRES_DATABASE":            "petclinic",
		"AZURE_POSTGRES_USER":                "petclinicadmin",
		environment.EnvNameEnvVarName:        env.Name(),
		environment.LocationEnvVarName:       "westus2",
		environment.SubscriptionIdEnvVarName: "00000000-0000-0000-0000-000000000000",
	} {
		env.DotenvSet(key, value)
	}
	require.NoError(t, envManager.Save(t.Context(), env))

	service := NewEnvironmentService(lazy.From(azdContext), lazy.From(envManager))
	for key, value := range map[string]string{
		"SPRING_DATABASE_TYPE":     "postgresql",
		"SPRING_DATABASE_PASSWORD": "Azd-SameCommand9!",
	} {
		_, err := service.SetValue(t.Context(), &azdext.SetEnvRequest{
			EnvName: env.Name(),
			Key:     key,
			Value:   value,
		})
		require.NoError(t, err)
	}

	armTemplate := parameterVisibilityTemplate()
	templateBytes, err := json.Marshal(armTemplate)
	require.NoError(t, err)
	mockContext.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		return strings.Contains(args.Cmd, "bicep") && len(args.Args) > 0 && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: fmt.Sprintf("Bicep CLI version %s (abcdef0123)", bicepcli.Version.String()),
	})
	mockContext.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		return strings.Contains(args.Cmd, "bicep") && len(args.Args) > 0 && args.Args[0] == "build"
	}).Respond(exec.RunResult{Stdout: string(templateBytes)})

	for _, module := range []string{"main", "petclinic"} {
		t.Run(module+".parameters.json", func(t *testing.T) {
			provider := bicepprovider.NewBicepProvider(
				nil,
				bicepcli.NewCli(mockContext.Console, mockContext.CommandRunner),
				nil,
				nil,
				nil,
				envManager,
				env,
				mockContext.Console,
				&registrationTestPrompter{},
				&parameterVisibilityPrincipal{},
				nil,
				cloud.AzurePublic(),
				nil,
				nil,
				mockContext.Container,
			)
			require.NoError(t, provider.Initialize(t.Context(), projectPath, provisioning.Options{
				Path:   "infra",
				Module: module,
				Mode:   provisioning.ModeDestroy,
			}))

			parameters, err := provider.Parameters(t.Context())
			require.NoError(t, err)
			require.Equal(t, "postgresql", parameterValue(parameters, "databaseType"))
			require.Equal(t, "Azd-SameCommand9!", parameterValue(parameters, "databasePassword"))
		})
	}
}

func parameterVisibilityTemplate() azure.ArmTemplate {
	definitions := azure.ArmTemplateParameterDefinitions{}
	for _, name := range []string{
		"environmentName",
		"location",
		"containerRegistryName",
		"containerAppsEnvironmentName",
		"identityId",
		"containerAppName",
		"databaseType",
		"postgresHost",
		"postgresDatabase",
		"postgresUser",
		"imageName",
	} {
		definitions[name] = azure.ArmTemplateParameterDefinition{Type: "string", DefaultValue: ""}
	}
	definitions["databasePassword"] = azure.ArmTemplateParameterDefinition{
		Type:         "secureString",
		DefaultValue: "",
	}
	return azure.ArmTemplate{
		Schema: "https://schema.management.azure.com/schemas/" +
			"2019-04-01/deploymentTemplate.json#",
		ContentVersion: "1.0.0.0",
		Parameters:     definitions,
		Outputs:        azure.ArmTemplateOutputs{},
	}
}

func parameterValue(parameters []provisioning.Parameter, name string) any {
	for _, parameter := range parameters {
		if parameter.Name == name {
			return parameter.Value
		}
	}
	return nil
}

type parameterVisibilityPrincipal struct{}

func (*parameterVisibilityPrincipal) CurrentPrincipalId(context.Context) (string, error) {
	return "11111111-1111-1111-1111-111111111111", nil
}

func (*parameterVisibilityPrincipal) CurrentPrincipalType(
	context.Context,
) (provisioning.PrincipalType, error) {
	return provisioning.UserType, nil
}
