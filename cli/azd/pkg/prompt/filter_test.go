// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/stretchr/testify/require"
)

func TestNormalizePromptLocationName(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"EastUS", "eastus"},
		{"  East US 2  ", "east us 2"},
		{"", ""},
		{"   ", ""},
		{"WestEurope", "westeurope"},
	}
	for _, c := range cases {
		require.Equal(t, c.out, normalizePromptLocationName(c.in))
	}
}

func TestFilterLocationOptions(t *testing.T) {
	locations := []account.Location{
		{Name: "eastus", DisplayName: "East US"},
		{Name: "westus", DisplayName: "West US"},
		{Name: "westeurope", DisplayName: "West Europe"},
	}

	t.Run("empty allowed returns all", func(t *testing.T) {
		got := filterLocationOptions(locations, nil)
		require.Equal(t, locations, got)
	})

	t.Run("allowlist filters and is case-insensitive", func(t *testing.T) {
		got := filterLocationOptions(locations, []string{"EastUS", "  WESTUS "})
		require.Len(t, got, 2)
		require.Equal(t, "eastus", got[0].Name)
		require.Equal(t, "westus", got[1].Name)
	})

	t.Run("all-whitespace allowlist treated as no filter", func(t *testing.T) {
		got := filterLocationOptions(locations, []string{"   ", ""})
		require.Equal(t, locations, got)
	})

	t.Run("no matches returns empty slice", func(t *testing.T) {
		got := filterLocationOptions(locations, []string{"southindia"})
		require.Empty(t, got)
	})

	t.Run("does not mutate input", func(t *testing.T) {
		original := append([]account.Location(nil), locations...)
		_ = filterLocationOptions(locations, []string{"eastus"})
		require.Equal(t, original, locations)
	})
}
