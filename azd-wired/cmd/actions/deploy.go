package actions

import (
	"actioncli/pkg/action"
	"actioncli/pkg/azddir"
	"context"
	"fmt"

	"github.com/google/wire"
	"github.com/spf13/pflag"
)

func (f *DeployFlags) Setup(flags *pflag.FlagSet, global *GlobalFlags) {
	flags.StringVarP(&f.Service, "service", "s", "", "Service name")
	f.Global = global
}

type DeployFlags struct {
	Service string
	Global  *GlobalFlags
}

type DeployAction[f DeployFlags] struct {
	dir *azddir.AzdDirectoryService
}

func NewDeployAction(dir *azddir.AzdDirectoryService) (*DeployAction[DeployFlags], error) {
	return &DeployAction[DeployFlags]{dir: dir}, nil
}

func (ia *DeployAction[F]) Run(ctx context.Context, flags DeployFlags, args []string) error {
	fmt.Printf("noprompt:%v\n", flags.Global.NoPrompt)
	fmt.Printf("flags: [service:%s]\n", flags.Service)
	fmt.Printf("args:%v\n", args)
	fmt.Printf("Deploy called. dir:\n%s\n", ia.dir.ProjectDirectory())
	return nil
}

var DeploySet = wire.NewSet(
	azddir.New,
	wire.Bind(new(DirectoryService), new(*azddir.AzdDirectoryService)),
	NewDeployAction,
	wire.Bind(new(action.Action[DeployFlags]), new(*DeployAction[DeployFlags])))
