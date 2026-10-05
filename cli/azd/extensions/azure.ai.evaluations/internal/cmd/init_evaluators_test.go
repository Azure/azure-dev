// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"testing"

	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// What an eval grades on is a SET, so there is no "the only one" to detect the
// way there is for the target and the judge model. Which criteria define
// quality is the substantive decision in the configuration, so init asks.
//
// The defaults are the two production composites. Their component evaluators
// must not also be selected, or the same dimension is scored twice.
func TestDefaultEvaluatorsUseProductionCompositesWithoutConstituents(t *testing.T) {
	assert.Equal(t, []string{
		evalcore.BuiltinPrefix + "output_quality",
		evalcore.BuiltinPrefix + "tool_use_quality",
	}, defaultEvaluators())
}

// The recommendations are the production composite set, not their standalone
// constituents.
func TestEvaluatorChoicesOfferProductionComposites(t *testing.T) {
	assert.Equal(t, []string{
		evalcore.BuiltinPrefix + "output_quality",
		evalcore.BuiltinPrefix + "tool_use_quality",
	}, evaluatorChoices(nil))
}

// The prompt offers the curated composites plus whatever the local catalog
// already declares. A custom declaration is offered because its file already
// exists; nothing that would have to be generated first appears here.
func TestEvaluatorChoicesOfferTheCatalogToo(t *testing.T) {
	cfg := &project.EvalConfig{Evaluators: []project.EvaluatorDecl{
		{Name: "support-agent-quality"},
		{Name: "tone-check"},
	}}

	got := evaluatorChoices(cfg)

	assert.Equal(t, []string{
		evalcore.BuiltinPrefix + "output_quality",
		evalcore.BuiltinPrefix + "tool_use_quality",
		"support-agent-quality",
		"tone-check",
	}, got)
}

func TestResolveEvaluatorsNoPromptUsesAvailableCompositeDefaults(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-prompt", true, "")

	got, chosen, err := resolveEvaluators(cmd, nil, defaultEvaluators())

	require.NoError(t, err)
	assert.False(t, chosen)
	assert.Equal(t, defaultEvaluators(), got)
}

func TestResolveEvaluatorsRefusesUnavailableCompositeDefaults(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-prompt", true, "")

	_, _, err := resolveEvaluators(cmd, nil,
		[]string{evalcore.BuiltinPrefix + "output_quality"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), evalcore.BuiltinPrefix+"tool_use_quality")
}

type evaluatorPickerServer struct {
	azdext.UnimplementedPromptServiceServer
	response *azdext.MultiSelectResponse
	requests chan *azdext.MultiSelectRequest
}

func (s *evaluatorPickerServer) MultiSelect(
	_ context.Context, req *azdext.MultiSelectRequest,
) (*azdext.MultiSelectResponse, error) {
	s.requests <- req
	return s.response, nil
}

func serveEvaluatorPicker(t *testing.T, selected ...string) *evaluatorPickerServer {
	t.Helper()
	values := make([]*azdext.MultiSelectChoice, 0, len(selected))
	for _, value := range selected {
		values = append(values, &azdext.MultiSelectChoice{Value: value})
	}
	picker := &evaluatorPickerServer{
		response: &azdext.MultiSelectResponse{Values: values},
		requests: make(chan *azdext.MultiSelectRequest, 1),
	}
	server := grpc.NewServer()
	azdext.RegisterPromptServiceServer(server, picker)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	t.Setenv("AZD_SERVER", listener.Addr().String())
	t.Setenv("AZD_NO_PROMPT", "false")
	return picker
}

func TestResolveEvaluatorsInteractivePreselectsCompositesAndPreservesSelection(t *testing.T) {
	selected := []string{evalcore.BuiltinPrefix + "tool_use_quality"}
	picker := serveEvaluatorPicker(t, selected...)
	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-prompt", false, "")
	cmd.Flags().String("output", "", "")
	cmd.SetContext(t.Context())

	got, chosen, err := resolveEvaluators(cmd, nil, defaultEvaluators())

	require.NoError(t, err)
	assert.True(t, chosen)
	assert.Equal(t, selected, got, "the response replaces rather than merges with the preselection")
	require.Len(t, picker.requests, 1)
	req := <-picker.requests
	require.Len(t, req.Options.Choices, 2)
	for _, choice := range req.Options.Choices {
		assert.True(t, choice.Selected, "%s should be recommended", choice.Value)
	}
}
