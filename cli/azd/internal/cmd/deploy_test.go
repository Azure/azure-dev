// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"io"
	"strconv"
	"strings"
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
	NewDeployFlags(cmd, &internal.GlobalCommandOptions{})

	timeoutFlag := cmd.Flags().Lookup("timeout")
	require.NotNil(t, timeoutFlag)
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
			require.Equal(t, tt.want, flags.Timeout)
		})
	}
}

func TestDeployActionResolveDeployTimeout(t *testing.T) {
	tests := []struct {
		name          string
		configTimeout *int
		flagTimeout   *int
		wantTimeout   time.Duration
		wantErr       bool
	}{
		{
			name:        "DefaultTimeout",
			wantTimeout: 1200 * time.Second,
		},
		{
			name:          "ConfigTimeout",
			configTimeout: intPtr(45),
			wantTimeout:   45 * time.Second,
		},
		{
			name:          "FlagOverridesConfig",
			configTimeout: intPtr(90),
			flagTimeout:   intPtr(30),
			wantTimeout:   30 * time.Second,
		},
		{
			name:          "ZeroConfigReturnsError",
			configTimeout: intPtr(0),
			wantErr:       true,
		},
		{
			name:          "NegativeConfigReturnsError",
			configTimeout: intPtr(-10),
			wantErr:       true,
		},
		{
			name:          "ZeroFlagReturnsError",
			configTimeout: intPtr(90),
			flagTimeout:   intPtr(0),
			wantErr:       true,
		},
		{
			name:          "NegativeFlagReturnsError",
			configTimeout: intPtr(90),
			flagTimeout:   intPtr(-10),
			wantErr:       true,
		},
		{
			name:        "LargeFlagTimeout",
			flagTimeout: intPtr(7200),
			wantTimeout: 7200 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := newDeployTimeoutAction(t, tt.configTimeout, tt.flagTimeout)

			timeout, err := action.resolveDeployTimeout(action.projectConfig.Services["api"])
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantTimeout, timeout)
		})
	}
}

func TestDeployActionResolveDeployTimeoutEnvVar(t *testing.T) {
	tests := []struct {
		name          string
		envValue      string
		configTimeout *int
		flagTimeout   *int
		wantTimeout   time.Duration
		wantErr       bool
	}{
		{
			name:        "EnvVarTimeout",
			envValue:    "60",
			wantTimeout: 60 * time.Second,
		},
		{
			name:          "EnvVarOverridesConfig",
			envValue:      "60",
			configTimeout: intPtr(45),
			wantTimeout:   60 * time.Second,
		},
		{
			name:          "FlagOverridesEnvVar",
			envValue:      "60",
			configTimeout: intPtr(45),
			flagTimeout:   intPtr(300),
			wantTimeout:   300 * time.Second,
		},
		{
			name:     "InvalidEnvVar",
			envValue: "abc",
			wantErr:  true,
		},
		{
			name:     "ZeroEnvVar",
			envValue: "0",
			wantErr:  true,
		},
		{
			name:     "NegativeEnvVar",
			envValue: "-5",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_DEPLOY_TIMEOUT", tt.envValue)
			action := newDeployTimeoutAction(t, tt.configTimeout, tt.flagTimeout)

			timeout, err := action.resolveDeployTimeout(action.projectConfig.Services["api"])
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
		name          string
		configTimeout *int
		flagTimeout   *int
		wantTimeout   time.Duration
	}{
		{
			name:        "DefaultTimeout",
			wantTimeout: 1200 * time.Second,
		},
		{
			name:          "ConfigTimeout",
			configTimeout: intPtr(45),
			wantTimeout:   45 * time.Second,
		},
		{
			name:          "ExplicitValue",
			configTimeout: intPtr(90),
			flagTimeout:   intPtr(30),
			wantTimeout:   30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deployErr := mockDeployErr(t.Name())
			action, serviceManager := newDeployActionForTimeoutTest(t, tt.configTimeout, tt.flagTimeout, deployErr, false)
			startTime := time.Now()

			_, err := action.Run(context.Background())
			require.ErrorIs(t, err, deployErr)

			require.True(t, serviceManager.deployHasDeadline, "deploy should run with a deadline")
			require.WithinDuration(t, startTime.Add(tt.wantTimeout), serviceManager.deployDeadline, 2*time.Second)
			serviceManager.AssertExpectations(t)
		})
	}
}

func TestDeployActionRunTimeoutWarningAndErrorMessage(t *testing.T) {
	action, serviceManager := newDeployActionForTimeoutTest(t, nil, intPtr(1), nil, true)

	_, err := action.Run(context.Background())
	require.EqualError(
		t,
		err,
		"deployment of service 'api' timed out after 1 seconds. To increase, use --timeout,"+
			" AZD_DEPLOY_TIMEOUT env var, or deployTimeout in azure.yaml."+
			" Note: azd has stopped waiting, but the deployment"+
			" may still be running in Azure."+
			" Check the Azure Portal for current deployment status.",
	)

	console := action.console.(*mockinput.MockConsole)
	output := strings.Join(console.Output(), "\n")
	require.Contains(
		t,
		output,
		"WARNING: Deployment of service 'api'"+
			" exceeded the azd wait timeout.",
	)
	require.Contains(
		t, output,
		"Check the Azure Portal for current deployment status.",
	)
	serviceManager.AssertExpectations(t)
}

func TestDeployActionRunDoesNotTreatInternalDeadlineExceededAsDeployTimeout(t *testing.T) {
	action, serviceManager := newDeployActionForTimeoutTest(t, nil, intPtr(30), context.DeadlineExceeded, false)

	_, err := action.Run(context.Background())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, context.DeadlineExceeded, err)
	serviceManager.AssertExpectations(t)
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
	waitForTimeout    bool
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

	if m.waitForTimeout {
		<-ctx.Done()
		return nil, ctx.Err()
	}

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
	configTimeout *int,
	flagTimeout *int,
	deployErr error,
	waitForTimeout bool,
) (*DeployAction, *mockDeployServiceManager) {
	t.Helper()

	action := newDeployTimeoutAction(t, configTimeout, flagTimeout)
	projectManager := &mockDeployProjectManager{}
	projectManager.On("Initialize", action.projectConfig).Return(nil).Once()
	projectManager.On("EnsureServiceTargetTools", action.projectConfig).Return(nil).Once()
	t.Cleanup(func() {
		projectManager.AssertExpectations(t)
	})

	serviceManager := &mockDeployServiceManager{deployErr: deployErr, waitForTimeout: waitForTimeout}
	serviceManager.On("Deploy", "api").Return().Once()

	action.projectManager = projectManager
	action.serviceManager = serviceManager
	return action, serviceManager
}

func newDeployTimeoutAction(t *testing.T, configTimeout *int, flagTimeout *int) *DeployAction {
	t.Helper()

	projectConfig := deployTimeoutTestProjectConfig(t, configTimeout)

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

func deployTimeoutTestProjectConfig(t *testing.T, configTimeout *int) *project.ProjectConfig {
	t.Helper()

	projectYaml := "name: test-proj\nservices:\n  api:\n    project: src/api\n    language: js\n    host: containerapp\n"
	if configTimeout != nil {
		projectYaml += "    deployTimeout: " + intToString(*configTimeout) + "\n"
	}

	projectConfig, err := project.Parse(
		context.Background(),
		projectYaml,
	)
	require.NoError(t, err)

	return projectConfig
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
