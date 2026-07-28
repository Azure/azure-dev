// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Ids are per declaration. A shared key works only while a config has one
// group: with two, the second deploy finds the first's id cached, confirms it
// exists, and hands it back for the wrong group — so group A silently scores
// group B's criteria.
func TestIDKey_IsPerName(t *testing.T) {
	a := idKey("evalgroup", "quality-a")
	b := idKey("evalgroup", "quality-b")

	assert.NotEqual(t, a, b, "two groups must not share an id key")
	assert.Contains(t, a, "QUALITY_A")
	assert.True(t, len(a) > 3 && a[len(a)-3:] == "_ID")
}

// Names that are not valid env identifiers still have to produce distinct,
// stable keys.
func TestIDKey_NormalizesNames(t *testing.T) {
	assert.Equal(t, idKey("evalgroup", "my group"), idKey("evalgroup", "my-group"),
		"characters that cannot appear in an env name normalize the same way")
	assert.NotEqual(t, idKey("evalgroup", "a"), idKey("dataset", "a"),
		"the kind keeps different resources apart")
}

// The id and version keys for the same declaration must not collide.
func TestIDKey_DoesNotCollideWithVersionKey(t *testing.T) {
	assert.NotEqual(t, idKey("dataset", "golden"), versionKey("dataset", "golden"))
}
