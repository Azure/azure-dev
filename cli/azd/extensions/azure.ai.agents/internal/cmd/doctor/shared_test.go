// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"

	"azureaiagent/internal/cmd/nextstep"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// fixedAssembler returns an assembleState stub that yields the given
// State on every call. Used by check tests to inject manifest-walker
// outputs (HasModels / HasToolboxes / HasConnections, ModelRefs, etc.)
// without touching disk or invoking the production walker.
func fixedAssembler(
	state *nextstep.State,
) func(context.Context, *azdext.AzdClient) (*nextstep.State, []error) {
	return func(_ context.Context, _ *azdext.AzdClient) (*nextstep.State, []error) {
		return state, nil
	}
}

// fixedProjectIDReader returns a readProjectResourceIDFn that yields
// the supplied id (or error) on every call. Mirrors the rbac test
// pattern so checks that derive an ARM scope from AZURE_AI_PROJECT_ID
// can be exercised without a real azd env.
func fixedProjectIDReader(
	id string, err error,
) func(context.Context, *azdext.AzdClient) (string, error) {
	return func(_ context.Context, _ *azdext.AzdClient) (string, error) {
		return id, err
	}
}

// validProjectResourceID is a canonical Foundry project ARM resource
// ID used by remote check tests that derive an ARM scope from
// AZURE_AI_PROJECT_ID. It parses cleanly through both
// `parseAccountProjectFromProjectID` (connections check) and any
// other ARM-ID parser into:
//
//	subscription = 00000000-0000-0000-0000-000000000000
//	resourceGroup = rg-bugbash
//	account = acct-1
//	project = proj-1
const validProjectResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000" +
	"/resourceGroups/rg-bugbash" +
	"/providers/Microsoft.CognitiveServices/accounts/acct-1/projects/proj-1"
