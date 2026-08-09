// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"azureaieval/internal/project"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `init --path ./quality` wrote a configuration that `run` then looked for
// under ./evals and reported as missing, while azure.yaml's $ref pointed at it
// correctly the whole time. The path init used is remembered so the flag does
// not have to be repeated on every later command.
func TestEvalDirCascade(t *testing.T) {
	// No azd environment: getEnvValue returns empty, so only flag and default apply.
	ec := &evalContext{}

	assert.Equal(t, project.DefaultEvalDir, ec.evalDir(context.Background(), ""),
		"nothing given anywhere is ./evals")
	assert.Equal(t, "quality", ec.evalDir(context.Background(), "quality"),
		"--path wins")
}

// Every command that reads the configuration has to be able to say where it is,
// or a project scaffolded with --path is unreachable from that command.
func TestCommandsReadingTheConfigTakePath(t *testing.T) {
	for _, path := range []string{"run start", "init", "generate"} {
		cmd := find(t, path)
		assert.NotNilf(t, cmd.Flags().Lookup("path"),
			"%s reads the configuration, so it must accept --path", path)
	}
}

// --path defaults to empty, not to ./evals, so "not given" stays
// distinguishable from "given the default". Defaulting it to ./evals would
// shadow the path init recorded and reintroduce the bug.
func TestRunPathFlagDefaultsToEmpty(t *testing.T) {
	flag := find(t, "run start").Flags().Lookup("path")
	require.NotNil(t, flag)
	assert.Empty(t, flag.DefValue,
		"a non-empty default would always win over the recorded path")
}

// The recorded key is what `init` writes and what the other commands read; a
// rename on one side alone silently stops the hand-off working.
func TestEvalPathEnvKey(t *testing.T) {
	assert.Equal(t, "EVAL_CONFIG_PATH", envKeyEvalPath)
}

// find is shared with surface_test.go; this keeps the compiler honest about it.
var _ = func(t *testing.T) *cobra.Command { return find(t, "run start") }
