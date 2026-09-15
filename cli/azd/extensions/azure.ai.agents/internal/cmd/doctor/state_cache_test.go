// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"testing"

	"azureaiagent/internal/cmd/nextstep"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestAssembleAgentStateCachesSnapshot(t *testing.T) {
	t.Parallel()

	var calls int
	state := &nextstep.State{HasProjectEndpoint: true}
	deps := Dependencies{
		StateCache: NewStateCache(),
		assembleState: func(_ context.Context, _ *azdext.AzdClient) (
			*nextstep.State, []error,
		) {
			calls++
			return state, nil
		},
	}

	first, firstErrs := deps.AssembleAgentState(t.Context())
	second, secondErrs := deps.AssembleAgentState(t.Context())

	require.Same(t, state, first)
	require.Same(t, first, second)
	require.Empty(t, firstErrs)
	require.Empty(t, secondErrs)
	require.Equal(t, 1, calls)
}
