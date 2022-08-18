package helpers

import (
	"context"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/httpUtil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

func CreateTestContext(ctx context.Context, options *commands.GlobalCommandOptions, azCli azcli.AzCli, httpClient httpUtil.HttpUtil) context.Context {
	newContext := context.WithValue(ctx, environment.OptionsContextKey, options)
	newContext = context.WithValue(newContext, environment.AzdCliContextKey, azCli)
	newContext = context.WithValue(newContext, environment.HttpUtilContextKey, httpClient)

	return newContext
}

// CallStackContains checks wither the specified function name exists in the call stack
func CallStackContains(funcName string) bool {
	skip := 1
	for {
		pc, _, _, ok := runtime.Caller(skip)
		if !ok {
			return false
		}

		details := runtime.FuncForPC(pc)
		if strings.Contains(details.Name(), funcName) {
			return true
		}

		skip += 1
	}
}
