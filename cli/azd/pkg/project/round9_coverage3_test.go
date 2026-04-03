// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package project

import (
	"bytes"
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// Round 9: Targeted coverage for 65% target
// ============================================================

// ---------- DockerfileBuilder panic paths ----------
func Test_DockerfileBuilder_Panics_Coverage3(t *testing.T) {
	b := NewDockerfileBuilder()

	t.Run("Arg_empty_name", func(t *testing.T) {
		assert.Panics(t, func() { b.Arg("") })
	})
	t.Run("From_empty_image", func(t *testing.T) {
		assert.Panics(t, func() { b.From("") })
	})
}

func Test_DockerfileStage_Panics_Coverage3(t *testing.T) {
	b := NewDockerfileBuilder()
	s := b.From("golang:1.21")

	t.Run("Arg_empty_name", func(t *testing.T) {
		assert.Panics(t, func() { s.Arg("") })
	})
	t.Run("WorkDir_empty", func(t *testing.T) {
		assert.Panics(t, func() { s.WorkDir("") })
	})
	t.Run("Run_empty", func(t *testing.T) {
		assert.Panics(t, func() { s.Run("") })
	})
	t.Run("Copy_empty_source", func(t *testing.T) {
		assert.Panics(t, func() { s.Copy("", "dst") })
	})
	t.Run("Copy_empty_dest", func(t *testing.T) {
		assert.Panics(t, func() { s.Copy("src", "") })
	})
	t.Run("CopyFrom_empty_from", func(t *testing.T) {
		assert.Panics(t, func() { s.CopyFrom("", "src", "dst") })
	})
	t.Run("Env_empty_name", func(t *testing.T) {
		assert.Panics(t, func() { s.Env("", "val") })
	})
	t.Run("Expose_zero_port", func(t *testing.T) {
		assert.Panics(t, func() { s.Expose(0) })
	})
	t.Run("Expose_negative_port", func(t *testing.T) {
		assert.Panics(t, func() { s.Expose(-1) })
	})
	t.Run("Cmd_empty", func(t *testing.T) {
		assert.Panics(t, func() { s.Cmd() })
	})
	t.Run("Entrypoint_empty", func(t *testing.T) {
		assert.Panics(t, func() { s.Entrypoint() })
	})
	t.Run("User_empty", func(t *testing.T) {
		assert.Panics(t, func() { s.User("") })
	})
	t.Run("RunWithMounts_empty", func(t *testing.T) {
		assert.Panics(t, func() { s.RunWithMounts("") })
	})
}

// ---------- DockerfileBuilder: additional Build paths ----------
func Test_DockerfileBuilder_Build_MultiStage_Coverage3(t *testing.T) {
	b := NewDockerfileBuilder()
	b.Arg("GO_VERSION", "1.21")
	// First stage
	s1 := b.From("golang:${GO_VERSION}", "builder")
	s1.Arg("BUILD_MODE", "release")
	s1.WorkDir("/app")
	s1.Copy(".", ".")
	s1.Run("go mod download")
	s1.CopyFrom("builder", "/app/bin", "/usr/local/bin", "1000:1000")
	s1.Env("APP_ENV", "production")
	s1.RunWithMounts("go build -o /app/bin/main ./cmd/...", "type=cache,target=/go/pkg")
	s1.EmptyLine()
	s1.Comment("Final image")

	// Second stage
	s2 := b.From("alpine:latest")
	s2.Expose(8080)
	s2.User("nonroot")
	s2.Entrypoint("/app/bin/main")
	s2.Cmd("--config", "/etc/app/config.yaml")

	var buf bytes.Buffer
	err := b.Build(&buf)
	require.NoError(t, err)

	content := buf.String()
	assert.Contains(t, content, "ARG GO_VERSION=1.21")
	assert.Contains(t, content, "FROM golang:${GO_VERSION} AS builder")
	assert.Contains(t, content, "WORKDIR /app")
	assert.Contains(t, content, "COPY . .")
	assert.Contains(t, content, "RUN go mod download")
	assert.Contains(t, content, "COPY --from=builder --chown=1000:1000 /app/bin /usr/local/bin")
	assert.Contains(t, content, "ENV APP_ENV=production")
	assert.Contains(t, content, "EXPOSE 8080")
	assert.Contains(t, content, "USER nonroot")
	assert.Contains(t, content, `ENTRYPOINT ["/app/bin/main"]`)
	assert.Contains(t, content, `CMD ["--config", "/etc/app/config.yaml"]`)
}

// ---------- appendOperationArtifacts: invalid event type ----------
func Test_appendOperationArtifacts_InvalidEvent_Coverage3(t *testing.T) {
	sc := NewServiceContext()
	err := appendOperationArtifacts(sc, ext.Event("invalid"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid operation phase")
}

func Test_appendOperationArtifacts_NilContext_Coverage3(t *testing.T) {
	err := appendOperationArtifacts(nil, ServiceEventPackage, nil)
	require.NoError(t, err)
}

func Test_appendOperationArtifacts_AllEvents_Coverage3(t *testing.T) {
	events := []ext.Event{
		ServiceEventRestore,
		ServiceEventBuild,
		ServiceEventPackage,
		ServiceEventPublish,
		ServiceEventDeploy,
	}
	for _, ev := range events {
		t.Run(string(ev), func(t *testing.T) {
			sc := NewServiceContext()
			err := appendOperationArtifacts(sc, ev, nil)
			require.NoError(t, err)
		})
	}
}

// ---------- ServiceStable: DotNet canImport=true paths ----------
func Test_ServiceStable_DotNet_Errors_Coverage3(t *testing.T) {
	t.Run("NonContainerAppHost_Error", func(t *testing.T) {
		tmpDir := t.TempDir()
		importer := &DotNetImporter{
			hostCheck: map[string]hostCheckResult{
				tmpDir: {is: true},
			},
		}
		im := NewImportManager(importer)

		pc := &ProjectConfig{
			Name: "test",
			Path: tmpDir,
			Services: map[string]*ServiceConfig{
				"api": {
					Name:         "api",
					Host:         AppServiceTarget, // NOT ContainerAppTarget
					Language:     ServiceLanguageDotNet,
					RelativePath: ".",
					Project:      &ProjectConfig{Path: tmpDir},
				},
			},
		}

		_, err := im.ServiceStable(t.Context(), pc)
		require.Error(t, err)
		assert.ErrorIs(t, err, errAppHostMustTargetContainerApp)
	})

	t.Run("MultipleServices_Error", func(t *testing.T) {
		tmpDir := t.TempDir()
		importer := &DotNetImporter{
			hostCheck: map[string]hostCheckResult{
				tmpDir: {is: true},
			},
		}
		im := NewImportManager(importer)

		pc := &ProjectConfig{
			Name: "test",
			Path: tmpDir,
			Services: map[string]*ServiceConfig{
				"api": {
					Name:         "api",
					Host:         ContainerAppTarget,
					Language:     ServiceLanguageDotNet,
					RelativePath: ".",
					Project:      &ProjectConfig{Path: tmpDir},
				},
				"web": {
					Name:         "web",
					Host:         AppServiceTarget,
					Language:     ServiceLanguageJavaScript,
					RelativePath: "web",
					Project:      &ProjectConfig{Path: tmpDir},
				},
			},
		}

		_, err := im.ServiceStable(t.Context(), pc)
		require.Error(t, err)
		assert.ErrorIs(t, err, errNoMultipleServicesWithAppHost)
	})
}

// ---------- isComponentInitialized: already-initialized path ----------
func Test_isComponentInitialized_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{})
	sm := &serviceManager{
		env:            env,
		operationCache: make(ServiceOperationCache),
		initialized:    make(map[*ServiceConfig]map[any]bool),
	}

	sc := &ServiceConfig{Name: "api"}
	fakeComponent := "framework-service"

	// First call: not initialized, creates empty map
	ok := sm.isComponentInitialized(sc, fakeComponent)
	assert.False(t, ok)

	// Mark as initialized
	sm.initialized[sc][fakeComponent] = true

	// Second call: is initialized
	ok = sm.isComponentInitialized(sc, fakeComponent)
	assert.True(t, ok)

	// Third call with different component: not initialized
	ok = sm.isComponentInitialized(sc, "other-component")
	assert.False(t, ok)
}

// ---------- serviceManager Initialize: framework init error, target init error, already initialized ----------
func Test_serviceManager_Initialize_Coverage3(t *testing.T) {
	t.Run("FrameworkInit_Error", func(t *testing.T) {
		env := environment.NewWithValues("test-env", map[string]string{})
		sm := &serviceManager{
			env:            env,
			operationCache: make(ServiceOperationCache),
			initialized:    make(map[*ServiceConfig]map[any]bool),
			serviceLocator: newFakeLocator_r9(nil, nil),
		}

		sc := makeSvcConfig("api", "api", ContainerAppTarget, ServiceLanguagePython, t.TempDir())
		sc.Project.EventDispatcher = ext.NewEventDispatcher[ProjectLifecycleEventArgs]()

		err := sm.Initialize(t.Context(), sc)
		// Should get "getting framework service" error since Python is not registered
		require.Error(t, err)
	})

	t.Run("AlreadyInitialized_Skips", func(t *testing.T) {
		env := environment.NewWithValues("test-env", map[string]string{})
		fakeFramework := &noOpProject{}
		fakeTarget := &fakeServiceTarget_Cov3{}

		sm := &serviceManager{
			env:            env,
			operationCache: make(ServiceOperationCache),
			initialized:    make(map[*ServiceConfig]map[any]bool),
			serviceLocator: newFakeLocator_r9(fakeFramework, fakeTarget),
		}

		sc := makeSvcConfig("api", "api", ContainerAppTarget, ServiceLanguageDocker, t.TempDir())
		sc.Project.EventDispatcher = ext.NewEventDispatcher[ProjectLifecycleEventArgs]()

		// First initialization
		err := sm.Initialize(t.Context(), sc)
		require.NoError(t, err)

		// Second initialization - should skip (already initialized)
		err = sm.Initialize(t.Context(), sc)
		require.NoError(t, err)
	})
}

// ---------- serviceManager.Restore: cache hit ----------
func Test_serviceManager_Restore_CacheHit_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{})
	fakeFramework := &noOpProject{}
	fakeTarget := &fakeServiceTarget_Cov3{}

	sm := &serviceManager{
		env:            env,
		operationCache: make(ServiceOperationCache),
		initialized:    make(map[*ServiceConfig]map[any]bool),
		serviceLocator: newFakeLocator_r9(fakeFramework, fakeTarget),
	}

	sc := makeSvcConfig("api", "api", ContainerAppTarget, ServiceLanguageDocker, t.TempDir())
	sc.Project.EventDispatcher = ext.NewEventDispatcher[ProjectLifecycleEventArgs]()

	// Seed cache with a restore result
	cachedResult := &ServiceRestoreResult{}
	sm.setOperationResult(sc, ServiceEventRestore, cachedResult)

	result, err := sm.Restore(t.Context(), sc, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, cachedResult, result)
}

// ---------- UnsupportedServiceHostError ----------
func Test_UnsupportedServiceHostError_Coverage3(t *testing.T) {
	err := &UnsupportedServiceHostError{
		Host:        "unknown-host",
		ServiceName: "api",
	}
	assert.Contains(t, err.Error(), "unknown-host")
	assert.Contains(t, err.Error(), "api")
}

// ---------- fakeLocator for serviceManager tests ----------
type fakeLocator_r9 struct {
	framework FrameworkService
	target    ServiceTarget
}

func newFakeLocator_r9(framework FrameworkService, target ServiceTarget) *fakeLocator_r9 {
	return &fakeLocator_r9{framework: framework, target: target}
}

func (f *fakeLocator_r9) ResolveNamed(name string, o any) error {
	switch ptr := o.(type) {
	case *FrameworkService:
		if f.framework != nil {
			*ptr = f.framework
			return nil
		}
		return &UnsupportedServiceHostError{Host: name}
	case *ServiceTarget:
		if f.target != nil {
			*ptr = f.target
			return nil
		}
		return &UnsupportedServiceHostError{Host: name}
	case *CompositeFrameworkService:
		return &UnsupportedServiceHostError{Host: name}
	}
	return nil
}

func (f *fakeLocator_r9) Resolve(_ any) error {
	return nil
}

func (f *fakeLocator_r9) Invoke(_ any) error {
	return nil
}

// ---------- ExternalServiceTarget.RequiredExternalTools: trivial empty ----------
func Test_ExternalServiceTarget_RequiredExternalTools_Coverage3(t *testing.T) {
	est := &ExternalServiceTarget{}
	tools := est.RequiredExternalTools(context.Background(), &ServiceConfig{})
	assert.Empty(t, tools)
}

// ---------- ExternalFrameworkService.RequiredExternalTools with nil broker ----------
func Test_ExternalFrameworkService_toProtoNil_Coverage3(t *testing.T) {
	efs := &ExternalFrameworkService{}
	cfg, err := efs.toProtoServiceConfig(nil)
	assert.Nil(t, cfg)
	assert.NoError(t, err)
}
