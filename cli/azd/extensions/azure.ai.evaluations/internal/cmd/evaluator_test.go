// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// The service needs a type discriminator to deserialize a definition. Without
// it the whole request is rejected with "The request field is required", which
// names the wrong field, so a hand-authored rubric failed to upload.
func TestNormalizeRubricBodyAddsDefinitionType(t *testing.T) {
	raw := []byte(`{"dimensions":[{"id":"accuracy","description":"Correct.","weight":5}]}`)

	body, err := normalizeRubricBody("support-quality", raw)
	require.NoError(t, err)

	var doc struct {
		Name       string `json:"name"`
		Definition struct {
			Type       string `json:"type"`
			Dimensions []struct {
				ID     string `json:"id"`
				Weight int    `json:"weight"`
			} `json:"dimensions"`
		} `json:"definition"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	require.Equal(t, "support-quality", doc.Name)
	require.Equal(t, "rubric", doc.Definition.Type)
	require.Len(t, doc.Definition.Dimensions, 1)
	require.Equal(t, 5, doc.Definition.Dimensions[0].Weight)
}

// A definition that already declares its type keeps it, so a generated rubric
// round-trips unchanged.
func TestNormalizeRubricBodyKeepsExistingType(t *testing.T) {
	raw := []byte(`{"type":"custom_kind","dimensions":[{"id":"a","weight":1}]}`)

	body, err := normalizeRubricBody("x", raw)
	require.NoError(t, err)

	var doc struct {
		Definition struct {
			Type string `json:"type"`
		} `json:"definition"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	require.Equal(t, "custom_kind", doc.Definition.Type)
}

// A full document is normalized the same way, and the name follows the flag.
func TestNormalizeRubricBodyHandlesFullDocument(t *testing.T) {
	raw := []byte(`{"name":"stale","definition":{"dimensions":[{"id":"a","weight":1}]}}`)

	body, err := normalizeRubricBody("actual-name", raw)
	require.NoError(t, err)

	var doc struct {
		Name       string `json:"name"`
		Definition struct {
			Type string `json:"type"`
		} `json:"definition"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	require.Equal(t, "actual-name", doc.Name)
	require.Equal(t, "rubric", doc.Definition.Type)
}

func TestNormalizeRubricBodyRejectsNonRubric(t *testing.T) {
	_, err := normalizeRubricBody("x", []byte(`{"something":1}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "dimensions")

	_, err = normalizeRubricBody("x", []byte(`not json`))
	require.Error(t, err)
}
