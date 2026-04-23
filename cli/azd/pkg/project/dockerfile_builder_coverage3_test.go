// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
