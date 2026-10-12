// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	corecmd "github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/internal/commandresult"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mocktools"
	"github.com/mattn/go-colorable"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const serviceEventMessageProject = `name: test-project
services:
  api:
    project: api
    language: js
    host: containerapp
  web:
    project: web
    language: js
    host: containerapp
`

func TestServiceEventMessagesCommands(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("AZD_EXT_DEBUG", "false")
	t.Setenv("AZD_DEBUG", "false")

	for _, command := range []string{"deploy", "up"} {
		for _, format := range []output.Format{output.NoneFormat, output.JsonFormat} {
			t.Run(command+"/"+string(format), func(t *testing.T) {
				type scenario struct {
					name          string
					handlerError  bool
					deployError   bool
					cancel        bool
					postHookError bool
					wantServices  int
				}
				tests := []scenario{
					{name: "completed handlers", wantServices: 2},
					{name: "handler error retains messages", handlerError: true},
					{name: "later deployment failure", deployError: true, wantServices: 1},
					{name: "later cancellation", cancel: true, wantServices: 1},
				}
				if command == "up" {
					tests = append(tests, scenario{
						name: "later postdeploy hook failure", postHookError: true, wantServices: 2,
					})
				}

				for _, test := range tests {
					t.Run(test.name, func(t *testing.T) {
						failure := errors.New("deployment policy check failed")
						fixture := newServiceEventMessageCommandFixture(
							t, command, serviceEventMessageFormatter(format), serviceEventMessageProject,
							func(
								_ context.Context, eventName string, args *azdext.ServiceEventArgs,
							) (*azdext.BetaServiceEventResponse, error) {
								if args.Service.Name != "api" {
									return nil, nil
								}
								response := serviceEventMessageResponse(eventName)
								if test.handlerError && eventName == "postdeploy" {
									return response, failure
								}
								return response, nil
							},
						)
						ctx, cancel := context.WithCancel(t.Context())
						defer cancel()
						fixture.manager.deploy = func(ctx context.Context, serviceName string) error {
							if serviceName == "web" {
								if test.deployError {
									return failure
								}
								if test.cancel {
									cancel()
									return ctx.Err()
								}
							}
							return nil
						}
						if test.postHookError {
							fixture.project.Hooks = map[string][]*ext.HookConfig{
								"postdeploy": {{Shell: "sh", Run: "echo postdeploy", Interactive: true}},
							}
							mocktools.RegisterHookExecutors(fixture.mockContext)
							fixture.mockContext.CommandRunner.When(func(_ exec.RunArgs, command string) bool {
								return strings.Contains(command, "azd-postdeploy-")
							}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
								return exec.NewRunResult(1, "", ""), failure
							})
						}

						result, err := fixture.run(ctx)
						if test.handlerError {
							require.Nil(t, result)
							require.ErrorContains(t, err, failure.Error())
						} else if test.cancel {
							require.Nil(t, result)
							require.ErrorIs(t, err, context.Canceled)
						} else if test.deployError || test.postHookError {
							require.Nil(t, result)
							require.ErrorIs(t, err, failure)
						} else {
							require.NoError(t, err)
							require.NotNil(t, result)
						}

						if format == output.JsonFormat {
							var result corecmd.DeploymentResult
							require.NoError(t, json.Unmarshal(fixture.resultOutput.Bytes(), &result))
							require.Len(t, result.Services, test.wantServices)
							require.Equal(t, expectedServiceEventMessages(), result.Messages)
							require.NotContains(t, fixture.resultOutput.String(), "WARNING:")
							require.NotContains(t, fixture.resultOutput.String(), "\x1b")
							require.NotContains(t, fixture.consoleOutput.String(), "before-deploy guidance")
							require.NotContains(t, fixture.consoleOutput.String(), "after-deploy guidance")
							return
						}

						text := serviceEventMessageOutputText(t, fixture.consoleOutput.String())
						table := strings.LastIndex(text, "\n  Service")
						pre := strings.Index(text, "api (predeploy): before-deploy guidance")
						post := strings.Index(text, "api (postdeploy): after-deploy guidance")
						require.GreaterOrEqual(t, table, 0)
						require.Greater(t, pre, table)
						require.Contains(t, text[table:pre], "Duration")
						require.Greater(t, post, pre)
						require.Equal(t, 1, strings.Count(text, "before-deploy guidance"))
						require.Equal(t, 1, strings.Count(text, "after-deploy guidance"))
						require.NotContains(t, text, "Deployment messages")
						require.Contains(t, text, "Suggestion: Review the access policy.")
						require.Contains(t, text, "https://example.com/access")
						if err == nil {
							endpoint := strings.Index(text, "https://example.com/api")
							require.Greater(t, endpoint, post)
						}
					})
				}
			})
		}
	}
}

func TestServiceEventMessagesCommandsEmptyOutput(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("AZD_EXT_DEBUG", "false")
	t.Setenv("AZD_DEBUG", "false")

	for _, command := range []string{"deploy", "up"} {
		for _, format := range []output.Format{output.NoneFormat, output.JsonFormat} {
			t.Run(command+"/"+string(format), func(t *testing.T) {
				baseline := newServiceEventMessageCommandFixture(
					t, command, serviceEventMessageFormatter(format), serviceEventMessageProject, nil,
				)
				_, err := baseline.run(t.Context())
				require.NoError(t, err)

				for _, emptyResponse := range []struct {
					name     string
					response *azdext.BetaServiceEventResponse
				}{
					{name: "nil response"},
					{name: "nil messages", response: &azdext.BetaServiceEventResponse{}},
					{name: "empty messages", response: &azdext.BetaServiceEventResponse{
						Messages: []azdext.BetaServiceEventMessage{},
					}},
				} {
					t.Run(emptyResponse.name, func(t *testing.T) {
						fixture := newServiceEventMessageCommandFixture(
							t, command, serviceEventMessageFormatter(format), serviceEventMessageProject,
							func(
								context.Context, string, *azdext.ServiceEventArgs,
							) (*azdext.BetaServiceEventResponse, error) {
								return emptyResponse.response, nil
							},
						)
						_, err := fixture.run(t.Context())
						require.NoError(t, err)

						if format == output.JsonFormat {
							var baselineResult, result map[string]any
							require.NoError(t, json.Unmarshal(baseline.resultOutput.Bytes(), &baselineResult))
							require.NoError(t, json.Unmarshal(fixture.resultOutput.Bytes(), &result))
							require.NotContains(t, result, "messages")
							delete(baselineResult, "timestamp")
							delete(result, "timestamp")
							require.Equal(t, baselineResult, result)
							return
						}

						require.Equal(t,
							serviceEventMessageFinalOutput(t, baseline.consoleOutput.String()),
							serviceEventMessageFinalOutput(t, fixture.consoleOutput.String()),
						)
					})
				}
			})
		}
	}
}

func TestServiceEventMessagesCommandsConcurrentOrder(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("AZD_EXT_DEBUG", "false")
	t.Setenv("AZD_DEBUG", "false")

	projectYaml := serviceEventMessageProject + `  worker:
    project: worker
    language: js
    host: containerapp
    uses: [api, web]
`
	for _, command := range []string{"deploy", "up"} {
		for _, format := range []output.Format{output.NoneFormat, output.JsonFormat} {
			t.Run(command+"/"+string(format), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				arrived := make(chan string, 2)
				release := make(chan struct{})
				secondServiceDone := make(chan struct{})
				var serviceOrder []string
				fixture := newServiceEventMessageCommandFixture(
					t, command, serviceEventMessageFormatter(format), projectYaml,
					func(
						ctx context.Context, eventName string, args *azdext.ServiceEventArgs,
					) (*azdext.BetaServiceEventResponse, error) {
						if eventName != "postdeploy" || args.Service.Name == "worker" {
							return nil, nil
						}
						arrived <- args.Service.Name
						select {
						case <-release:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
						if args.Service.Name == serviceOrder[0] {
							select {
							case <-secondServiceDone:
							case <-ctx.Done():
								return nil, ctx.Err()
							}
						}
						return &azdext.BetaServiceEventResponse{
							Messages: []azdext.BetaServiceEventMessage{
								{Kind: azdext.BetaServiceEventMessageWarning, Message: args.Service.Name + " first warning"},
								{Kind: azdext.BetaServiceEventMessageInfo, Message: args.Service.Name + " second message"},
							},
						}, nil
					},
				)
				fixture.manager.completed = func(serviceName string) {
					if serviceName == serviceOrder[1] {
						close(secondServiceDone)
					}
				}
				done := make(chan error, 1)
				go func() {
					_, err := fixture.run(ctx)
					done <- err
					close(done)
				}()
				t.Cleanup(func() {
					cancel()
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("command did not stop")
					}
				})

				select {
				case serviceOrder = <-fixture.serviceOrder:
					require.Len(t, serviceOrder, 3)
					require.Equal(t, "worker", serviceOrder[2])
				case err := <-done:
					t.Fatalf("command stopped before service initialization: %v", err)
				case <-ctx.Done():
					t.Fatal("command did not initialize its services")
				}

				var serviceNames []string
				for range 2 {
					select {
					case serviceName := <-arrived:
						serviceNames = append(serviceNames, serviceName)
					case err := <-done:
						t.Fatalf("command completed before both handlers arrived: %v", err)
					case <-ctx.Done():
						t.Fatal("both service handlers must be in flight")
					}
				}
				require.ElementsMatch(t, []string{"api", "web"}, serviceNames)
				close(release)
				select {
				case err := <-done:
					require.NoError(t, err)
				case <-ctx.Done():
					t.Fatal("command did not finish after releasing its handlers")
				}

				if format == output.JsonFormat {
					var result corecmd.DeploymentResult
					require.NoError(t, json.Unmarshal(fixture.resultOutput.Bytes(), &result))
					require.Len(t, result.Messages, 4)
					require.Equal(t, []string{
						serviceOrder[0], serviceOrder[0], serviceOrder[1], serviceOrder[1],
					}, []string{
						result.Messages[0].ServiceName, result.Messages[1].ServiceName,
						result.Messages[2].ServiceName, result.Messages[3].ServiceName,
					})
					for _, message := range result.Messages {
						require.Equal(t, "postdeploy", message.EventName)
						require.Equal(t, "test.extension", message.ExtensionID)
					}
					for index, serviceName := range serviceOrder[:2] {
						require.Equal(t, serviceName+" first warning", result.Messages[index*2].Message)
						require.Equal(t, serviceName+" second message", result.Messages[index*2+1].Message)
					}
					return
				}

				text := serviceEventMessageOutputText(t, fixture.consoleOutput.String())
				table := strings.LastIndex(text, "\n  Service")
				previous := table
				require.GreaterOrEqual(t, table, 0)
				for _, serviceName := range serviceOrder[:2] {
					for _, suffix := range []string{" first warning", " second message"} {
						message := serviceName + " (postdeploy): " + serviceName + suffix
						index := strings.Index(text, message)
						require.Greater(t, index, previous)
						require.Equal(t, 1, strings.Count(text, message))
						previous = index
					}
				}
				var tableOrder []string
				messageStart := strings.Index(text, serviceOrder[0]+" (postdeploy):")
				for line := range strings.SplitSeq(text[table:messageStart], "\n") {
					fields := strings.Fields(line)
					if len(fields) >= 3 && fixture.project.Services[fields[1]] != nil {
						tableOrder = append(tableOrder, fields[1])
					}
				}
				require.Equal(t, serviceOrder, tableOrder)
			})
		}
	}
}

func TestServiceEventMessagesCommandsJSONQuery(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("AZD_EXT_DEBUG", "false")
	t.Setenv("AZD_DEBUG", "false")
	for _, command := range []string{"deploy", "up"} {
		t.Run(command, func(t *testing.T) {
			fixture := newServiceEventMessageCommandFixture(
				t, command, &output.JsonFormatter{Query: "messages[?kind=='warning'].message"},
				serviceEventMessageProject,
				func(
					_ context.Context, eventName string, args *azdext.ServiceEventArgs,
				) (*azdext.BetaServiceEventResponse, error) {
					if args.Service.Name != "api" {
						return nil, nil
					}
					return serviceEventMessageResponse(eventName), nil
				},
			)
			_, err := fixture.run(t.Context())
			require.NoError(t, err)
			var messages []string
			require.NoError(t, json.Unmarshal(fixture.resultOutput.Bytes(), &messages))
			require.Equal(t, []string{"after-deploy guidance"}, messages)
		})
	}
}

func TestServiceEventMessagesCommandsJSONFormattingFailure(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("AZD_EXT_DEBUG", "false")
	t.Setenv("AZD_DEBUG", "false")
	for _, command := range []string{"deploy", "up"} {
		for _, deploymentFailed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/failed=%t", command, deploymentFailed), func(t *testing.T) {
				fixture := newServiceEventMessageCommandFixture(
					t, command, &output.JsonFormatter{Query: "["}, serviceEventMessageProject,
					func(
						_ context.Context, eventName string, args *azdext.ServiceEventArgs,
					) (*azdext.BetaServiceEventResponse, error) {
						if args.Service.Name != "api" {
							return nil, nil
						}
						return serviceEventMessageResponse(eventName), nil
					},
				)
				failure := errors.New("deployment policy check failed")
				if deploymentFailed {
					fixture.manager.deploy = func(_ context.Context, serviceName string) error {
						if serviceName == "web" {
							return failure
						}
						return nil
					}
				}

				result, err := fixture.run(t.Context())
				require.Nil(t, result)
				require.ErrorContains(t, err, command+" result could not be displayed:")
				require.ErrorContains(t, err, "SyntaxError")
				if deploymentFailed {
					require.ErrorIs(t, err, failure)
				}
				require.Empty(t, fixture.resultOutput.String())
			})
		}
	}
}

type serviceEventMessageCommandFixture struct {
	run           func(context.Context) (*actions.ActionResult, error)
	project       *project.ProjectConfig
	manager       *serviceEventMessageServiceManager
	mockContext   *mocks.MockContext
	consoleOutput bytes.Buffer
	resultOutput  bytes.Buffer
	serviceOrder  <-chan []string
}

type serviceEventMessageConsoleDecorator func(input.Console, *bytes.Buffer) input.Console

func newServiceEventMessageCommandFixture(
	t *testing.T,
	command string,
	formatter output.Formatter,
	projectYaml string,
	handler func(context.Context, string, *azdext.ServiceEventArgs) (*azdext.BetaServiceEventResponse, error),
	consoleDecorators ...serviceEventMessageConsoleDecorator,
) *serviceEventMessageCommandFixture {
	t.Helper()
	require.Contains(t, []string{"deploy", "up"}, command)
	projectConfig, err := project.Parse(t.Context(), projectYaml)
	require.NoError(t, err)
	projectConfig.Path = t.TempDir()
	serviceOrder := make(chan []string, 1)
	fixture := &serviceEventMessageCommandFixture{
		project:      projectConfig,
		manager:      &serviceEventMessageServiceManager{},
		mockContext:  mocks.NewMockContext(t.Context()),
		serviceOrder: serviceOrder,
	}
	env := environment.New("test-env")
	env.SetSubscriptionId("subscription-id")
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Reload", mock.Anything, env).Return(nil).Maybe()
	envManager.On("InvalidateEnvCache", mock.Anything, env.Name()).Return(nil).Maybe()
	projectManager := &serviceEventMessageProjectManager{initialized: serviceOrder}
	projectManager.On("InitializeServices", mock.Anything).Return(nil).Once()
	projectManager.On("EnsureServiceTargetTools", mock.Anything).Return(nil).Once()
	t.Cleanup(func() {
		projectManager.AssertExpectations(t)
		envManager.AssertExpectations(t)
	})
	var console input.Console = &serviceEventMessageConsole{Console: input.NewConsole(
		true, false,
		input.Writers{Output: &fixture.consoleOutput},
		input.ConsoleHandles{Stdin: strings.NewReader(""), Stdout: &fixture.consoleOutput, Stderr: io.Discard},
		formatter, nil,
	)}
	for _, decorate := range consoleDecorators {
		console = decorate(console, &fixture.consoleOutput)
	}
	importManager := project.NewImportManager(nil)
	if command == "deploy" {
		cmd := corecmd.NewDeployCmd()
		flags := corecmd.NewDeployFlags(cmd, &internal.GlobalCommandOptions{})
		require.NoError(t, cmd.ParseFlags([]string{"--all"}))
		action := corecmd.NewDeployAction(
			flags, nil, projectConfig, projectManager, fixture.manager,
			&serviceEventMessageResourceManager{}, nil, env, envManager,
			nil, cloud.AzurePublic(), nil, fixture.mockContext.CommandRunner,
			console, formatter, &fixture.resultOutput,
			fixture.mockContext.AlphaFeaturesManager, importManager,
		)
		fixture.run = action.Run
	} else {
		action := corecmd.NewUpGraphAction(
			projectConfig, env, envManager, console, fixture.mockContext.AlphaFeaturesManager,
			importManager, fixture.manager, projectManager, fixture.mockContext.Container,
			nil, nil, cloud.AzurePublic(), fixture.mockContext.CommandRunner,
			formatter, &fixture.resultOutput, &provisioning.Manager{},
		)
		fixture.run = func(ctx context.Context) (*actions.ActionResult, error) {
			return action.Run(ctx, nil, nil, nil, time.Now())
		}
	}

	if handler == nil {
		return fixture
	}
	extension := &extensions.Extension{
		Id: "test.extension", Version: "1.0.0", DisplayName: "Test Extension",
		Capabilities: []extensions.CapabilityType{extensions.LifecycleEventsCapability},
	}
	ready := make(chan struct{})
	eventService := grpcserver.NewEventService(
		&serviceEventMessageExtensionLookup{extension: extension},
		lazy.NewLazy(func() (environment.Manager, error) { return envManager, nil }),
		lazy.NewLazy(func() (*project.ProjectConfig, error) { return projectConfig, nil }),
		lazy.NewLazy(func() (*environment.Environment, error) { return env, nil }),
		grpcserver.NewFollowUpManager(),
		mockinput.NewMockConsole(),
	)
	server := grpcserver.NewServer(
		azdext.UnimplementedProjectServiceServer{},
		azdext.UnimplementedEnvironmentServiceServer{},
		azdext.UnimplementedPromptServiceServer{},
		azdext.UnimplementedUserConfigServiceServer{},
		azdext.UnimplementedDeploymentServiceServer{},
		eventService,
		v1beta.UnimplementedComposeServiceServer{},
		azdext.UnimplementedWorkflowServiceServer{},
		&serviceEventMessageExtensionServer{ready: ready},
		azdext.UnimplementedServiceTargetServiceServer{},
		azdext.UnimplementedFrameworkServiceServer{},
		azdext.UnimplementedContainerServiceServer{},
		azdext.UnimplementedAccountServiceServer{},
		azdext.UnimplementedAiModelServiceServer{},
		v1beta.UnimplementedCopilotServiceServer{},
		azdext.UnimplementedProvisioningServiceServer{},
		azdext.UnimplementedValidationServiceServer{},
		v1beta.UnimplementedTelemetryServiceServer{},
		v1beta.UnimplementedCommandResultServiceServer{},
	)
	serverInfo, err := server.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Stop()) })
	client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	token, err := grpcserver.GenerateExtensionToken(extension, serverInfo)
	require.NoError(t, err)
	host := azdext.NewExtensionHost(client)
	for _, eventName := range []string{"predeploy", "postdeploy"} {
		host.WithBetaServiceEventHandler(eventName, func(
			ctx context.Context, args *azdext.ServiceEventArgs,
		) (*azdext.BetaServiceEventResponse, error) {
			return handler(ctx, eventName, args)
		}, nil)
	}
	hostCtx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	done := make(chan error, 1)
	go func() {
		done <- host.Run(azdext.WithAccessToken(hostCtx, token))
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("beta extension host did not stop")
		}
	})
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("beta extension host stopped before registering handlers: %v", err)
	case <-hostCtx.Done():
		t.Fatal("beta extension host did not become ready")
	}
	return fixture
}

type serviceEventMessageServiceManager struct {
	project.ServiceManager
	deploy    func(context.Context, string) error
	completed func(string)
}

func (*serviceEventMessageServiceManager) Package(
	context.Context,
	*project.ServiceConfig,
	*project.ServiceContext,
	*async.Progress[project.ServiceProgress],
	*project.PackageOptions,
) (*project.ServicePackageResult, error) {
	return &project.ServicePackageResult{}, nil
}

func (*serviceEventMessageServiceManager) Publish(
	context.Context,
	*project.ServiceConfig,
	*project.ServiceContext,
	*async.Progress[project.ServiceProgress],
	*project.PublishOptions,
) (*project.ServicePublishResult, error) {
	return &project.ServicePublishResult{}, nil
}

func (m *serviceEventMessageServiceManager) Deploy(
	ctx context.Context,
	service *project.ServiceConfig,
	serviceContext *project.ServiceContext,
	_ *async.Progress[project.ServiceProgress],
) (*project.ServiceDeployResult, error) {
	err := service.Invoke(ctx, project.ServiceEventDeploy, project.ServiceLifecycleEventArgs{
		Project: service.Project, Service: service, ServiceContext: serviceContext,
	}, func() error {
		if m.deploy != nil {
			return m.deploy(ctx, service.Name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if m.completed != nil {
		m.completed(service.Name)
	}
	return &project.ServiceDeployResult{
		Artifacts: project.ArtifactCollection{{
			Kind: project.ArtifactKindEndpoint, Location: "https://example.com/" + service.Name,
			LocationKind: project.LocationKindRemote,
		}},
	}, nil
}

type serviceEventMessageProjectManager struct {
	project.ProjectManager
	mock.Mock
	initialized chan<- []string
}

func (manager *serviceEventMessageProjectManager) InitializeServices(
	_ context.Context, services []*project.ServiceConfig,
) error {
	err := manager.Called(services).Error(0)
	if err != nil {
		return err
	}
	order := make([]string, len(services))
	for index, service := range services {
		order[index] = service.Name
	}
	manager.initialized <- order
	return nil
}

func (manager *serviceEventMessageProjectManager) EnsureServiceTargetTools(
	_ context.Context, services []*project.ServiceConfig,
) error {
	return manager.Called(services).Error(0)
}

type serviceEventMessageResourceManager struct {
	project.ResourceManager
}

func (*serviceEventMessageResourceManager) GetResourceGroupName(
	context.Context, string, osutil.ExpandableString,
) (string, error) {
	return "test-group", nil
}

type serviceEventMessageExtensionLookup struct {
	extension *extensions.Extension
}

func (lookup *serviceEventMessageExtensionLookup) GetInstalled(
	filter extensions.FilterOptions,
) (*extensions.Extension, error) {
	if filter.Id != lookup.extension.Id {
		return nil, fmt.Errorf("unexpected extension %q", filter.Id)
	}
	return lookup.extension, nil
}

type serviceEventMessageExtensionServer struct {
	azdext.UnimplementedExtensionServiceServer
	ready chan struct{}
}

func (server *serviceEventMessageExtensionServer) Ready(
	context.Context, *azdext.ReadyRequest,
) (*azdext.ReadyResponse, error) {
	close(server.ready)
	return &azdext.ReadyResponse{}, nil
}

// Enable the graph table without installing terminal signal watchers.
type serviceEventMessageConsole struct {
	input.Console
}

func (*serviceEventMessageConsole) IsSpinnerInteractive() bool {
	return true
}

func serviceEventMessageFormatter(format output.Format) output.Formatter {
	if format == output.JsonFormat {
		return &output.JsonFormatter{}
	}
	return &output.NoneFormatter{}
}

func serviceEventMessageResponse(eventName string) *azdext.BetaServiceEventResponse {
	if eventName == "predeploy" {
		return &azdext.BetaServiceEventResponse{Messages: []azdext.BetaServiceEventMessage{{
			Kind: azdext.BetaServiceEventMessageInfo, Message: "before-deploy guidance",
		}}}
	}
	return &azdext.BetaServiceEventResponse{Messages: []azdext.BetaServiceEventMessage{{
		Kind: azdext.BetaServiceEventMessageWarning, Message: "after-deploy guidance",
		Suggestion: "Review the access policy.",
		Links:      []errorhandler.ErrorLink{{Title: "Access guide", URL: "https://example.com/access"}},
	}}}
}

func expectedServiceEventMessages() []commandresult.ServiceEventMessage {
	return []commandresult.ServiceEventMessage{
		{
			ExtensionID: "test.extension", ServiceName: "api", EventName: "predeploy",
			Kind: "info", Message: "before-deploy guidance",
		},
		{
			ExtensionID: "test.extension", ServiceName: "api", EventName: "postdeploy",
			Kind: "warning", Message: "after-deploy guidance", Suggestion: "Review the access policy.",
			Links: []commandresult.ServiceEventMessageLink{{Title: "Access guide", URL: "https://example.com/access"}},
		},
	}
}

func serviceEventMessageFinalOutput(t *testing.T, text string) string {
	t.Helper()
	text = serviceEventMessageOutputText(t, text)
	table := strings.LastIndex(text, "\n  Service")
	require.GreaterOrEqual(t, table, 0)
	duration := regexp.MustCompile(`(?m)(\s(?:Done|Failed|Skipped)) +(?:[0-9hms.]+)?$`)
	return duration.ReplaceAllString(text[table:], "$1")
}

func serviceEventMessageOutputText(t *testing.T, text string) string {
	t.Helper()
	var writer bytes.Buffer
	_, err := io.Copy(colorable.NewNonColorable(&writer), strings.NewReader(text))
	require.NoError(t, err)
	return writer.String()
}
