// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDatasetVersionWireFormat locks the JSON field names we send to (and
// expect from) the Foundry dataset API. The Foundry data plane uses "type"
// for the dataset kind (e.g. "uri_folder"); flipping this to "dataType"
// silently breaks the PATCH because the server will store an unknown field
// and default the type. Confirmed by inspecting a real GET response from
// /api/projects/{p}/datasets/{name}/versions/{v}.
func TestDatasetVersionWireFormat(t *testing.T) {
	t.Run("marshal uses expected field names", func(t *testing.T) {
		v := DatasetVersion{
			ID:          "/subscriptions/sub/.../versions/v1",
			Name:        "code-foo",
			Version:     "v1",
			DataURI:     "https://acct.blob.core.windows.net/c/v1",
			DataType:    "uri_folder",
			Description: "desc",
			Tags:        map[string]string{"contentHash": "abc"},
		}

		raw, err := json.Marshal(v)
		require.NoError(t, err)

		// Decode into a generic map so we assert on actual wire field names,
		// not Go struct field names.
		var wire map[string]any
		require.NoError(t, json.Unmarshal(raw, &wire))

		require.Equal(t, "/subscriptions/sub/.../versions/v1", wire["id"])
		require.Equal(t, "code-foo", wire["name"])
		require.Equal(t, "v1", wire["version"])
		require.Equal(t, "https://acct.blob.core.windows.net/c/v1", wire["dataUri"])
		require.Equal(t, "uri_folder", wire["type"], "field must marshal as \"type\" (server-side name); see GET response from Foundry data plane")
		require.NotContains(t, wire, "dataType", "server uses \"type\", not \"dataType\"; do not flip this tag")
		require.Equal(t, "desc", wire["description"])
	})

	t.Run("unmarshal reads server response shape", func(t *testing.T) {
		// Real-shape response captured from the Foundry data plane:
		//   GET /api/projects/{p}/datasets/{name}/versions/{v}?api-version=v1
		body := `{
			"id": "/subscriptions/sub/.../versions/v1",
			"name": "code-foo",
			"version": "v1",
			"dataUri": "https://acct.blob.core.windows.net/c/v1",
			"type": "uri_folder",
			"description": "desc",
			"tags": {"contentHash": "abc"}
		}`

		var v DatasetVersion
		require.NoError(t, json.Unmarshal([]byte(body), &v))

		require.Equal(t, "uri_folder", v.DataType,
			"DataType must unmarshal from server's \"type\" field")
		require.Equal(t, "code-foo", v.Name)
		require.Equal(t, "v1", v.Version)
		require.Equal(t, "abc", v.Tags["contentHash"])
	})

	t.Run("omitempty drops zero fields", func(t *testing.T) {
		// A PATCH body for the dedup path can be just dataUri + type + tags.
		v := DatasetVersion{
			DataURI:  "https://acct.blob.core.windows.net/c/v1",
			DataType: "uri_folder",
		}
		raw, err := json.Marshal(v)
		require.NoError(t, err)

		var wire map[string]any
		require.NoError(t, json.Unmarshal(raw, &wire))
		require.NotContains(t, wire, "id")
		require.NotContains(t, wire, "name")
		require.NotContains(t, wire, "version")
		require.NotContains(t, wire, "description")
		require.NotContains(t, wire, "tags")
	})
}
