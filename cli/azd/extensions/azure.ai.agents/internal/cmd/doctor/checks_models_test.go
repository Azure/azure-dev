// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"testing"

	"azureaiagent/internal/cmd/nextstep"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestCheckModels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		deps       Dependencies
		wantStatus Status
		wantText   string
	}{
		{
			name:       "missing client skips",
			wantStatus: StatusSkip,
			wantText:   "not reachable",
		},
		{
			name: "load errors fail",
			deps: Dependencies{
				AzdClient: &azdext.AzdClient{},
				assembleState: fixedAssembler(&nextstep.State{
					ModelLoadErrors: []string{"missing deployment ref"},
				}),
			},
			wantStatus: StatusFail,
			wantText:   "missing deployment ref",
		},
		{
			name: "no models skips",
			deps: Dependencies{
				AzdClient:     &azdext.AzdClient{},
				assembleState: fixedAssembler(&nextstep.State{}),
			},
			wantStatus: StatusSkip,
			wantText:   "no model resources",
		},
		{
			name: "valid models pass",
			deps: Dependencies{
				AzdClient: &azdext.AzdClient{},
				assembleState: fixedAssembler(&nextstep.State{
					HasModels: true,
				}),
			},
			wantStatus: StatusPass,
			wantText:   "configuration loaded",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := newCheckModels(test.deps).Fn(
				t.Context(),
				Options{},
				nil,
			)
			require.Equal(t, test.wantStatus, result.Status)
			require.Contains(t, result.Message, test.wantText)
		})
	}
}
