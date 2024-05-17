package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type contextKey string

const actionName contextKey = "action"
const middlewareAName contextKey = "middleware-A"
const middlewareBName contextKey = "middleware-B"

func Test_BuildAndRunSimpleCommand(t *testing.T) {
	ran := false
	container := ioc.NewNestedContainer(nil)

	root := actions.NewActionDescriptor("root", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			RunE: func(cmd *cobra.Command, args []string) error {
				ran = true
				return nil
			},
		},
	})

	builder := NewCobraBuilder(container)
	cmd, err := builder.BuildCommand(root)

	require.NotNil(t, cmd)
	require.NoError(t, err)

	// Disable args processing from os:args
	cmd.SetArgs([]string{})
	err = cmd.ExecuteContext(context.Background())

	require.NoError(t, err)
	require.True(t, ran)
}

func Test_BuildAndRunSimpleAction(t *testing.T) {
	container := ioc.NewNestedContainer(nil)
	setup(container)

	root := actions.NewActionDescriptor("root", &actions.ActionDescriptorOptions{
		ActionResolver: newTestAction,
		FlagsResolver:  newTestFlags,
	})

	builder := NewCobraBuilder(container)
	cmd, err := builder.BuildCommand(root)

	require.NotNil(t, cmd)
	require.NoError(t, err)

	cmd.SetArgs([]string{"-r"})
	err = cmd.ExecuteContext(context.Background())

	require.NoError(t, err)
}

func Test_BuildAndRunSimpleActionWithMiddleware(t *testing.T) {
	container := ioc.NewNestedContainer(nil)
	setup(container)

	root := actions.NewActionDescriptor("root", &actions.ActionDescriptorOptions{
		ActionResolver: newTestAction,
		FlagsResolver:  newTestFlags,
	}).UseMiddleware("A", newTestMiddlewareA)

	builder := NewCobraBuilder(container)
	cmd, err := builder.BuildCommand(root)

	require.NotNil(t, cmd)
	require.NoError(t, err)

	actionRan := false
	middlewareRan := false

	ctx := context.Background()
	ctx = context.WithValue(ctx, actionName, &actionRan)
	ctx = context.WithValue(ctx, middlewareAName, &middlewareRan)

	cmd.SetArgs([]string{"-r"})
	err = cmd.ExecuteContext(ctx)

	require.NoError(t, err)
	require.True(t, actionRan)
	require.True(t, middlewareRan)
}

func Test_BuildAndRunActionWithNestedMiddleware(t *testing.T) {
	container := ioc.NewNestedContainer(nil)
	setup(container)

	root := actions.NewActionDescriptor("root", nil).
		UseMiddleware("A", newTestMiddlewareA)

	root.Add("child", &actions.ActionDescriptorOptions{
		ActionResolver: newTestAction,
		FlagsResolver:  newTestFlags,
	}).UseMiddleware("B", newTestMiddlewareB)

	builder := NewCobraBuilder(container)
	cmd, err := builder.BuildCommand(root)

	require.NotNil(t, cmd)
	require.NoError(t, err)

	actionRan := false
	middlewareARan := false
	middlewareBRan := false

	ctx := context.Background()
	ctx = context.WithValue(ctx, actionName, &actionRan)
	ctx = context.WithValue(ctx, middlewareAName, &middlewareARan)
	ctx = context.WithValue(ctx, middlewareBName, &middlewareBRan)

	cmd.SetArgs([]string{"child", "-r"})
	err = cmd.ExecuteContext(ctx)

	require.NoError(t, err)
	require.True(t, actionRan)
	require.True(t, middlewareARan)
	require.True(t, middlewareBRan)
}

func Test_BuildAndRunActionWithNestedAndConditionalMiddleware(t *testing.T) {
	container := ioc.NewNestedContainer(nil)
	setup(container)

	root := actions.NewActionDescriptor("root", nil).
		// This middleware will always run because its registered at the root
		UseMiddleware("A", newTestMiddlewareA)

	root.Add("child", &actions.ActionDescriptorOptions{
		ActionResolver: newTestAction,
		FlagsResolver:  newTestFlags,
	}).
		// This middleware is an example of a middleware that will only be registered if it passes
		// the predicate. Typically this would be based on a value in the action descriptor.
		UseMiddlewareWhen("B", newTestMiddlewareB, func(descriptor *actions.ActionDescriptor) bool {
			return false
		})

	builder := NewCobraBuilder(container)
	cmd, err := builder.BuildCommand(root)

	require.NotNil(t, cmd)
	require.NoError(t, err)

	actionRan := false
	middlewareARan := false
	middlewareBRan := false

	ctx := context.Background()
	ctx = context.WithValue(ctx, actionName, &actionRan)
	ctx = context.WithValue(ctx, middlewareAName, &middlewareARan)
	ctx = context.WithValue(ctx, middlewareBName, &middlewareBRan)

	cmd.SetArgs([]string{"child", "-r"})
	err = cmd.ExecuteContext(ctx)

	require.NoError(t, err)
	require.True(t, actionRan)
	require.True(t, middlewareARan)
	require.False(t, middlewareBRan)
}

func Test_BuildCommandsWithAutomaticHelpAndOutputFlags(t *testing.T) {
	container := ioc.NewNestedContainer(nil)

	root := actions.NewActionDescriptor("root", &actions.ActionDescriptorOptions{
		OutputFormats: []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat: output.TableFormat,
		Command: &cobra.Command{
			RunE: func(cmd *cobra.Command, args []string) error {
				return nil
			},
		},
	})

	cobraBuilder := NewCobraBuilder(container)
	cmd, err := cobraBuilder.BuildCommand(root)

	require.NoError(t, err)
	require.NotNil(t, cmd)

	helpFlag := cmd.Flag("help")
	outputFlag := cmd.Flag("output")
	docsFlag := cmd.Flag("docs")

	require.NotNil(t, helpFlag)
	require.Equal(t, "help", helpFlag.Name)
	require.Equal(t, "h", helpFlag.Shorthand)
	require.Equal(t, "Gets help for root.", helpFlag.Usage)

	require.NotNil(t, docsFlag)
	require.Equal(t, "docs", docsFlag.Name)
	require.Equal(t, "", docsFlag.Shorthand)
	require.Equal(t, "Opens the documentation for root in your web browser.", docsFlag.Usage)

	require.NotNil(t, outputFlag)
	require.Equal(t, "output", outputFlag.Name)
	require.Equal(t, "o", outputFlag.Shorthand)
	require.Equal(t, "The output format (the supported formats are json, table).", outputFlag.Usage)
}

func Test_RunDocsFlow(t *testing.T) {
	container := ioc.NewNestedContainer(nil)
	testCtx := mocks.NewMockContext(context.Background())
	container.MustRegisterSingleton(func() input.Console {
		return testCtx.Console
	})

	root := actions.NewActionDescriptor("root", &actions.ActionDescriptorOptions{
		OutputFormats: []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat: output.TableFormat,
		Command: &cobra.Command{
			RunE: func(cmd *cobra.Command, args []string) error {
				return nil
			},
		},
	})

	var calledUrl string
	overrideBrowser = func(ctx context.Context, console input.Console, url string) {
		calledUrl = url
	}

	cobraBuilder := NewCobraBuilder(container)
	cmd, err := cobraBuilder.BuildCommand(root)

	require.NoError(t, err)
	require.NotNil(t, cmd)

	cmd.SetArgs([]string{"--docs"})
	err = cmd.ExecuteContext(*testCtx.Context)
	require.NoError(t, err)
	require.Equal(t, cReferenceDocumentationUrl+"root", calledUrl)
}

func Test_RunDocsAndHelpFlow(t *testing.T) {
	container := ioc.NewNestedContainer(nil)
	testCtx := mocks.NewMockContext(context.Background())
	container.MustRegisterSingleton(func() input.Console {
		return testCtx.Console
	})

	root := actions.NewActionDescriptor("root", &actions.ActionDescriptorOptions{
		OutputFormats: []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat: output.TableFormat,
		Command: &cobra.Command{
			RunE: func(cmd *cobra.Command, args []string) error {
				return nil
			},
		},
	})

	var calledUrl string
	overrideBrowser = func(ctx context.Context, console input.Console, url string) {
		calledUrl = url
	}

	cobraBuilder := NewCobraBuilder(container)
	cmd, err := cobraBuilder.BuildCommand(root)

	require.NoError(t, err)
	require.NotNil(t, cmd)

	// having both args should honor help
	cmd.SetArgs([]string{"--docs", "--help"})
	err = cmd.ExecuteContext(*testCtx.Context)
	require.NoError(t, err)
	require.Equal(t, "", calledUrl)
}

func setup(container *ioc.NestedContainer) {
	registerCommonDependencies(container)
	globalOptions := &internal.GlobalCommandOptions{
		EnableTelemetry:    false,
		EnableDebugLogging: false,
	}
	ioc.RegisterInstance(container, globalOptions)
}

// Types for test

// Action

type testFlags struct {
	ran bool
}

func newTestFlags(cmd *cobra.Command) *testFlags {
	flags := &testFlags{}
	flags.Bind(cmd)

	return flags
}

func (a *testFlags) Bind(cmd *cobra.Command) {
	cmd.Flags().BoolVarP(&a.ran, "ran", "r", false, "sets whether the test command ran")
}

type testAction struct {
	flags *testFlags
}

func newTestAction(flags *testFlags) actions.Action {
	return &testAction{
		flags: flags,
	}
}

func (a *testAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	actionRan, ok := ctx.Value(actionName).(*bool)
	if ok {
		*actionRan = true
	}

	if !a.flags.ran {
		return nil, errors.New("flag was not set")
	}

	return nil, nil
}

// Middleware

type testMiddlewareA struct {
}

func newTestMiddlewareA() middleware.Middleware {
	return &testMiddlewareA{}
}

func (m *testMiddlewareA) Run(ctx context.Context, nextFn middleware.NextFn) (*actions.ActionResult, error) {
	middlewareRan, ok := ctx.Value(middlewareAName).(*bool)
	if ok {
		*middlewareRan = true
	}

	return nextFn(ctx)
}

type testMiddlewareB struct {
}

func newTestMiddlewareB() middleware.Middleware {
	return &testMiddlewareB{}
}

func (m *testMiddlewareB) Run(ctx context.Context, nextFn middleware.NextFn) (*actions.ActionResult, error) {
	middlewareRan, ok := ctx.Value(middlewareBName).(*bool)
	if ok {
		*middlewareRan = true
	}

	return nextFn(ctx)
}
