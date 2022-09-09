package actions

import (
	"actioncli/pkg/action"
	"actioncli/pkg/azddir"
	"context"

	"github.com/google/wire"
	"github.com/spf13/pflag"
)

type UpFlags struct {
	InitFlags
	DeployFlags
}

func (f *UpFlags) Setup(flags *pflag.FlagSet, global *GlobalFlags) {
	f.InitFlags.Setup(flags, global)
	f.DeployFlags.Setup(flags, global)
}

type UpAction[f UpFlags] struct {
	init   *InitAction[InitFlags]
	deploy *DeployAction[DeployFlags]
}

func NewUpAction(
	init *InitAction[InitFlags],
	deploy *DeployAction[DeployFlags]) (*UpAction[UpFlags], error) {
	return &UpAction[UpFlags]{init: init, deploy: deploy}, nil
}

func (ia *UpAction[f]) Run(ctx context.Context, flags UpFlags, args []string) error {
	err := ia.init.Run(ctx, flags.InitFlags, args)
	if err != nil {
		return err
	}

	err = ia.deploy.Run(ctx, flags.DeployFlags, args)
	return err
}

var UpSet = wire.NewSet(
	azddir.New,
	wire.Bind(new(DirectoryService), new(*azddir.AzdDirectoryService)),
	NewDeployAction,
	NewInitAction,
	NewUpAction,
	wire.Bind(new(action.Action[UpFlags]), new(*UpAction[UpFlags])))
