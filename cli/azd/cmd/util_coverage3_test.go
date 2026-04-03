// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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
	ctx := WithBrowserOverride(context.Background(), func(ctx context.Context, console input.Console, url string) {
		capturedURL = url
	})

	mockConsole := mockinput.NewMockConsole()
	openWithDefaultBrowser(ctx, mockConsole, "https://example.com")
	assert.Equal(t, "https://example.com", capturedURL)
}

func Test_OpenWithDefaultBrowser_NoOverride(t *testing.T) {
	mockConsole := mockinput.NewMockConsole()
	// Use a no-op browser override to prevent real browser launch
	ctx := WithBrowserOverride(context.Background(), func(_ context.Context, _ input.Console, _ string) {})
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

	ctx := context.Background()
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
	ctx := context.Background()
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
	mockContext := mocks.NewMockContext(context.Background())
	serviceNameWarningCheck(mockContext.Console, "api", "restore")
}
