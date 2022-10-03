//go:build wireinject
// +build wireinject

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/action"
	"github.com/google/wire"
)

func injectInitAction() (action.Action[initFlags], error) {
	panic(wire.Build(InitSet))
}
