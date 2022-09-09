//go:build wireinject
// +build wireinject

package cmd

import (
	. "actioncli/cmd/actions"
	"actioncli/pkg/action"
	"github.com/google/wire"
)

func InjectInitAction() (action.Action[InitFlags], error) {
	panic(wire.Build(InitSet))
}

func InjectDeployAction() (action.Action[DeployFlags], error) {
	panic(wire.Build(DeploySet))
}

func InjectUpAction() (action.Action[UpFlags], error) {
	panic(wire.Build(UpSet))
}
