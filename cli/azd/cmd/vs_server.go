// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/vsrpc"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type vsServerFlags struct {
	global *internal.GlobalCommandOptions
	port   int
}

func (s *vsServerFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	s.global = global
	local.IntVar(&s.port, "port", 0, "Port to listen on (0 for random port)")
}

func newVsServerFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *vsServerFlags {
	flags := &vsServerFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newVsServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Hidden: true,
		Use:    "vs-server",
		Short:  "Run Server",
	}

	return cmd
}

type vsServerAction struct {
	rootContainer *ioc.NestedContainer
	flags         *vsServerFlags
}

func newVsServerAction(rootContainer *ioc.NestedContainer, flags *vsServerFlags) actions.Action {
	return &vsServerAction{
		rootContainer: rootContainer,
		flags:         flags,
	}
}

func (s *vsServerAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.flags.port))
	if err != nil {
		panic(err)
	}

	var versionRes contracts.VersionResult
	versionSpec := internal.VersionInfo()

	versionRes.Azd.Commit = versionSpec.Commit
	versionRes.Azd.Version = versionSpec.Version.String()

	res := contracts.VsServerResult{
		Port:          listener.Addr().(*net.TCPAddr).Port,
		Pid:           os.Getpid(),
		VersionResult: versionRes,
	}

	resString, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}

	fmt.Printf("%s\n", string(resString))

	return nil, vsrpc.NewServer(s.rootContainer).Serve(listener)
}
