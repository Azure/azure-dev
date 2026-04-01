// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestGetCmdTemplateHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "template"}
	result := getCmdTemplateHelpDescription(cmd)
	require.Contains(t, result, "template")
}

func TestGetCmdTemplateHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "template"}
	result := getCmdTemplateHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

func TestGetCmdTemplateSourceHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "source"}
	result := getCmdTemplateSourceHelpDescription(cmd)
	require.Contains(t, result, "source")
}

func TestGetCmdTemplateSourceHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "source"}
	result := getCmdTemplateSourceHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

func TestGetCmdTemplateSourceAddHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "add"}
	result := getCmdTemplateSourceAddHelpDescription(cmd)
	require.NotEmpty(t, result)
}

func TestGetCmdTemplateSourceAddHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "add"}
	result := getCmdTemplateSourceAddHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

func TestNewTemplateListCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateListCmd()
	require.Contains(t, cmd.Use, "list")
	require.NotEmpty(t, cmd.Short)
}

func TestNewTemplateShowCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateShowCmd()
	require.Contains(t, cmd.Use, "show")
	require.NotEmpty(t, cmd.Short)
}

func TestNewTemplateSourceListCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateSourceListCmd()
	require.Contains(t, cmd.Use, "list")
	require.NotEmpty(t, cmd.Short)
}

func TestNewTemplateSourceAddCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateSourceAddCmd()
	require.Contains(t, cmd.Use, "add")
	require.NotEmpty(t, cmd.Short)
}

func TestNewTemplateSourceRemoveCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateSourceRemoveCmd()
	require.Contains(t, cmd.Use, "remove")
	require.NotEmpty(t, cmd.Short)
}

func TestNewTemplateListFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "list"}
	flags := newTemplateListFlags(cmd)
	require.NotNil(t, flags)
	// Verify source flag is registered
	f := cmd.Flags().Lookup("source")
	require.NotNil(t, f)
}
