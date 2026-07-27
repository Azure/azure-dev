// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
)

func TestDockerfileBuilder_Simple(t *testing.T) {
	builder := NewDockerfileBuilder()
	builder.From("node:18-alpine").
		WorkDir("/app").
		Copy("package*.json", "./").
		Run("npm install").
		Copy(".", ".").
		Expose(3000).
		Cmd("node", "server.js")

	var buf bytes.Buffer
	err := builder.Build(&buf)
	require.NoError(t, err)

	snapshot.SnapshotT(t, buf.String())
}

func TestDockerfileBuilder_MultiStage(t *testing.T) {
	builder := NewDockerfileBuilder()

	// Build stage
	builder.From("node:18-alpine", "build").
		WorkDir("/app").
		Copy("package*.json", "./").
		Run("npm ci").
		Copy(".", ".").
		Run("npm run build")

	// Production stage
	builder.From("node:18-alpine", "production").
		WorkDir("/app").
		CopyFrom("build", "/app/dist", "./dist").
		CopyFrom("build", "/app/node_modules", "./node_modules").
		Expose(3000).
		Cmd("node", "dist/server.js")

	var buf bytes.Buffer
	err := builder.Build(&buf)
	require.NoError(t, err)

	snapshot.SnapshotT(t, buf.String())
}

func TestDockerfileBuilder_GlobalArgs(t *testing.T) {
	builder := NewDockerfileBuilder()

	builder.Arg("NODE_VERSION", "18").
		Arg("PORT").
		From("node:${NODE_VERSION}-alpine").
		WorkDir("/app").
		Copy(".", ".").
		Env("PORT", "3000").
		Expose(3000).
		Cmd("node", "server.js")

	var buf bytes.Buffer
	err := builder.Build(&buf)
	require.NoError(t, err)

	snapshot.SnapshotT(t, buf.String())
}

func TestDockerfileBuilder_StageArgs(t *testing.T) {
	builder := NewDockerfileBuilder()

	builder.From("node:18-alpine").
		Arg("BUILD_DATE").
		Arg("VERSION", "1.0.0").
		WorkDir("/app").
		Run("echo \"Build date: ${BUILD_DATE}\"").
		Run("echo \"Version: ${VERSION}\"").
		Copy(".", ".").
		Cmd("node", "server.js")

	var buf bytes.Buffer
	err := builder.Build(&buf)
	require.NoError(t, err)

	snapshot.SnapshotT(t, buf.String())
}

func TestDockerfileBuilder_CopyWithChown(t *testing.T) {
	builder := NewDockerfileBuilder()

	builder.From("node:18-alpine").
		WorkDir("/app").
		Copy("package*.json", "./", "node:node").
		Run("npm install").
		Copy(".", ".", "node:node").
		User("node").
		Cmd("node", "server.js")

	var buf bytes.Buffer
	err := builder.Build(&buf)
	require.NoError(t, err)

	snapshot.SnapshotT(t, buf.String())
}

func TestDockerfileBuilder_CopyFromWithChown(t *testing.T) {
	builder := NewDockerfileBuilder()

	builder.From("golang:1.21-alpine", "build").
		WorkDir("/app").
		Copy("go.mod", "./").
		Copy("go.sum", "./").
		Run("go mod download").
		Copy(".", ".").
		Run("go build -o myapp")

	builder.From("alpine:latest").
		Run("addgroup -g 1000 appuser && adduser -D -u 1000 -G appuser appuser").
		WorkDir("/home/appuser").
		CopyFrom("build", "/app/myapp", "./myapp", "appuser:appuser").
		User("appuser").
		Entrypoint("./myapp")

	var buf bytes.Buffer
	err := builder.Build(&buf)
	require.NoError(t, err)

	snapshot.SnapshotT(t, buf.String())
}

func TestDockerfileBuilder_EnvVariables(t *testing.T) {
	builder := NewDockerfileBuilder()

	builder.From("node:18-alpine").
		Env("NODE_ENV", "production").
		Env("APP_DIR", "/app").
		Env("PATH_WITH_SPACES", "path with spaces").
		Env("QUOTED", "value\"with\"quotes").
		WorkDir("$APP_DIR").
		Copy(".", ".").
		Cmd("node", "server.js")

	var buf bytes.Buffer
	err := builder.Build(&buf)
	require.NoError(t, err)

	snapshot.SnapshotT(t, buf.String())
}

func TestDockerfileBuilder_RunWithMounts(t *testing.T) {
	builder := NewDockerfileBuilder()

	builder.From("golang:1.21-alpine").
		WorkDir("/app").
		Copy("go.mod", "./").
		Copy("go.sum", "./").
		RunWithMounts(
			"go mod download",
			"type=cache,target=/go/pkg/mod",
		).
		Copy(".", ".").
		RunWithMounts(
			"go build -o myapp",
			"type=cache,target=/go/pkg/mod",
			"type=cache,target=/root/.cache/go-build",
		).
		Cmd("./myapp")

	var buf bytes.Buffer
	err := builder.Build(&buf)
	require.NoError(t, err)

	snapshot.SnapshotT(t, buf.String())
}

func TestDockerfileBuilder_Comments(t *testing.T) {
	builder := NewDockerfileBuilder()

	builder.From("node:18-alpine").
		Comment("Install dependencies").
		WorkDir("/app").
		Copy("package*.json", "./").
		Run("npm install").
		EmptyLine().
		Comment("Copy application code").
		Copy(".", ".").
		EmptyLine().
		Comment("This is a multi-line comment\nIt spans multiple lines\nAnd provides detailed information").
		Expose(3000).
		Cmd("node", "server.js")

	var buf bytes.Buffer
	err := builder.Build(&buf)
	require.NoError(t, err)

	snapshot.SnapshotT(t, buf.String())
}

func TestDockerfileBuilder_ComplexMultiStage(t *testing.T) {
	builder := NewDockerfileBuilder()

	// Global args
	builder.Arg("NODE_VERSION", "18").
		Arg("ALPINE_VERSION", "3.18")

	// Dependencies stage
	builder.From("node:${NODE_VERSION}-alpine${ALPINE_VERSION}", "deps").
		Comment("Install dependencies").
		WorkDir("/app").
		Copy("package*.json", "./").
		Run("npm ci --only=production")

	// Build stage
	builder.From("node:${NODE_VERSION}-alpine${ALPINE_VERSION}", "build").
		WorkDir("/app").
		Copy("package*.json", "./").
		Run("npm ci").
		Copy(".", ".").
		Run("npm run build").
		Run("npm run test")

	// Production stage
	builder.From("node:${NODE_VERSION}-alpine${ALPINE_VERSION}").
		Comment("Production stage").
		Arg("BUILD_DATE").
		Arg("VERSION", "1.0.0").
		WorkDir("/app").
		EmptyLine().
		Comment("Copy dependencies from deps stage").
		CopyFrom("deps", "/app/node_modules", "./node_modules").
		EmptyLine().
		Comment("Copy built application from build stage").
		CopyFrom("build", "/app/dist", "./dist").
		EmptyLine().
		Comment("Set metadata").
		Env("NODE_ENV", "production").
		Env("BUILD_DATE", "${BUILD_DATE}").
		Env("VERSION", "${VERSION}").
		EmptyLine().
		Comment("Create non-root user").
		Run("addgroup -g 1000 appuser && adduser -D -u 1000 -G appuser appuser").
		User("appuser").
		EmptyLine().
		Expose(3000).
		Cmd("node", "dist/server.js")

	var buf bytes.Buffer
	err := builder.Build(&buf)
	require.NoError(t, err)

	snapshot.SnapshotT(t, buf.String())
}

func TestDockerfileBuilder_DotnetApp(t *testing.T) {
	builder := NewDockerfileBuilder()

	// Build stage
	builder.From("mcr.microsoft.com/dotnet/sdk:8.0", "build").
		WorkDir("/src").
		Copy("*.csproj", "./").
		Run("dotnet restore").
		Copy(".", ".").
		Run("dotnet publish -c Release -o /app/publish")

	// Runtime stage
	builder.From("mcr.microsoft.com/dotnet/aspnet:8.0").
		WorkDir("/app").
		CopyFrom("build", "/app/publish", ".").
		Expose(80).
		Expose(443).
		Entrypoint("dotnet", "MyApp.dll")

	var buf bytes.Buffer
	err := builder.Build(&buf)
	require.NoError(t, err)

	snapshot.SnapshotT(t, buf.String())
}

func TestDockerfileBuilder_PythonApp(t *testing.T) {
	builder := NewDockerfileBuilder()

	builder.From("python:3.11-slim").
		Comment("Set working directory").
		WorkDir("/app").
		EmptyLine().
		Comment("Install dependencies").
		Copy("requirements.txt", ".").
		RunWithMounts(
			"pip install --no-cache-dir -r requirements.txt",
			"type=cache,target=/root/.cache/pip",
		).
		EmptyLine().
		Comment("Copy application").
		Copy(".", ".").
		EmptyLine().
		Comment("Create non-root user").
		Run("useradd -m -u 1000 appuser && chown -R appuser:appuser /app").
		User("appuser").
		EmptyLine().
		Expose(8000).
		Cmd("python", "-m", "uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000")

	var buf bytes.Buffer
	err := builder.Build(&buf)
	require.NoError(t, err)

	snapshot.SnapshotT(t, buf.String())
}

func Test_NewDockerfileBuilder(t *testing.T) {
	b := NewDockerfileBuilder()
	require.NotNil(t, b)
}

func Test_DockerfileBuilder_SimpleDockerfile(t *testing.T) {
	b := NewDockerfileBuilder()
	b.From("golang:1.22", "build").
		WorkDir("/app").
		Copy("go.mod", "./").
		Copy("go.sum", "./").
		Run("go mod download").
		Copy(".", ".").
		Run("go build -o /app/main .")

	b.From("gcr.io/distroless/base-debian12").
		Copy("/app/main", "/app/main").
		User("nonroot:nonroot").
		Expose(8080).
		Entrypoint("/app/main")

	var buf bytes.Buffer
	err := b.Build(&buf)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "FROM golang:1.22 AS build")
	assert.Contains(t, output, "WORKDIR /app")
	assert.Contains(t, output, "COPY go.mod ./")
	assert.Contains(t, output, "COPY go.sum ./")
	assert.Contains(t, output, "RUN go mod download")
	assert.Contains(t, output, "FROM gcr.io/distroless/base-debian12")
	assert.Contains(t, output, "COPY /app/main /app/main")
	assert.Contains(t, output, "USER nonroot:nonroot")
	assert.Contains(t, output, "EXPOSE 8080")
	assert.Contains(t, output, "ENTRYPOINT [\"/app/main\"]")
}

func Test_DockerfileBuilder_Arg(t *testing.T) {
	b := NewDockerfileBuilder()
	b.Arg("VERSION")
	b.Arg("PORT", "8080")

	b.From("node:${VERSION}").
		Expose(8080)

	var buf bytes.Buffer
	err := b.Build(&buf)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "ARG VERSION")
	assert.Contains(t, output, "ARG PORT=8080")
}

func Test_DockerfileStage_Arg(t *testing.T) {
	b := NewDockerfileBuilder()
	b.From("node:18").
		Arg("BUILD_MODE", "production").
		Run("echo $BUILD_MODE")

	var buf bytes.Buffer
	err := b.Build(&buf)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "ARG BUILD_MODE=production")
}

func Test_DockerfileStage_CopyFrom(t *testing.T) {
	b := NewDockerfileBuilder()
	b.From("golang:1.22", "build").
		Run("go build -o /app/main")

	b.From("alpine:latest").
		CopyFrom("build", "/app/main", "/usr/local/bin/main")

	var buf bytes.Buffer
	err := b.Build(&buf)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "COPY --from=build /app/main /usr/local/bin/main")
}

func Test_DockerfileStage_CopyFromWithChown(t *testing.T) {
	b := NewDockerfileBuilder()
	b.From("golang:1.22", "build").
		Run("go build -o /app/main")

	b.From("alpine:latest").
		CopyFrom("build", "/app/main", "/usr/local/bin/main", "app:app")

	var buf bytes.Buffer
	err := b.Build(&buf)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "--chown=app:app")
	assert.Contains(t, output, "--from=build")
}

func Test_DockerfileStage_CopyWithChown(t *testing.T) {
	b := NewDockerfileBuilder()
	b.From("node:18").
		Copy("package.json", ".", "node:node")

	var buf bytes.Buffer
	err := b.Build(&buf)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "COPY --chown=node:node package.json .")
}

func Test_DockerfileStage_Env(t *testing.T) {
	b := NewDockerfileBuilder()
	b.From("node:18").
		Env("NODE_ENV", "production").
		Env("PORT", "3000")

	var buf bytes.Buffer
	err := b.Build(&buf)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "ENV NODE_ENV=production")
	assert.Contains(t, output, "ENV PORT=3000")
}

func Test_DockerfileStage_Cmd(t *testing.T) {
	b := NewDockerfileBuilder()
	b.From("node:18").
		Cmd("node", "server.js")

	var buf bytes.Buffer
	err := b.Build(&buf)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, `CMD ["node", "server.js"]`)
}

func Test_DockerfileStage_RunWithMounts(t *testing.T) {
	b := NewDockerfileBuilder()
	b.From("golang:1.22").
		RunWithMounts("go build -o /app/main",
			"type=cache,target=/go/pkg/mod",
			"type=cache,target=/root/.cache/go-build")

	var buf bytes.Buffer
	err := b.Build(&buf)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "--mount=type=cache,target=/go/pkg/mod")
	assert.Contains(t, output, "--mount=type=cache,target=/root/.cache/go-build")
	assert.Contains(t, output, "go build -o /app/main")
}

func Test_DockerfileStage_EmptyLineAndComment(t *testing.T) {
	b := NewDockerfileBuilder()
	b.From("node:18").
		Comment("Install dependencies").
		Run("npm install").
		EmptyLine().
		Comment("Build application").
		Run("npm run build")

	var buf bytes.Buffer
	err := b.Build(&buf)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "# Install dependencies")
	assert.Contains(t, output, "# Build application")
	// Check that empty line exists (double newline)
	assert.True(t, strings.Contains(output, "\n\n"))
}

func Test_DockerfileBuilder_MultiStage(t *testing.T) {
	b := NewDockerfileBuilder()
	b.Arg("GO_VERSION", "1.22")

	build := b.From("golang:${GO_VERSION}", "build")
	build.WorkDir("/src")
	build.Copy(".", ".")
	build.Run("go build -o /app/main .")

	prod := b.From("alpine:latest")
	prod.CopyFrom("build", "/app/main", "/app/main")
	prod.Expose(8080)
	prod.Entrypoint("/app/main")

	var buf bytes.Buffer
	err := b.Build(&buf)
	require.NoError(t, err)

	output := buf.String()
	// Should have proper multi-stage structure
	assert.Contains(t, output, "FROM golang:${GO_VERSION} AS build")
	assert.Contains(t, output, "FROM alpine:latest")
	assert.Contains(t, output, "COPY --from=build /app/main /app/main")
}

func Test_DockerfileBuilder_EmptyBuild(t *testing.T) {
	b := NewDockerfileBuilder()
	var buf bytes.Buffer
	err := b.Build(&buf)
	require.NoError(t, err)
	assert.Empty(t, buf.String())
}

func Test_validateTargetResource(t *testing.T) {
	target := &dotnetContainerAppTarget{}

	t.Run("EmptyResourceGroup_Error", func(t *testing.T) {
		tr := environment.NewTargetResource("sub-id", "", "res-name", "")
		err := target.validateTargetResource(tr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing resource group name")
	})

	t.Run("WrongResourceType_Error", func(t *testing.T) {
		tr := environment.NewTargetResource("sub-id", "my-rg", "res-name", "Microsoft.Web/sites")
		err := target.validateTargetResource(tr)
		require.Error(t, err)
	})

	t.Run("CorrectResourceType_OK", func(t *testing.T) {
		tr := environment.NewTargetResource(
			"sub-id", "my-rg", "res-name",
			string(azapi.AzureResourceTypeContainerAppEnvironment),
		)
		err := target.validateTargetResource(tr)
		require.NoError(t, err)
	})

	t.Run("EmptyResourceType_OK", func(t *testing.T) {
		tr := environment.NewTargetResource("sub-id", "my-rg", "res-name", "")
		err := target.validateTargetResource(tr)
		require.NoError(t, err)
	})
}

func Test_mapToExpandableStringSlice(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		result := mapToExpandableStringSlice(map[string]string{}, "=")
		assert.Empty(t, result)
	})

	t.Run("NilMap", func(t *testing.T) {
		result := mapToExpandableStringSlice(nil, "=")
		assert.Empty(t, result)
	})

	t.Run("WithValues", func(t *testing.T) {
		input := map[string]string{
			"KEY1": "value1",
			"KEY2": "value2",
		}
		result := mapToExpandableStringSlice(input, "=")
		require.Len(t, result, 2)

		// Since map iteration is non-deterministic, sort results
		strs := make([]string, len(result))
		for i, es := range result {
			strs[i] = string(expandableStringTemplate(es))
		}
		sort.Strings(strs)
		assert.Equal(t, "KEY1=value1", strs[0])
		assert.Equal(t, "KEY2=value2", strs[1])
	})

	t.Run("EmptyValues", func(t *testing.T) {
		input := map[string]string{
			"KEY_ONLY": "",
		}
		result := mapToExpandableStringSlice(input, "=")
		require.Len(t, result, 1)
		// When value is empty, only key is used
		assert.Equal(t, "KEY_ONLY", string(expandableStringTemplate(result[0])))
	})

	t.Run("CustomSeparator", func(t *testing.T) {
		input := map[string]string{
			"HOST": "localhost:8080",
		}
		result := mapToExpandableStringSlice(input, ":")
		require.Len(t, result, 1)
		assert.Equal(t, "HOST:localhost:8080", string(expandableStringTemplate(result[0])))
	})
}

// ---------- checkResourceType ----------

// ---------- DockerfileBuilder panic paths ----------
func Test_DockerfileBuilder_Panics(t *testing.T) {
	b := NewDockerfileBuilder()

	t.Run("Arg_empty_name", func(t *testing.T) {
		assert.Panics(t, func() { b.Arg("") })
	})
	t.Run("From_empty_image", func(t *testing.T) {
		assert.Panics(t, func() { b.From("") })
	})
}

func Test_DockerfileStage_Panics(t *testing.T) {
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
func Test_DockerfileBuilder_Build_MultiStage(t *testing.T) {
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

func Test_OverriddenEndpoints(t *testing.T) {
	t.Run("NoOverride", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{})
		sc := &ServiceConfig{Name: "api"}

		endpoints := OverriddenEndpoints(t.Context(), sc, env)
		assert.Nil(t, endpoints)
	})

	t.Run("ValidJSON", func(t *testing.T) {
		urls := []string{"https://app.azurewebsites.net", "https://app-slot.azurewebsites.net"}
		jsonBytes, _ := json.Marshal(urls)
		env := environment.NewWithValues("test", map[string]string{
			"SERVICE_API_ENDPOINTS": string(jsonBytes),
		})
		sc := &ServiceConfig{Name: "api"}

		endpoints := OverriddenEndpoints(t.Context(), sc, env)
		assert.Equal(t, urls, endpoints)
	})

	t.Run("InvalidJSON_returns_nil", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{
			"SERVICE_API_ENDPOINTS": "not-json",
		})
		sc := &ServiceConfig{Name: "api"}

		endpoints := OverriddenEndpoints(t.Context(), sc, env)
		assert.Nil(t, endpoints)
	})
}

func Test_slotEnvVarNameForService(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple", "web", "AZD_DEPLOY_WEB_SLOT_NAME"},
		{"withHyphens", "my-web-app", "AZD_DEPLOY_MY_WEB_APP_SLOT_NAME"},
		{"uppercase", "MyApp", "AZD_DEPLOY_MYAPP_SLOT_NAME"},
		{"mixed", "my-App-2", "AZD_DEPLOY_MY_APP_2_SLOT_NAME"},
		{"withSpaces", "api and frontend", "AZD_DEPLOY_API_AND_FRONTEND_SLOT_NAME"},
		{"spacesAndHyphens", "my api-service", "AZD_DEPLOY_MY_API_SERVICE_SLOT_NAME"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := slotEnvVarNameForService(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func Test_skipStatusCheckEnvVarNameForService(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple", "web", "AZD_DEPLOY_WEB_SKIP_STATUS_CHECK"},
		{"withHyphens", "my-web-app", "AZD_DEPLOY_MY_WEB_APP_SKIP_STATUS_CHECK"},
		{"uppercase", "MyApp", "AZD_DEPLOY_MYAPP_SKIP_STATUS_CHECK"},
		{"mixed", "my-App-2", "AZD_DEPLOY_MY_APP_2_SKIP_STATUS_CHECK"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := skipStatusCheckEnvVarNameForService(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
