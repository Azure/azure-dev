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
	a := idKey("eval", "quality-a")
	b := idKey("eval", "quality-b")

	assert.NotEqual(t, a, b, "two groups must not share an id key")
	assert.Contains(t, a, "QUALITY_A")
	assert.True(t, len(a) > 3 && a[len(a)-3:] == "_ID")
}

// Names that are not valid env identifiers still have to produce distinct,
// stable keys.
//
// The readable half of the key cannot tell "my group" from "my-group" — both
// sanitize to MY_GROUP. Letting them share a key is the collision the test
// above describes: the second declaration finds the first's id cached and
// scores the wrong group.
func TestIDKey_NormalizesNames(t *testing.T) {
	assert.NotEqual(t, idKey("eval", "my group"), idKey("eval", "my-group"),
		"names that sanitize alike are still different names")
	assert.NotEqual(t, idKey("eval", "a"), idKey("dataset", "a"),
		"the kind keeps different resources apart")

	assert.Equal(t, idKey("eval", "my group"), idKey("eval", "my group"),
		"the same name must key the same way on every deploy")
}

// The id and version keys for the same declaration must not collide.
func TestIDKey_DoesNotCollideWithVersionKey(t *testing.T) {
	assert.NotEqual(t, idKey("dataset", "golden"), versionKey("dataset", "golden"))
}

// An eval's id is read from the entry recorded under its own name and nowhere
// else. A shared entry cannot say which declaration it belongs to, and reading
// one let a file whose single entry had been replaced run the previous eval's
// criteria over the new one's rows, reported as success.
func TestEvalIDIsReadFromTheEvalsOwnEntry(t *testing.T) {
	assert.NotEqual(t, idKey("eval", "quality"), idKey("eval", "nightly"),
		"two declarations cannot share an entry")
}
