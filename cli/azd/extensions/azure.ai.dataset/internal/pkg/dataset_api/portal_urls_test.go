// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProjectID = "/subscriptions/8ecadfc9-d1a3-4ea4-b844-0d9f87e4d7c8/resourceGroups/rg/" +
	"providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"

// The download and show views end with a link to the thing they just handled,
// and it has to be the page that exists.
func TestDatasetPortalURL(t *testing.T) {
	prefix, err := NewPortalPrefix(testProjectID)
	require.NoError(t, err)

	got := prefix.DatasetURL("support-tests", "3.0")
	assert.Contains(t, got, "https://ai.azure.com/nextgen/r/")
	assert.Contains(t, got, ",rg,,acct,proj")
	assert.Contains(t, got, "/build/data/datasets/support-tests/3.0")
}

// A name with a slash or a space in it would otherwise break the route.
func TestDatasetPortalURLEscapesItsSegments(t *testing.T) {
	prefix, err := NewPortalPrefix(testProjectID)
	require.NoError(t, err)

	got := prefix.DatasetURL("my data", "1.0")
	assert.Contains(t, got, "my%20data")
	assert.NotContains(t, got, "my data")
}

// Anything that is not a Foundry project builds a plausible URL onto a page
// that does not exist, so it is refused instead.
func TestAPortalPrefixRefusesWhatIsNotAProject(t *testing.T) {
	for _, id := range []string{
		"",
		"not-a-resource-id",
		"/subscriptions/8ecadfc9-d1a3-4ea4-b844-0d9f87e4d7c8/resourceGroups/rg/providers/" +
			"Microsoft.Storage/storageAccounts/acct/blobServices/default",
		"/subscriptions/8ecadfc9-d1a3-4ea4-b844-0d9f87e4d7c8/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/acct",
	} {
		_, err := NewPortalPrefix(id)
		assert.Errorf(t, err, "%q is not a project and must not produce a link", id)
	}
}

// The subscription is base64 in the portal route, not the GUID as written.
func TestAPortalPrefixRefusesASubscriptionThatIsNotAGUID(t *testing.T) {
	_, err := NewPortalPrefix(
		"/subscriptions/not-a-guid/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/acct/projects/proj")
	require.Error(t, err)
}
