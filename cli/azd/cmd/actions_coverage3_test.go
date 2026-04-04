// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"io"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

// Constructor tests verify that action constructors correctly assign
// all fields. Each test type-asserts to the concrete struct and checks
// key field assignments after construction.

func Test_NewUploadAction_Constructor(t *testing.T) {
	t.Parallel()
	opts := &internal.GlobalCommandOptions{NoPrompt: true}
	a := newUploadAction(opts)
	ua := a.(*uploadAction)
	require.Same(t, opts, ua.rootOptions)
}

func Test_NewBuildAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &buildFlags{}
	args := []string{"svc"}
	console := mockinput.NewMockConsole()
	formatter := &output.JsonFormatter{}
	a := newBuildAction(
		flags, args, nil, nil, nil, nil, console, formatter, io.Discard, nil,
	)
	ba := a.(*buildAction)
	require.Same(t, flags, ba.flags)
	require.Equal(t, args, ba.args)
}

func Test_NewRestoreAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &restoreFlags{}
	console := mockinput.NewMockConsole()
	formatter := &output.JsonFormatter{}
	a := newRestoreAction(
		flags, nil, console, formatter, io.Discard,
		nil, nil, nil, nil, nil, nil, nil,
	)
	ra := a.(*restoreAction)
	require.Same(t, flags, ra.flags)
}

func Test_NewPackageAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &packageFlags{}
	console := mockinput.NewMockConsole()
	formatter := &output.JsonFormatter{}
	a := newPackageAction(
		flags, nil, nil, nil, nil, console, formatter, io.Discard, nil,
	)
	pa := a.(*packageAction)
	require.Same(t, flags, pa.flags)
}

func Test_NewUpAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &upFlags{}
	console := mockinput.NewMockConsole()
	a := newUpAction(flags, console, nil, nil, nil, nil, nil, nil, nil)
	ua := a.(*upAction)
	require.Same(t, flags, ua.flags)
}

func Test_NewDownAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &downFlags{}
	console := mockinput.NewMockConsole()
	a := newDownAction(nil, flags, nil, nil, nil, nil, console, nil, nil)
	da := a.(*downAction)
	require.Same(t, flags, da.flags)
}

func Test_NewMonitorAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &monitorFlags{}
	console := mockinput.NewMockConsole()
	c := &cloud.Cloud{PortalUrlBase: "https://portal.azure.com"}
	a := newMonitorAction(nil, nil, nil, nil, nil, console, flags, c, nil)
	ma := a.(*monitorAction)
	require.Same(t, flags, ma.flags)
	require.Equal(t, "https://portal.azure.com", ma.portalUrlBase)
}

func Test_NewAuthLoginAction_Constructor(t *testing.T) {
	t.Parallel()
	formatter := &output.JsonFormatter{}
	console := mockinput.NewMockConsole()
	annotations := CmdAnnotations{"key": "value"}
	a := newAuthLoginAction(
		formatter, io.Discard, nil, nil,
		&authLoginFlags{}, console, annotations, nil,
	)
	la := a.(*loginAction)
	require.NotNil(t, la.flags)
	require.Equal(t, annotations, la.annotations)
}

func Test_NewAuthStatusAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &authStatusFlags{}
	formatter := &output.JsonFormatter{}
	console := mockinput.NewMockConsole()
	a := newAuthStatusAction(formatter, io.Discard, nil, flags, console)
	sa := a.(*authStatusAction)
	require.Same(t, flags, sa.flags)
	require.Same(t, formatter, sa.formatter)
}

func Test_NewTemplateListAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &templateListFlags{}
	formatter := &output.JsonFormatter{}
	a := newTemplateListAction(flags, formatter, io.Discard, nil)
	ta := a.(*templateListAction)
	require.Same(t, flags, ta.flags)
	require.Same(t, formatter, ta.formatter)
}

func Test_NewUpdateAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &updateFlags{}
	console := mockinput.NewMockConsole()
	formatter := &output.JsonFormatter{}
	a := newUpdateAction(flags, console, formatter, io.Discard, nil, nil, nil)
	ua := a.(*updateAction)
	require.Same(t, flags, ua.flags)
}

func Test_NewInfraGenerateAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &infraGenerateFlags{}
	console := mockinput.NewMockConsole()
	calledAs := CmdCalledAs("infra generate")
	a := newInfraGenerateAction(nil, nil, flags, console, nil, nil, calledAs)
	ia := a.(*infraGenerateAction)
	require.Same(t, flags, ia.flags)
	require.Equal(t, calledAs, ia.calledAs)
}

func Test_NewHooksRunAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &hooksRunFlags{}
	console := mockinput.NewMockConsole()
	args := []string{"pre-build"}
	a := newHooksRunAction(nil, nil, nil, nil, nil, console, flags, args, nil)
	ha := a.(*hooksRunAction)
	require.Same(t, flags, ha.flags)
	require.Equal(t, args, ha.args)
}

func Test_NewPipelineConfigAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &pipelineConfigFlags{}
	console := mockinput.NewMockConsole()
	a := newPipelineConfigAction(nil, console, flags, nil, nil, nil, nil, nil, nil)
	pa := a.(*pipelineConfigAction)
	require.Same(t, flags, pa.flags)
}

// --- Utility constructor tests ---

func Test_AlphaFeatureManager_WithConfig(t *testing.T) {
	t.Parallel()
	cfg := config.NewEmptyConfig()
	fm := alpha.NewFeaturesManagerWithConfig(cfg)
	require.NotNil(t, fm)
}

func Test_EnvironmentNewWithValues(t *testing.T) {
	t.Parallel()
	env := environment.NewWithValues("testenv", map[string]string{"K": "V"})
	require.NotNil(t, env)
	require.Equal(t, "V", env.Getenv("K"))
}

func Test_ProjectConfig_Basic(t *testing.T) {
	t.Parallel()
	cfg := &project.ProjectConfig{Name: "test"}
	require.Equal(t, "test", cfg.Name)
}

func Test_OutputFormatters(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}

	jsonFmt := &output.JsonFormatter{}
	err := jsonFmt.Format(map[string]string{"k": "v"}, buf, nil)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "k")

	noneFmt := &output.NoneFormatter{}
	err = noneFmt.Format("data", buf, nil)
	require.Error(t, err)
}
