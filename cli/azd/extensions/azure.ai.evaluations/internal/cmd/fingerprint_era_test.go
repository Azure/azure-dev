// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Recreating an eval is not free: it is a new eval, so every run taken against
// the old one stops being comparable with the ones taken after.
//
// The baseline decides that, and it can only decide it against a hash of the
// same shape. When the shape changed, an unchanged declaration hashed
// differently and every eval in the file was recreated by an upgrade nobody
// asked for. A baseline this build cannot read says nothing, and saying nothing
// is not saying "changed".
func TestABaselineFromDifferentHashingIsNotReadAsAnEdit(t *testing.T) {
	definition := fingerprintEra + "aaaa"
	digest := "bbbb"

	assert.False(t, substanceChanged("v1:cccc", definition, digest),
		"a baseline tagged by an older hashing cannot be compared with this one")
	assert.False(t, substanceChanged("v9:cccc", definition, digest),
		"nor one tagged by a newer build a downgrade left behind")
}

// An untagged baseline predates the tag and was written by this same hashing,
// so it is still compared by value. Treating it as unreadable would stop every
// existing environment noticing a real edit, which is the opposite defect.
func TestAnUntaggedBaselineIsStillCompared(t *testing.T) {
	definition := fingerprintEra + "aaaa"

	assert.False(t, substanceChanged("aaaa", definition, "bbbb"),
		"the same definition, written before the tag existed")
	assert.False(t, substanceChanged("bbbb", definition, "bbbb"),
		"or the full digest, which is what the build before that recorded")
	assert.True(t, substanceChanged("dddd", definition, "bbbb"),
		"and something that matches neither is an edit")
}

// Nothing recorded is a first deploy, not a change.
func TestNoBaselineIsNotAnEdit(t *testing.T) {
	assert.False(t, substanceChanged("", fingerprintEra+"aaaa", "bbbb"))
}

// The tag is read off the value, and a hash is hex so it carries no colon.
func TestFingerprintEraReadsOnlyAVersionTag(t *testing.T) {
	assert.Equal(t, "v2:", fingerprintEraOf("v2:abcdef"))
	assert.Equal(t, "v10:", fingerprintEraOf("v10:abcdef"))
	assert.Empty(t, fingerprintEraOf("abcdef"), "an untagged hash has no era")
	assert.Empty(t, fingerprintEraOf("sha256:abcdef"),
		"and something that is not a version tag is not one either")
}
