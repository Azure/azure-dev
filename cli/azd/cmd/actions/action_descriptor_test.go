package actions_test

import (
	"reflect"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func Test_SingleAction(t *testing.T) {
	ad := actions.NewActionDescriptor("single", &actions.ActionDescriptorOptions{})

	require.Nil(t, ad.Parent())
	require.Len(t, ad.Children(), 0)
	require.Len(t, ad.Middleware(), 0)
	require.Len(t, ad.FlagCompletions(), 0)
}

func Test_SingleActionWithoutOptions(t *testing.T) {
	ad := actions.NewActionDescriptor("single", nil)
	require.Equal(t, "single", ad.Name)
	require.Equal(t, "single", ad.Options.Command.Use)
}

func Test_ActionGroup(t *testing.T) {
	group := actions.NewActionDescriptor("group", &actions.ActionDescriptorOptions{})
	child := group.Add("child", &actions.ActionDescriptorOptions{})

	require.Nil(t, group.Parent())
	require.NotNil(t, child.Parent())
	require.Len(t, group.Children(), 1)
	require.Len(t, group.Middleware(), 0)
	require.Len(t, group.FlagCompletions(), 0)
}

func Test_NestedActionGroup(t *testing.T) {
	root := actions.NewActionDescriptor("root", &actions.ActionDescriptorOptions{})
	group := root.Add("group", &actions.ActionDescriptorOptions{})
	child := group.Add("child", &actions.ActionDescriptorOptions{})

	require.Nil(t, root.Parent())
	require.Exactly(t, root, group.Parent())
	require.NotNil(t, group, child.Parent())

	require.Len(t, root.Children(), 1)
	require.Len(t, group.Children(), 1)
}

func Test_MiddlewareRegistration(t *testing.T) {
	middlewarePredicate := func(descriptor *actions.ActionDescriptor) bool {
		return !descriptor.Options.DisableTelemetry
	}

	root := actions.NewActionDescriptor("root", &actions.ActionDescriptorOptions{})
	root.UseMiddleware("debug", middleware.NewDebugMiddleware)
	root.UseMiddlewareWhen("telemetry", middleware.NewTelemetryMiddleware, middlewarePredicate)

	require.Len(t, root.Middleware(), 2)
	require.Equal(t, "debug", root.Middleware()[0].Name)
	FuncsEqual(t, middleware.NewDebugMiddleware, root.Middleware()[0].Resolver)
	require.Nil(t, root.Middleware()[0].Predicate)

	require.Equal(t, "telemetry", root.Middleware()[1].Name)
	FuncsEqual(t, middleware.NewTelemetryMiddleware, root.Middleware()[1].Resolver)
	FuncsEqual(t, middlewarePredicate, root.Middleware()[1].Predicate)
}

func Test_CompletionRegistration(t *testing.T) {
	root := actions.NewActionDescriptor("root", &actions.ActionDescriptorOptions{})
	root.AddFlagCompletion(
		"template",
		func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return []string{}, cobra.ShellCompDirectiveDefault
		},
	)

	require.Len(t, root.FlagCompletions(), 1)
}

func FuncsEqual(t *testing.T, expected any, actual any) {
	require.Equal(t, reflect.ValueOf(expected).Pointer(), reflect.ValueOf(actual).Pointer())
}
