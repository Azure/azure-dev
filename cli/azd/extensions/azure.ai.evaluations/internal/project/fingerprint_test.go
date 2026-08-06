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
		kind, name, want string
	}{
		{"dataset", "support-regression", "DATASET_SUPPORT_REGRESSION"},
		{"evaluator", "quality.v2", "EVALUATOR_QUALITY_V2"},
		{"dataset", "Mixed Case Name", "DATASET_MIXED_CASE_NAME"},
		// One rune maps to one underscore, so a multi-byte character does not
		// widen the key.
		{"eval", "unicode-caf\u00e9", "EVAL_UNICODE_CAF_"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			key := FingerprintKey(tt.kind, tt.name)

			assert.Equal(t, EnvKeyFingerprintPrefix+tt.want, key)
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
