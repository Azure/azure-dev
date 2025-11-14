// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"testing"

	"github.com/azure/azure-dev/test/snapshot"
	"github.com/stretchr/testify/require"
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
