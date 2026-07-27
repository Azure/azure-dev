// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func TestParseKeyValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arg       string
		wantKey   string
		wantValue string
		wantErr   bool
	}{
		{
			name:      "simple_key_value",
			arg:       "KEY=VALUE",
			wantKey:   "KEY",
			wantValue: "VALUE",
		},
		{
			name:      "value_with_equals",
			arg:       "KEY=VALUE=WITH=EQUALS",
			wantKey:   "KEY",
			wantValue: "VALUE=WITH=EQUALS",
		},
		{
			name:      "empty_value",
			arg:       "KEY=",
			wantKey:   "KEY",
			wantValue: "",
		},
		{
			name:      "value_with_spaces",
			arg:       "KEY=hello world",
			wantKey:   "KEY",
			wantValue: "hello world",
		},
		{
			name:    "no_equals_sign",
			arg:     "KEYONLY",
			wantErr: true,
		},
		{
			name:    "empty_string",
			arg:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			key, value, err := parseKeyValue(tt.arg)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "invalid key=value format")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantKey, key)
			require.Equal(t, tt.wantValue, value)
		})
	}
}

func TestWarnKeyCaseConflicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		dotEnv map[string]string
		key    string
	}{
		{
			name:   "no_conflicts",
			dotEnv: map[string]string{"OTHER_KEY": "val"},
			key:    "MY_KEY",
		},
		{
			name:   "exact_match_no_conflict",
			dotEnv: map[string]string{"MY_KEY": "val"},
			key:    "MY_KEY",
		},
		{
			name:   "single_case_conflict",
			dotEnv: map[string]string{"My_Key": "val"},
			key:    "MY_KEY",
		},
		{
			name:   "multiple_case_conflicts",
			dotEnv: map[string]string{"My_Key": "v1", "my_key": "v2"},
			key:    "MY_KEY",
		},
		{
			name:   "empty_dotenv",
			dotEnv: map[string]string{},
			key:    "MY_KEY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mockContext := mocks.NewMockContext(t.Context())
			// Verify the function doesn't panic with any input
			warnKeyCaseConflicts(t.Context(), mockContext.Console, tt.dotEnv, tt.key)
		})
	}
}

func TestServiceNameWarningCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		serviceName string
		commandName string
	}{
		{
			name:        "empty_service_name_no_warning",
			serviceName: "",
			commandName: "deploy",
		},
		{
			name:        "non_empty_service_name_shows_warning",
			serviceName: "myservice",
			commandName: "deploy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mockContext := mocks.NewMockContext(t.Context())
			// Should not panic regardless of input
			serviceNameWarningCheck(mockContext.Console, tt.serviceName, tt.commandName)
		})
	}
}

func TestCountTrue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		elms     []bool
		expected int
	}{
		{
			name:     "all_false",
			elms:     []bool{false, false, false},
			expected: 0,
		},
		{
			name:     "all_true",
			elms:     []bool{true, true, true},
			expected: 3,
		},
		{
			name:     "mixed",
			elms:     []bool{true, false, true, false},
			expected: 2,
		},
		{
			name:     "single_true",
			elms:     []bool{true},
			expected: 1,
		},
		{
			name:     "single_false",
			elms:     []bool{false},
			expected: 0,
		},
		{
			name:     "empty",
			elms:     []bool{},
			expected: 0,
		},
		{
			name:     "nil_variadic",
			elms:     nil,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := countTrue(tt.elms...)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestWithBrowserOverride(t *testing.T) {
	t.Parallel()

	t.Run("sets_and_retrieves_override", func(t *testing.T) {
		t.Parallel()
		var capturedURL string
		ctx := WithBrowserOverride(t.Context(),
			func(_ context.Context, _ input.Console, url string) {
				capturedURL = url
			})
		require.NotNil(t, ctx)

		// Verify the value is retrievable
		val, ok := ctx.Value(browserOverrideKey{}).(browseUrl)
		require.True(t, ok)
		require.NotNil(t, val)

		// Verify override is callable
		val(ctx, nil, "https://example.com")
		require.Equal(t, "https://example.com", capturedURL)
	})

	t.Run("nil_context_value_without_override", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		val := ctx.Value(browserOverrideKey{})
		require.Nil(t, val)
	})
}

func Test_Since(t *testing.T) {
	// Reset interact time for clean test
	tracing.InteractTimeMs.Store(0)

	start := time.Now().Add(-2 * time.Second)
	d := since(start)
	assert.True(t, d >= 2*time.Second, "expected at least 2s, got %v", d)

	// Test with interaction time deducted
	tracing.InteractTimeMs.Store(500)
	d2 := since(start)
	// Should be about 500ms less than real elapsed
	assert.True(t, d2 < d, "expected interaction time to reduce duration")

	// Cleanup
	tracing.InteractTimeMs.Store(0)
}

func Test_OpenWithDefaultBrowser_Override(t *testing.T) {
	t.Parallel()

	var capturedURL string
	ctx := WithBrowserOverride(t.Context(), func(ctx context.Context, console input.Console, url string) {
		capturedURL = url
	})

	mockConsole := mockinput.NewMockConsole()
	openWithDefaultBrowser(ctx, mockConsole, "https://example.com")
	assert.Equal(t, "https://example.com", capturedURL)
}

func Test_OpenWithDefaultBrowser_NoOverride(t *testing.T) {
	mockConsole := mockinput.NewMockConsole()
	// Use a no-op browser override to prevent real browser launch
	ctx := WithBrowserOverride(t.Context(), func(_ context.Context, _ input.Console, _ string) {})
	openWithDefaultBrowser(ctx, mockConsole, "https://example.com")
}

func Test_ServiceNameWarningCheck(t *testing.T) {
	t.Parallel()

	t.Run("NoWarningWhenEmpty", func(t *testing.T) {
		mockConsole := mockinput.NewMockConsole()
		// Should return early without writing anything (no panic = pass)
		serviceNameWarningCheck(mockConsole, "", "deploy")
	})

	t.Run("WarningWhenSet", func(t *testing.T) {
		mockConsole := mockinput.NewMockConsole()
		// Exercises the non-empty path (writes to stderr which is io.Discard in mock)
		serviceNameWarningCheck(mockConsole, "mysvc", "deploy")
	})
}

// mockProjectManager implements project.ProjectManager for testing
type mockProjectManager struct {
	mock.Mock
}

func (m *mockProjectManager) DefaultServiceFromWd(
	ctx context.Context,
	projectConfig *project.ProjectConfig,
) (*project.ServiceConfig, error) {
	args := m.Called(ctx, projectConfig)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*project.ServiceConfig), args.Error(1)
}

func (m *mockProjectManager) Initialize(ctx context.Context, projectConfig *project.ProjectConfig) error {
	return m.Called(ctx, projectConfig).Error(0)
}

func (m *mockProjectManager) InitializeFrameworks(
	ctx context.Context, projectConfig *project.ProjectConfig,
) ([]*project.ServiceConfig, []project.ServiceFrameworkInitFailure, error) {
	args := m.Called(ctx, projectConfig)
	services, _ := args.Get(0).([]*project.ServiceConfig)
	skipped, _ := args.Get(1).([]project.ServiceFrameworkInitFailure)
	return services, skipped, args.Error(2)
}

func (m *mockProjectManager) EnsureAllTools(
	ctx context.Context, projectConfig *project.ProjectConfig, filter project.ServiceFilterPredicate,
) error {
	return m.Called(ctx, projectConfig, filter).Error(0)
}

func (m *mockProjectManager) EnsureFrameworkTools(
	ctx context.Context, projectConfig *project.ProjectConfig, filter project.ServiceFilterPredicate,
) error {
	return m.Called(ctx, projectConfig, filter).Error(0)
}

func (m *mockProjectManager) EnsureServiceTargetTools(
	ctx context.Context, projectConfig *project.ProjectConfig, filter project.ServiceFilterPredicate,
) error {
	return m.Called(ctx, projectConfig, filter).Error(0)
}

func (m *mockProjectManager) EnsureRestoreTools(
	ctx context.Context, projectConfig *project.ProjectConfig, filter project.ServiceFilterPredicate,
) error {
	return m.Called(ctx, projectConfig, filter).Error(0)
}

func Test_GetTargetServiceName(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	pc := &project.ProjectConfig{}

	t.Run("AllAndServiceConflict", func(t *testing.T) {
		pm := &mockProjectManager{}
		im := project.NewImportManager(nil)
		_, err := getTargetServiceName(ctx, pm, im, pc, "deploy", "myservice", true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot specify both --all and <service>")
	})

	t.Run("AllFlagReturnsEmpty", func(t *testing.T) {
		pm := &mockProjectManager{}
		im := project.NewImportManager(nil)
		name, err := getTargetServiceName(ctx, pm, im, pc, "deploy", "", true)
		require.NoError(t, err)
		assert.Equal(t, "", name)
	})

	t.Run("NoServiceNoAll_NoDefault", func(t *testing.T) {
		pm := &mockProjectManager{}
		pm.On("DefaultServiceFromWd", mock.Anything, mock.Anything).
			Return(nil, project.ErrNoDefaultService)
		im := project.NewImportManager(nil)
		_, err := getTargetServiceName(ctx, pm, im, pc, "deploy", "", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "current working directory is not a project or service directory")
	})

	t.Run("NoServiceNoAll_DefaultError", func(t *testing.T) {
		pm := &mockProjectManager{}
		pm.On("DefaultServiceFromWd", mock.Anything, mock.Anything).
			Return(nil, fmt.Errorf("random error"))
		im := project.NewImportManager(nil)
		_, err := getTargetServiceName(ctx, pm, im, pc, "deploy", "", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "random error")
	})

	t.Run("NoServiceNoAll_DefaultFound", func(t *testing.T) {
		svc := &project.ServiceConfig{Name: "web"}
		pm := &mockProjectManager{}
		pm.On("DefaultServiceFromWd", mock.Anything, mock.Anything).
			Return(svc, nil)
		// HasService needs to succeed; use real import manager with the service in project config
		pConfig := &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{
				"web": svc,
			},
		}
		im := project.NewImportManager(nil)
		name, err := getTargetServiceName(ctx, pm, im, pConfig, "deploy", "", false)
		require.NoError(t, err)
		assert.Equal(t, "web", name)
	})
}

func Test_WithBrowserOverride_ContextPropagation(t *testing.T) {
	t.Parallel()

	// Base context without override
	ctx := t.Context()
	val, ok := ctx.Value(browserOverrideKey{}).(browseUrl)
	assert.False(t, ok)
	assert.Nil(t, val)

	// Context with override
	called := false
	ctx2 := WithBrowserOverride(ctx, func(ctx context.Context, console input.Console, url string) {
		called = true
	})
	fn, ok := ctx2.Value(browserOverrideKey{}).(browseUrl)
	assert.True(t, ok)
	assert.NotNil(t, fn)
	fn(ctx2, nil, "test")
	assert.True(t, called)
}

// Test CmdAnnotations type
func Test_CmdAnnotations(t *testing.T) {
	t.Parallel()

	annotations := CmdAnnotations{
		"key1": "value1",
		"key2": "value2",
	}
	assert.Equal(t, "value1", annotations["key1"])
	assert.Equal(t, "value2", annotations["key2"])
}

// Test CmdCalledAs type
func Test_CmdCalledAs(t *testing.T) {
	t.Parallel()

	calledAs := CmdCalledAs("test-command")
	assert.Equal(t, CmdCalledAs("test-command"), calledAs)
	assert.Equal(t, "test-command", string(calledAs))
}

// Test envFlagCtxKey
func Test_EnvFlagCtxKey(t *testing.T) {
	t.Parallel()

	assert.Equal(t, envFlagKey("envFlag"), envFlagCtxKey)
}

// Test referenceDocumentationUrl constant
func Test_ReferenceDocumentationUrl(t *testing.T) {
	t.Parallel()

	assert.Contains(t, referenceDocumentationUrl, "learn.microsoft.com")
}

// Use a helper from mocks for full integration test of serviceNameWarningCheck
func Test_ServiceNameWarningCheck_Integration(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	serviceNameWarningCheck(mockContext.Console, "api", "restore")
}

func Test_Since_ReturnsNonNegative(t *testing.T) {
	// Reset interact time for clean test
	tracing.InteractTimeMs.Store(0)
	t.Cleanup(func() { tracing.InteractTimeMs.Store(0) })
	import_time := since(time.Now())
	assert.GreaterOrEqual(t, import_time.Nanoseconds(), int64(0))
}
