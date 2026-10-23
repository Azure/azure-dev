// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exec

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"

	"github.com/azure/azure-dev/cli/azd/pkg/exec/internal/process"
)

// CmdTree represents an exec.Cmd with platform-specific process cancellation.
type CmdTree struct {
	CmdTreeOptions
	*exec.Cmd

	ctx  context.Context
	tree *process.Tree
}

func newManagedCmdTree(
	ctx context.Context,
	cmd *exec.Cmd,
	options CmdTreeOptions,
) CmdTree {
	return CmdTree{
		CmdTreeOptions: options,
		Cmd:            cmd,
		ctx:            ctx,
		tree: process.New(ctx, cmd, process.Options{
			Interactive:              options.Interactive,
			TerminateRemainingOnWait: true,
		}),
	}
}

// Start starts the command and configures its managed process scope.
func (c *CmdTree) Start() error {
	if c.Cmd == nil {
		return errors.New("command is nil")
	}
	if c.tree == nil {
		c.tree = process.New(c.ctx, c.Cmd, process.Options{
			Interactive:              c.Interactive,
			TerminateRemainingOnWait: true,
		})
	}
	return c.tree.Start()
}

// Wait waits for the command and redirected I/O to finish.
func (c *CmdTree) Wait() error {
	if c.tree == nil {
		return c.Cmd.Wait()
	}
	return c.tree.Wait()
}

// Kill terminates the managed process scope.
func (c *CmdTree) Kill() {
	var err error
	if c.tree != nil {
		err = c.tree.Kill()
	} else if c.Cmd != nil && c.Cmd.Process != nil {
		err = c.Cmd.Process.Kill()
	} else {
		err = os.ErrProcessDone
	}

	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		log.Printf("failed to terminate managed process scope: %s\n", err)
	}
}
