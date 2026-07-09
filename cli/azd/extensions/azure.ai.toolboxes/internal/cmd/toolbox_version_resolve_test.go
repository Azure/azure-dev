// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"testing"

	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionSortDescending(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int // sign only: <0, 0, >0
	}{
		{"numeric a greater", "3", "1", -1},
		{"numeric b greater", "1", "3", 1},
		{"numeric equal", "2", "2", 0},
		{"double digit beats single digit", "10", "9", -1},
		{"lexical fallback a greater", "v2", "v1", -1},
		{"lexical fallback b greater", "v1", "v2", 1},
		{"mixed numeric/non-numeric falls back to lexical", "1", "abc", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := versionSortDescending(tc.a, tc.b)
			switch {
			case tc.want < 0:
				assert.Negative(t, got)
			case tc.want > 0:
				assert.Positive(t, got)
			default:
				assert.Zero(t, got)
			}
		})
	}
}

func TestLatestToolboxVersion(t *testing.T) {
	t.Run("empty_list_falls_back", func(t *testing.T) {
		client := newMockToolboxClient("https://e/")
		got, err := latestToolboxVersion(t.Context(), client, "tb", "7")
		require.NoError(t, err)
		assert.Equal(t, "7", got, "no versions returned: fall back to the caller-supplied default")
	})

	t.Run("returns_numeric_max_regardless_of_input_order", func(t *testing.T) {
		client := newMockToolboxClient("https://e/")
		client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
			{Name: "tb", Version: "1"},
			{Name: "tb", Version: "10"},
			{Name: "tb", Version: "2"},
		}
		got, err := latestToolboxVersion(t.Context(), client, "tb", "1")
		require.NoError(t, err)
		assert.Equal(t, "10", got)
	})

	t.Run("propagates_list_error", func(t *testing.T) {
		client := newMockToolboxClient("https://e/")
		client.listVersionsErr = errors.New("boom")
		_, err := latestToolboxVersion(t.Context(), client, "tb", "1")
		require.Error(t, err)
	})
}

func TestResolveBaseVersion(t *testing.T) {
	tb := &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}

	newClientWithLatest := func(latest string) *mockToolboxClient {
		client := newMockToolboxClient("https://e/")
		client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
			{Name: "tb", Version: "1"},
			{Name: "tb", Version: latest},
		}
		return client
	}

	cases := []struct {
		name        string
		fromVersion string
		want        string
	}{
		{"empty_defaults_to_latest", "", "3"},
		{"latest_keyword", "latest", "3"},
		{"latest_keyword_case_and_whitespace_insensitive", "  LATEST  ", "3"},
		{"default_keyword_uses_toolbox_default", "default", "1"},
		{"default_keyword_case_insensitive", "DEFAULT", "1"},
		{"explicit_version_passed_verbatim", "2", "2"},
		{"explicit_version_with_whitespace_trimmed", "  2  ", "2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newClientWithLatest("3")
			got, err := resolveBaseVersion(t.Context(), client, "tb", tb, tc.fromVersion)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
