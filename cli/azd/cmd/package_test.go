// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func Test_NewPackageAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &packageFlags{}
	console := mockinput.NewMockConsole()
	formatter := &output.JsonFormatter{}
	a := newPackageAction(
		flags, nil, nil, nil, nil, console, formatter, io.Discard, nil,
	)
	pa := a.(*packageAction)
	require.Same(t, flags, pa.flags)
}

func Test_NewPackageCmd(t *testing.T) {
	t.Parallel()
	cmd := newPackageCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "package")
}

func Test_NewPackageFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newPackageFlags(cmd, global)
	require.NotNil(t, flags)
}
