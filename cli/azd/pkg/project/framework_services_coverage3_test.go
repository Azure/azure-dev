// Copyright (c) Microsoft Corporation. Licensed under the MIT License.
// Consolidated tests for framework service Requirements, RequiredExternalTools, and Initialize
// for python, node, maven, and dotnet framework services.
package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/javac"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/node"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Python framework service ---

func Test_pythonProject_Requirements_Coverage3(t *testing.T) {
	p := NewPythonProject(python.NewCli(exec.NewCommandRunner(nil)), environment.NewWithValues("test", nil))
	reqs := p.Requirements()
	assert.False(t, reqs.Package.RequireRestore)
	assert.False(t, reqs.Package.RequireBuild)
}

func Test_pythonProject_RequiredExternalTools_Coverage3(t *testing.T) {
	cli := python.NewCli(exec.NewCommandRunner(nil))
	p := NewPythonProject(cli, environment.NewWithValues("test", nil))
	tools := p.RequiredExternalTools(t.Context(), &ServiceConfig{})
	require.Len(t, tools, 1)
	assert.Equal(t, cli, tools[0])
}

func Test_pythonProject_Initialize_Coverage3(t *testing.T) {
	p := NewPythonProject(python.NewCli(exec.NewCommandRunner(nil)), environment.NewWithValues("test", nil))
	err := p.Initialize(t.Context(), &ServiceConfig{})
	require.NoError(t, err)
}

// --- Node framework service ---

func Test_nodeProject_Requirements_Coverage3(t *testing.T) {
	p := NewNodeProject(
		node.NewCli(exec.NewCommandRunner(nil)),
		environment.NewWithValues("test", nil),
		exec.NewCommandRunner(nil),
	)
	reqs := p.Requirements()
	assert.True(t, reqs.Package.RequireRestore)
	assert.False(t, reqs.Package.RequireBuild)
}

func Test_nodeProject_RequiredExternalTools_Coverage3(t *testing.T) {
	cli := node.NewCli(exec.NewCommandRunner(nil))
	p := NewNodeProject(cli, environment.NewWithValues("test", nil), exec.NewCommandRunner(nil))

	// Provide a ServiceConfig with a valid Project to avoid nil pointer in Path()
	svcConfig := &ServiceConfig{
		Project:      &ProjectConfig{Path: t.TempDir()},
		RelativePath: ".",
	}
	tools := p.RequiredExternalTools(t.Context(), svcConfig)
	require.Len(t, tools, 1)
}

func Test_nodeProject_Initialize_Coverage3(t *testing.T) {
	p := NewNodeProject(
		node.NewCli(exec.NewCommandRunner(nil)),
		environment.NewWithValues("test", nil),
		exec.NewCommandRunner(nil),
	)
	err := p.Initialize(t.Context(), &ServiceConfig{})
	require.NoError(t, err)
}

// --- Maven framework service ---

func Test_mavenProject_Requirements_Coverage3(t *testing.T) {
	p := NewMavenProject(
		environment.NewWithValues("test", nil),
		maven.NewCli(exec.NewCommandRunner(nil)),
		javac.NewCli(exec.NewCommandRunner(nil)),
	)
	reqs := p.Requirements()
	assert.False(t, reqs.Package.RequireRestore)
	assert.False(t, reqs.Package.RequireBuild)
}

func Test_mavenProject_RequiredExternalTools_Coverage3(t *testing.T) {
	mvnCli := maven.NewCli(exec.NewCommandRunner(nil))
	javaCli := javac.NewCli(exec.NewCommandRunner(nil))
	p := NewMavenProject(environment.NewWithValues("test", nil), mvnCli, javaCli)
	tools := p.RequiredExternalTools(t.Context(), &ServiceConfig{})
	require.Len(t, tools, 2)
	assert.Equal(t, mvnCli, tools[0])
	assert.Equal(t, javaCli, tools[1])
}

// --- DotNet framework service ---

func Test_dotnetProject_Requirements_Coverage3(t *testing.T) {
	p := NewDotNetProject(dotnet.NewCli(exec.NewCommandRunner(nil)), environment.NewWithValues("test", nil))
	reqs := p.Requirements()
	assert.False(t, reqs.Package.RequireRestore)
	assert.False(t, reqs.Package.RequireBuild)
}

func Test_dotnetProject_RequiredExternalTools_Coverage3(t *testing.T) {
	cli := dotnet.NewCli(exec.NewCommandRunner(nil))
	p := NewDotNetProject(cli, environment.NewWithValues("test", nil))
	tools := p.RequiredExternalTools(t.Context(), &ServiceConfig{})
	require.Len(t, tools, 1)
	assert.Equal(t, cli, tools[0])
}
