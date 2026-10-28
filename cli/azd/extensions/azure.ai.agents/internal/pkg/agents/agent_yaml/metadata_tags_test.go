// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateAgentAPIRequestMetadataTags(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags any
		want string
	}{
		{name: "string", tags: "existing-value", want: "existing-value"},
		{name: "empty string", tags: "", want: ""},
		{name: "decoded list", tags: []any{"one", "two"}, want: `["one","two"]`},
		{name: "typed list", tags: []string{"one", "two"}, want: `["one","two"]`},
		{name: "empty list", tags: []any{}, want: `[]`},
		{name: "typed empty list", tags: []string{}, want: `[]`},
		{name: "commas and empty items", tags: []any{"one,two", "", "three"}, want: `["one,two","","three"]`},
		{name: "separate items", tags: []any{"one", "two", "", "three"}, want: `["one","two","","three"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := map[string]any{"tags": tc.tags, "owner": "team"}
			request, err := createAgentAPIRequest(AgentDefinition{
				Kind: AgentKindHosted, Name: "agent", Metadata: &metadata,
			}, "definition", nil, nil)
			require.NoError(t, err)
			require.Equal(t, map[string]string{"tags": tc.want, "owner": "team"}, request.Metadata)
			data, err := json.Marshal(request)
			require.NoError(t, err)
			var wire struct {
				Metadata map[string]string `json:"metadata"`
			}
			require.NoError(t, json.Unmarshal(data, &wire))
			require.Contains(t, wire.Metadata, "tags")
			require.Equal(t, tc.want, wire.Metadata["tags"])
			require.Equal(t, tc.tags, metadata["tags"], "mapping must not mutate authored tags")
		})
	}
}

func TestCreateAgentAPIRequestRejectsInvalidTagsWithoutValues(t *testing.T) {
	for _, tags := range []any{nil, false, 1, map[string]any{"private-key": "private-value"},
		[]any{"private-value", false}, []any{nil}, []any{map[string]any{"private-key": "private-value"}}} {
		metadata := map[string]any{"tags": tags}
		request, err := createAgentAPIRequest(AgentDefinition{Metadata: &metadata}, nil, nil, nil)
		require.Nil(t, request)
		require.EqualError(t, err, "metadata.tags must be a string or a list of strings")
		require.NotContains(t, err.Error(), "private-")
	}
}
