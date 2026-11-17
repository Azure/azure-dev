// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	bicepprovider "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	coreproject "github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type orderingEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	manager environment.Manager
	name    string
	writes  []string
	failOn  string
}

func (s *orderingEnvironmentServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: s.name}}, nil
}

func (s *orderingEnvironmentServer) GetValues(
	ctx context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	env, err := s.manager.Get(ctx, request.Name)
	if err != nil {
		return nil, err
	}
	result := &azdext.KeyValueListResponse{}
	for key, value := range env.Dotenv() {
		result.KeyValues = append(result.KeyValues, &azdext.KeyValue{Key: key, Value: value})
	}
	return result, nil
}

func (s *orderingEnvironmentServer) SetValue(
	ctx context.Context, request *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	if request.Key == s.failOn {
		return nil, status.Error(codes.Internal, "persisting administrator input failed")
	}
	env, err := s.manager.Get(ctx, request.EnvName)
	if err != nil {
		return nil, err
	}
	env.DotenvSet(request.Key, request.Value)
	if err := s.manager.Save(ctx, env); err != nil {
		return nil, err
	}
	s.writes = append(s.writes, request.Key)
	return &azdext.EmptyResponse{}, nil
}

type orderingPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	console input.Console
	calls   int
	cancel  bool
}

func (s *orderingPromptServer) Prompt(
	ctx context.Context, request *azdext.PromptRequest,
) (*azdext.PromptResponse, error) {
	s.calls++
	if s.cancel && strings.Contains(request.Options.Message, "principal name") {
		return nil, status.Error(codes.Canceled, "administrator selection canceled")
	}
	value, err := s.console.Prompt(ctx, input.ConsoleOptions{
		Message: request.Options.Message, DefaultValue: request.Options.DefaultValue,
	})
	return &azdext.PromptResponse{Value: value}, err
}

type orderingPrincipal struct{}

func (orderingPrincipal) CurrentPrincipalId(context.Context) (string, error) {
	return testSpringClientID, nil
}

func (orderingPrincipal) CurrentPrincipalType(context.Context) (provisioning.PrincipalType, error) {
	return provisioning.UserType, nil
}

func TestBicepProviderResolvesPreparedAdministratorThroughProvisionLifecycle(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	bicepName := "bicep"
	if goruntime.GOOS == "windows" {
		bicepName += ".exe"
	}
	require.NoError(t, os.Mkdir(filepath.Join(configDir, "bin"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "bin", bicepName), []byte("mock executable"), 0o600))
	for _, key := range []string{
		azureSubscriptionIDEnv, "AZURE_LOCATION", databasePasswordEnv, databaseAppPasswordEnv,
		databaseAdministratorNameEnv, databaseAdministratorObjectIDEnv, databaseUserAccessTenantIDEnv,
		"DATABASE_ADMIN_NAME", "DATABASE_ADMIN_OBJECT_ID",
	} {
		clearProcessValue(t, key)
	}
	for _, scenario := range []string{
		"interactive", "configured direct", "no-prompt missing name", "cancel name", "persistence failure",
	} {
		t.Run(scenario, func(t *testing.T) {
			project, _ := automaticPostgresProject(t)
			project.Infra = &azdext.InfraOptions{Provider: "bicep", Path: "infra", Module: "main"}
			yaml := "name: spring-test\ninfra:\n  provider: bicep\n  path: infra\nservices:\n" +
				"  petclinic:\n    project: .\n    language: java\n    host: springbootapp\n    module: app\n"
			require.NoError(t, os.WriteFile(filepath.Join(project.Path, "azure.yaml"), []byte(yaml), 0o600))
			for _, name := range []string{"main.bicep", "app.bicep"} {
				require.NoError(t, os.WriteFile(filepath.Join(project.Path, "infra", name),
					[]byte("param imageName string = ''"), 0o600))
			}
			require.NoError(t, os.WriteFile(filepath.Join(project.Path, "infra", "app.parameters.json"),
				[]byte(`{"parameters":{}}`), 0o600))
			deferred := scenario != "configured direct"
			nameBinding, idBinding := databaseAdministratorNameBinding, databaseAdministratorIDBinding
			if deferred {
				nameBinding, idBinding = databaseAdministratorNameDeferredBinding, databaseAdministratorIDDeferredBinding
			}
			parameters, err := json.Marshal(map[string]any{"parameters": map[string]any{
				"location":                      map[string]string{"value": "${AZURE_LOCATION}"},
				"databaseAdministratorName":     map[string]string{"value": nameBinding},
				"databaseAdministratorObjectId": map[string]string{"value": idBinding},
			}})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(project.Path, "infra", "main.parameters.json"), parameters, 0o600))

			mockContext := mocks.NewMockContext(t.Context())
			azdContext := azdcontext.NewAzdContextWithDirectory(project.Path)
			local := environment.NewLocalFileDataStore(azdContext, config.NewFileConfigManager(config.NewManager()))
			manager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, local, nil)
			require.NoError(t, err)
			require.NoError(t, manager.Save(t.Context(), environment.New("dev")))
			env, err := manager.Get(t.Context(), "dev")
			require.NoError(t, err)
			noPrompt := strings.HasPrefix(scenario, "no-prompt") || scenario == "configured direct"
			if noPrompt {
				env.SetSubscriptionId(testSpringClientID)
				env.SetLocation("eastus")
			}
			if !deferred {
				env.DotenvSet("DATABASE_ADMIN_NAME", "selected@example.test")
				env.DotenvSet("DATABASE_ADMIN_OBJECT_ID", testSpringClientID)
			}
			require.NoError(t, manager.Save(t.Context(), env))
			mockContext.Console.SetNoPromptMode(noPrompt)
			var selections []string
			mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
				selections = append(selections, options.Message)
				return true
			}).Respond(0)
			mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
				return strings.Contains(options.Message, "User object ID")
			}).Respond(testSpringClientID)
			mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
				return strings.Contains(options.Message, "principal name")
			}).Respond("selected@example.test")
			template, err := json.Marshal(azure.ArmTemplate{
				Schema: "https://schema.management.azure.com/schemas/2018-05-01/" +
					"subscriptionDeploymentTemplate.json#",
				ContentVersion: "1.0.0.0",
				Parameters: azure.ArmTemplateParameterDefinitions{
					"location":                      {Type: "string"},
					"databaseAdministratorName":     {Type: "string"},
					"databaseAdministratorObjectId": {Type: "string"},
				},
			})
			require.NoError(t, err)
			mockContext.CommandRunner.When(func(args azdexec.RunArgs, _ string) bool {
				return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "--version"
			}).Respond(azdexec.RunResult{Stdout: fmt.Sprintf("Bicep CLI version %s (test)", bicep.Version.String())})
			mockContext.CommandRunner.When(func(args azdexec.RunArgs, _ string) bool {
				return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "build"
			}).Respond(azdexec.RunResult{Stdout: string(template)})
			accountManager := &mockaccount.MockAccountManager{
				Subscriptions: []account.Subscription{
					{Id: testSpringClientID, Name: "first", TenantId: testSpringClientID},
					{Id: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Name: "second", TenantId: testSpringClientID},
				},
				Locations: []account.Location{
					{Name: "eastus", DisplayName: "East US"}, {Name: "westus", DisplayName: "West US"},
				},
			}
			provider := bicepprovider.NewBicepProvider(nil, bicep.NewCli(mockContext.Console, mockContext.CommandRunner),
				nil, nil, nil, manager, env, mockContext.Console,
				prompt.NewDefaultPrompter(env, mockContext.Console, accountManager, nil, nil, cloud.AzurePublic()),
				orderingPrincipal{}, nil, cloud.AzurePublic(), nil, nil, mockContext.Container)
			accounts := &lifecycleAccountServer{
				principalType: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
				objectID:      testSpringClientID, tenantID: testSpringClientID,
			}
			environments := &orderingEnvironmentServer{manager: manager, name: "dev"}
			if scenario == "persistence failure" {
				environments.failOn = "DATABASE_ADMIN_OBJECT_ID"
			}
			prompts := &orderingPromptServer{console: mockContext.Console, cancel: scenario == "cancel name"}
			client := lifecycleTestClient(t, func(server *grpc.Server) {
				azdext.RegisterEnvironmentServiceServer(server, environments)
				azdext.RegisterAccountServiceServer(server, accounts)
				azdext.RegisterPromptServiceServer(server, prompts)
				azdext.RegisterProjectServiceServer(server, &packagingProjectServer{project: project})
			})
			runtime := NewRuntime(client, noPrompt)
			native, err := coreproject.Parse(t.Context(), yaml)
			require.NoError(t, err)
			native.Path = project.Path
			preparations := 0
			require.NoError(t, native.AddHandler(t.Context(), ext.Event("preprovision"),
				func(ctx context.Context, _ coreproject.ProjectLifecycleEventArgs) error {
					preparations++
					return runtime.PreProvision(ctx, &azdext.ProjectEventArgs{Project: project})
				}))

			require.NoError(t, provider.Initialize(t.Context(), project.Path, provisioning.Options{
				Path: "infra", Module: "main", Mode: provisioning.ModeDeploy,
			}))
			require.Equal(t, testSpringClientID, env.GetSubscriptionId())
			require.Equal(t, "eastus", env.GetLocation())
			require.Zero(t, accounts.principalCalls, "the native provider initializes before extension preparation")
			plans := 0
			provision := func() error {
				return native.Invoke(t.Context(), coreproject.ProjectEventProvision, coreproject.ProjectLifecycleEventArgs{
					Project: native,
				}, func() error {
					parameters, err := provider.Parameters(t.Context())
					if err != nil {
						return err
					}
					plans++
					values := map[string]any{}
					for _, parameter := range parameters {
						values[parameter.Name] = parameter.Value
					}
					require.Equal(t, "selected@example.test", values["databaseAdministratorName"])
					require.Equal(t, testSpringClientID, values["databaseAdministratorObjectId"])
					return nil
				})
			}
			err = provision()
			require.Equal(t, 1, preparations)
			if scenario == "cancel name" || scenario == "no-prompt missing name" || scenario == "persistence failure" {
				require.Error(t, err)
				require.Zero(t, plans, "invalid or canceled input must not reach deployment planning")
				switch scenario {
				case "no-prompt missing name":
					require.ErrorContains(t, err, "name is required in no-prompt mode")
					require.Zero(t, accounts.principalCalls)
					require.Zero(t, prompts.calls)
					require.Empty(t, environments.writes)
				case "cancel name":
					require.ErrorContains(t, err, "administrator selection canceled")
					require.Equal(t, 2, prompts.calls)
					require.Empty(t, environments.writes)
				case "persistence failure":
					require.ErrorContains(t, err, "persisting administrator input failed")
					require.Equal(t, 2, prompts.calls)
					require.Equal(t, []string{"DATABASE_ADMIN_NAME"}, environments.writes)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, plans)
			expectedPrompts := 0
			if !noPrompt {
				expectedPrompts = 2
				require.Len(t, selections, 2)
				require.Contains(t, selections[0], "Subscription")
				require.Contains(t, selections[1], "'location'")
				output := strings.Join(mockContext.Console.Output(), "\n")
				require.NotContains(t, output, "'databaseAdministratorName' infrastructure parameter")
				require.NotContains(t, output, "'databaseAdministratorObjectId' infrastructure parameter")
			} else {
				require.Empty(t, selections)
			}
			require.Equal(t, expectedPrompts, prompts.calls)
			accounts.objectID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
			accounts.tenantID = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
			require.NoError(t, provision(), "standalone/repeat provisioning reuses the saved administrator")
			require.Equal(t, 2, preparations)
			require.Equal(t, 2, plans)
			require.Equal(t, expectedPrompts, prompts.calls)
			reloaded, err := manager.Get(t.Context(), "dev")
			require.NoError(t, err)
			require.Equal(t, accounts.tenantID, reloaded.Getenv(databaseUserAccessTenantIDEnv))
		})
	}
}
