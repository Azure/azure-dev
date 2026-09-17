// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FingerprintGroup is covered in service_target_eval_test.go. These cover the
// file hash and the environment key it is stored under, which nothing did.

// A fingerprint is compared against the one recorded at the last deploy, so
// identical content must hash identically and a single changed byte must not.
func TestFingerprint(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jsonl")
	b := filepath.Join(dir, "b.jsonl")
	require.NoError(t, os.WriteFile(a, []byte(`{"query":"hi"}`), 0o600))
	require.NoError(t, os.WriteFile(b, []byte(`{"query":"hi"}`), 0o600))

	sumA, err := Fingerprint(a)
	require.NoError(t, err)
	sumB, err := Fingerprint(b)
	require.NoError(t, err)

	assert.Equal(t, sumA, sumB, "same content, same fingerprint")
	assert.Len(t, sumA, 64, "sha-256 as hex")

	require.NoError(t, os.WriteFile(b, []byte(`{"query":"hI"}`), 0o600))
	sumB, err = Fingerprint(b)
	require.NoError(t, err)
	assert.NotEqual(t, sumA, sumB, "one changed byte has to show")
}

// A missing file names itself, because the usual cause is a catalog entry
// pointing at something that was moved or never generated.
func TestFingerprint_MissingFileNamesIt(t *testing.T) {
	_, err := Fingerprint(filepath.Join(t.TempDir(), "gone.jsonl"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "gone.jsonl")
}

// The key goes into an azd environment file, which accepts only uppercase
// letters, digits and underscores. A name that reached it unmapped would
// produce a key azd cannot round-trip, and the artifact would look changed on
// every deploy.
func TestFingerprintKey_IsAValidEnvironmentKey(t *testing.T) {
	tests := []struct {
		kind, name, readable string
	}{
		{"dataset", "support-regression", "DATASET_SUPPORT_REGRESSION"},
		{"evaluator", "quality.v2", "EVALUATOR_QUALITY_V2"},
		{"dataset", "Mixed Case Name", "DATASET_MIXED_CASE_NAME"},
		// One rune maps to one underscore, so a multi-byte character does not
		// widen the key.
		{"eval", "unicode-caf\u00e9", "EVAL_UNICODE_CAF_"},
	}

	for _, tt := range tests {
		t.Run(tt.readable, func(t *testing.T) {
			key := FingerprintKey(tt.kind, tt.name)

			assert.True(t,
				strings.HasPrefix(key, EnvKeyFingerprintPrefix+tt.readable+"_"),
				"the key stays readable: %q", key)
			for _, r := range strings.TrimPrefix(key, EnvKeyFingerprintPrefix) {
				assert.Truef(t,
					(r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_',
					"%q is not allowed in an environment key", r)
			}
		})
	}
}

// Two artifacts of different kinds can share a name, and they must not share a
// key — one would overwrite the other's recorded fingerprint.
func TestFingerprintKey_KindSeparatesTheNamespaces(t *testing.T) {
	assert.NotEqual(t,
		FingerprintKey("dataset", "quality"),
		FingerprintKey("evaluator", "quality"))
}

// The readable half of the key maps every character outside [A-Z0-9] to an
// underscore, so these names are indistinguishable in it. Sharing a key means
// sharing a recorded fingerprint, version and id: both artifacts then look
// changed on every deploy and republish forever.
func TestFingerprintKey_NamesThatSanitizeAlikeStillDiffer(t *testing.T) {
	collidingNames := []string{
		"quality-a",
		"quality_a",
		"quality a",
		"quality.a",
		"quality/a",
		"quality\u00e9a",
		"qualityXa",
	}

	seen := make(map[string]string, len(collidingNames))
	for _, name := range collidingNames {
		key := FingerprintKey("evaluator", name)
		if previous, clash := seen[key]; clash {
			t.Fatalf("%q and %q share the key %q", previous, name, key)
		}
		seen[key] = name
	}
}

// The digest covers the kind and the name separately, so moving a character
// across the boundary is not the same artifact.
func TestFingerprintKey_TheKindBoundaryIsNotAmbiguous(t *testing.T) {
	assert.NotEqual(t,
		FingerprintKey("dataset_a", "b"),
		FingerprintKey("dataset", "a_b"))
}
