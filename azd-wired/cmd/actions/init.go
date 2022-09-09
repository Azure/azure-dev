package actions

import (
	"actioncli/pkg/action"
	"actioncli/pkg/azddir"
	"context"
	"fmt"

	"github.com/google/wire"
	"github.com/spf13/pflag"
)

type DirectoryService interface {
	ProjectDirectory() string
}

type GlobalFlags struct {
	NoPrompt bool
}

type InitFlags struct {
	Template string
	Global   *GlobalFlags
}

func (f *InitFlags) Setup(flags *pflag.FlagSet, global *GlobalFlags) {
	flags.StringVarP(&f.Template, "template", "t", "", "Template name")
	f.Global = global
}

type InitAction[f InitFlags] struct {
	dir DirectoryService
}

func NewInitAction(dir DirectoryService) (*InitAction[InitFlags], error) {
	return &InitAction[InitFlags]{dir: dir}, nil
}

func (ia *InitAction[f]) Run(ctx context.Context, flags InitFlags, args []string) error {
	fmt.Printf("noprompt:%v\n", flags.Global.NoPrompt)
	fmt.Printf("flags: [template:%s]\n", flags.Template)
	fmt.Printf("args:%v\n", args)
	fmt.Printf("init called. dir:\n%s\n", ia.dir.ProjectDirectory())
	return nil
}

var InitSet = wire.NewSet(
	azddir.New,
	wire.Bind(new(DirectoryService), new(*azddir.AzdDirectoryService)),
	NewInitAction,
	wire.Bind(new(action.Action[InitFlags]), new(*InitAction[InitFlags])))
