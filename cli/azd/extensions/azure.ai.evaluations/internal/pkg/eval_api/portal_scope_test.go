// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSub = "/subscriptions/00000000-0000-0000-0000-000000000000"

// Only a Foundry project builds a portal prefix.
//
// The check was "has a parent and a slash in its type", which every nested
// resource satisfies: a storage blob container reached this far and produced a
// confident link to a portal page that cannot exist.
func TestOnlyAFoundryProjectBuildsAPortalPrefix(t *testing.T) {
	project := testSub + "/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/acct/projects/proj"
	_, err := NewPortalPrefix(project)
	require.NoError(t, err, "a Foundry project is the thing this is for")

	notProjects := map[string]string{
		"a storage container": testSub + "/resourceGroups/rg/providers/" +
			"Microsoft.Storage/storageAccounts/sa/blobServices/default",
		"the parent account": testSub + "/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/acct",
		"a different child of the account": testSub + "/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/acct/deployments/dep",
	}
	for what, id := range notProjects {
		_, err := NewPortalPrefix(id)
		assert.Errorf(t, err, "%s is not a Foundry project", what)
	}
}

// Names reach these builders from the service, not from this extension's own
// validation, so a space or a slash would otherwise produce a link that breaks
// when pasted or points at a different route.
func TestPortalURLsEscapeTheNamesTheyCarry(t *testing.T) {
	prefix, err := NewPortalPrefix(testSub + "/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/acct/projects/proj")
	require.NoError(t, err)

	got := prefix.DatasetURL("my set", "1.0")
	assert.NotContains(t, got, "my set", "a raw space does not survive a paste")
	assert.Contains(t, got, "my%20set")

	got = prefix.EvaluatorURL("a/b", "1")
	assert.Contains(t, got, "a%2Fb", "a slash would otherwise change the route")
	assert.Equal(t, 1, strings.Count(got, "/build/evaluations/catalog/"))
}
