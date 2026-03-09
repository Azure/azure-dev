// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"io"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestDeployFlagsTimeoutFlag(t *testing.T) {
	cmd := NewDeployCmd()
	flags := NewDeployFlags(cmd, &internal.GlobalCommandOptions{})

	timeoutField := reflect.ValueOf(flags).Elem().FieldByName("Timeout")
	if !timeoutField.IsValid() {
		t.Skip("deploy timeout feature is not available on this branch")
	}

	timeoutFlag := cmd.Flags().Lookup("timeout")
	if timeoutFlag == nil {
		t.Skip("deploy timeout flag is not available on this branch")
	}

	require.Equal(t, "1200", timeoutFlag.DefValue)

	tests := []struct {
		name string
		args []string
		want int
	}{
		{
			name: "DefaultValue",
			args: []string{"--all"},
			want: 1200,
		},
		{
			name: "ExplicitValue",
			args: []string{"--all", "--timeout", "45"},
			want: 45,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewDeployCmd()
			flags := NewDeployFlags(cmd, &internal.GlobalCommandOptions{})

			require.NoError(t, cmd.ParseFlags(tt.args))
			require.Equal(t, tt.want, deployFlagsTimeoutValue(t, flags))
		})
	}
}

func TestDeployActionResolveDeployTimeout(t *testing.T) {
	cmd := NewDeployCmd()
	flags := NewDeployFlags(cmd, &internal.GlobalCommandOptions{})

	if reflect.ValueOf(flags).Elem().FieldByName("Timeout").IsValid() == false {
		t.Skip("deploy timeout feature is not available on this branch")
	}

	if cmd.Flags().Lookup("timeout") == nil {
		t.Skip("deploy timeout flag is not available on this branch")
	}

	tests := []struct {
		name        string
		flagTimeout *int
		wantTimeout time.Duration
		wantErr     bool
	}{
		{
			name:        "DefaultTimeout",
			wantTimeout: 1200 * time.Second,
		},
		{
			name:        "ZeroFlagReturnsError",
			flagTimeout: intPtr(0),
			wantErr:     true,
		},
		{
			name:        "NegativeFlagReturnsError",
			flagTimeout: intPtr(-10),
			wantErr:     true,
		},
		{
			name:        "LargeFlagTimeout",
			flagTimeout: intPtr(7200),
			wantTimeout: 7200 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := newDeployTimeoutAction(t, tt.flagTimeout)

			timeout, err := action.resolveDeployTimeout()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantTimeout, timeout)
		})
	}
}

func TestDeployActionRunAppliesResolvedTimeout(t *testing.T) {
	tests := []struct {
		name        string
		flagTimeout *int
		wantTimeout time.Duration
	}{
		{
			name:        "DefaultTimeout",
			wantTimeout: 1200 * time.Second,
		},
		{
			name:        "ExplicitValue",
			flagTimeout: intPtr(30),
			wantTimeout: 30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deployErr := mockDeployErr(t.Name())
			action, serviceManager := newDeployActionForTimeoutTest(t, tt.flagTimeout, deployErr)

			_, err := action.Run(context.Background())
			require.ErrorIs(t, err, deployErr)

			require.True(t, serviceManager.deployHasDeadline, "deploy should run with a deadline")
			require.WithinDuration(t, time.Now().Add(tt.wantTimeout), serviceManager.deployDeadline, 2*time.Second)
			serviceManager.AssertExpectations(t)
		})
	}
}

type mockDeployProjectManager struct {
	mock.Mock
}

func (m *mockDeployProjectManager) Initialize(ctx context.Context, projectConfig *project.ProjectConfig) error {
	args := m.Called(projectConfig)
	return args.Error(0)
}

func (m *mockDeployProjectManager) DefaultServiceFromWd(
	ctx context.Context,
	projectConfig *project.ProjectConfig,
) (*project.ServiceConfig, error) {
	args := m.Called(projectConfig)

	service, _ := args.Get(0).(*project.ServiceConfig)
	return service, args.Error(1)
}

func (m *mockDeployProjectManager) EnsureAllTools(
	ctx context.Context,
	projectConfig *project.ProjectConfig,
	serviceFilterFn project.ServiceFilterPredicate,
) error {
	return nil
}

func (m *mockDeployProjectManager) EnsureFrameworkTools(
	ctx context.Context,
	projectConfig *project.ProjectConfig,
	serviceFilterFn project.ServiceFilterPredicate,
) error {
	return nil
}

func (m *mockDeployProjectManager) EnsureServiceTargetTools(
	ctx context.Context,
	projectConfig *project.ProjectConfig,
	serviceFilterFn project.ServiceFilterPredicate,
) error {
	args := m.Called(projectConfig)
	return args.Error(0)
}

func (m *mockDeployProjectManager) EnsureRestoreTools(
	ctx context.Context,
	projectConfig *project.ProjectConfig,
	serviceFilterFn project.ServiceFilterPredicate,
) error {
	return nil
}

type mockDeployServiceManager struct {
	mock.Mock

	deployDeadline    time.Time
	deployHasDeadline bool
	deployErr         error
}

func (m *mockDeployServiceManager) GetRequiredTools(
	ctx context.Context,
	serviceConfig *project.ServiceConfig,
) ([]tools.ExternalTool, error) {
	return nil, nil
}

func (m *mockDeployServiceManager) Initialize(ctx context.Context, serviceConfig *project.ServiceConfig) error {
	return nil
}

func (m *mockDeployServiceManager) Restore(
	ctx context.Context,
	serviceConfig *project.ServiceConfig,
	serviceContext *project.ServiceContext,
	progress *async.Progress[project.ServiceProgress],
) (*project.ServiceRestoreResult, error) {
	return nil, nil
}

func (m *mockDeployServiceManager) Build(
	ctx context.Context,
	serviceConfig *project.ServiceConfig,
	serviceContext *project.ServiceContext,
	progress *async.Progress[project.ServiceProgress],
) (*project.ServiceBuildResult, error) {
	return nil, nil
}

func (m *mockDeployServiceManager) Package(
	ctx context.Context,
	serviceConfig *project.ServiceConfig,
	serviceContext *project.ServiceContext,
	progress *async.Progress[project.ServiceProgress],
	options *project.PackageOptions,
) (*project.ServicePackageResult, error) {
	return &project.ServicePackageResult{}, nil
}

func (m *mockDeployServiceManager) Publish(
	ctx context.Context,
	serviceConfig *project.ServiceConfig,
	serviceContext *project.ServiceContext,
	progress *async.Progress[project.ServiceProgress],
	publishOptions *project.PublishOptions,
) (*project.ServicePublishResult, error) {
	return &project.ServicePublishResult{}, nil
}

func (m *mockDeployServiceManager) Deploy(
	ctx context.Context,
	serviceConfig *project.ServiceConfig,
	serviceContext *project.ServiceContext,
	progress *async.Progress[project.ServiceProgress],
) (*project.ServiceDeployResult, error) {
	m.deployDeadline, m.deployHasDeadline = ctx.Deadline()
	m.Called(serviceConfig.Name)

	if m.deployErr != nil {
		return nil, m.deployErr
	}

	return &project.ServiceDeployResult{}, nil
}

func (m *mockDeployServiceManager) GetTargetResource(
	ctx context.Context,
	serviceConfig *project.ServiceConfig,
	serviceTarget project.ServiceTarget,
) (*environment.TargetResource, error) {
	return nil, nil
}

func (m *mockDeployServiceManager) GetFrameworkService(
	ctx context.Context,
	serviceConfig *project.ServiceConfig,
) (project.FrameworkService, error) {
	return nil, nil
}

func (m *mockDeployServiceManager) GetServiceTarget(
	ctx context.Context,
	serviceConfig *project.ServiceConfig,
) (project.ServiceTarget, error) {
	return nil, nil
}

func newDeployActionForTimeoutTest(
	t *testing.T,
	flagTimeout *int,
	deployErr error,
) (*DeployAction, *mockDeployServiceManager) {
	t.Helper()

	action := newDeployTimeoutAction(t, flagTimeout)
	projectManager := &mockDeployProjectManager{}
	projectManager.On("Initialize", action.projectConfig).Return(nil).Once()
	projectManager.On("EnsureServiceTargetTools", action.projectConfig).Return(nil).Once()
	t.Cleanup(func() {
		projectManager.AssertExpectations(t)
	})

	serviceManager := &mockDeployServiceManager{deployErr: deployErr}
	serviceManager.On("Deploy", "api").Return().Once()

	action.projectManager = projectManager
	action.serviceManager = serviceManager
	return action, serviceManager
}

func newDeployTimeoutAction(t *testing.T, flagTimeout *int) *DeployAction {
	t.Helper()

	projectConfig := deployTimeoutTestProjectConfig(t)

	cmd := NewDeployCmd()
	flags := NewDeployFlags(cmd, &internal.GlobalCommandOptions{})
	args := []string{"--all"}
	if flagTimeout != nil {
		args = append(args, "--timeout", intToString(*flagTimeout))
	}

	require.NoError(t, cmd.ParseFlags(args))

	env := environment.New("test-env")
	env.SetSubscriptionId("subscription-id")

	return &DeployAction{
		flags:         flags,
		projectConfig: projectConfig,
		env:           env,
		importManager: project.NewImportManager(nil),
		console:       mockinput.NewMockConsole(),
		formatter:     &output.NoneFormatter{},
		writer:        io.Discard,
	}
}

func deployTimeoutTestProjectConfig(t *testing.T) *project.ProjectConfig {
	t.Helper()

	projectConfig, err := project.Parse(
		context.Background(),
		"name: test-proj\nservices:\n  api:\n    project: src/api\n    language: js\n    host: containerapp\n",
	)
	require.NoError(t, err)

	return projectConfig
}

func deployFlagsTimeoutValue(t *testing.T, flags *DeployFlags) int {
	t.Helper()

	field := reflect.ValueOf(flags).Elem().FieldByName("Timeout")
	if !field.IsValid() {
		t.Skip("deploy timeout feature is not available on this branch")
	}

	require.Equal(t, reflect.Int, field.Kind())
	return int(field.Int())
}

func intPtr(value int) *int {
	return &value
}

func intToString(value int) string {
	return strconv.Itoa(value)
}

func mockDeployErr(name string) error {
	return &mockDeployError{name: name}
}

type mockDeployError struct {
	name string
}

func (e *mockDeployError) Error() string {
	return e.name
}
