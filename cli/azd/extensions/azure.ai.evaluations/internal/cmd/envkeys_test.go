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
func TestIDKey_NormalizesNames(t *testing.T) {
	assert.Equal(t, idKey("eval", "my group"), idKey("eval", "my-group"),
		"characters that cannot appear in an env name normalize the same way")
	assert.NotEqual(t, idKey("eval", "a"), idKey("dataset", "a"),
		"the kind keeps different resources apart")
}

// The id and version keys for the same declaration must not collide.
func TestIDKey_DoesNotCollideWithVersionKey(t *testing.T) {
	assert.NotEqual(t, idKey("dataset", "golden"), versionKey("dataset", "golden"))
}

// Setting EVAL_ID by hand is the documented way to point a config at a
// group that already exists. It is also the key the extension writes itself,
// which is what let a second group adopt the first one's id — so it stays
// readable only where it cannot be ambiguous. Fixing the aliasing dropped this
// fallback entirely once, silently breaking the documented behaviour.
func TestGroupIDKeys_SharedKeyReadOnlyWhenUnambiguous(t *testing.T) {
	sole := evalIDKeys("quality", true)
	assert.Equal(t, idKey("eval", "quality"), sole[0],
		"a group's own entry is preferred over the shared one")
	assert.Contains(t, sole, envKeyEvalID,
		"a single-group config honours an id set by hand")

	assert.Equal(t, []string{idKey("eval", "quality")}, evalIDKeys("quality", false),
		"with several groups the shared entry cannot say which group it means")
}
