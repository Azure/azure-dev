// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_validateArtifact(t *testing.T) {
	tests := []struct {
		name      string
		artifact  *Artifact
		expectErr string
	}{
		{
			name: "valid artifact",
			artifact: &Artifact{
				Kind:         ArtifactKindDirectory,
				Location:     "/build",
				LocationKind: LocationKindLocal,
			},
		},
		{
			name: "empty kind",
			artifact: &Artifact{
				Kind:         "",
				Location:     "/build",
				LocationKind: LocationKindLocal,
			},
			expectErr: "kind is required",
		},
		{
			name: "whitespace-only kind",
			artifact: &Artifact{
				Kind:         ArtifactKind("  "),
				Location:     "/build",
				LocationKind: LocationKindLocal,
			},
			expectErr: "kind is required",
		},
		{
			name: "unknown kind",
			artifact: &Artifact{
				Kind:         ArtifactKind("foobar"),
				Location:     "/build",
				LocationKind: LocationKindLocal,
			},
			expectErr: "not a recognized artifact kind",
		},
		{
			name: "empty location",
			artifact: &Artifact{
				Kind:         ArtifactKindArchive,
				Location:     "",
				LocationKind: LocationKindLocal,
			},
			expectErr: "location is required",
		},
		{
			name: "whitespace-only location",
			artifact: &Artifact{
				Kind:         ArtifactKindArchive,
				Location:     "   ",
				LocationKind: LocationKindLocal,
			},
			expectErr: "location is required",
		},
		{
			name: "empty locationKind",
			artifact: &Artifact{
				Kind:         ArtifactKindArchive,
				Location:     "/build/out.zip",
				LocationKind: "",
			},
			expectErr: "locationKind is required",
		},
		{
			name: "invalid locationKind",
			artifact: &Artifact{
				Kind:         ArtifactKindArchive,
				Location:     "/build/out.zip",
				LocationKind: LocationKind("cloud"),
			},
			expectErr: "locationKind must be either",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateArtifact(tt.artifact)
			if tt.expectErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func Test_ArtifactCollection_Add_Validation(t *testing.T) {
	ac := ArtifactCollection{}

	// Add a valid artifact
	err := ac.Add(&Artifact{
		Kind:         ArtifactKindContainer,
		Location:     "registry.io/img:v1",
		LocationKind: LocationKindRemote,
	})
	require.NoError(t, err)
	require.Len(t, ac, 1)

	// Add an invalid artifact — collection should not grow
	err = ac.Add(&Artifact{
		Kind:         ArtifactKind("bad"),
		Location:     "somewhere",
		LocationKind: LocationKindLocal,
	})
	require.Error(t, err)
	require.Len(t, ac, 1)
}

func Test_ArtifactCollection_Add_Multiple(t *testing.T) {
	ac := ArtifactCollection{}

	err := ac.Add(
		&Artifact{
			Kind:         ArtifactKindDirectory,
			Location:     "/a",
			LocationKind: LocationKindLocal,
		},
		&Artifact{
			Kind:         ArtifactKindArchive,
			Location:     "/b.zip",
			LocationKind: LocationKindLocal,
		},
	)
	require.NoError(t, err)
	require.Len(t, ac, 2)
}

func Test_ArtifactCollection_Find_WithKindFilter(t *testing.T) {
	ac := ArtifactCollection{
		{
			Kind:         ArtifactKindDirectory,
			Location:     "/a",
			LocationKind: LocationKindLocal,
		},
		{
			Kind:         ArtifactKindContainer,
			Location:     "img:v1",
			LocationKind: LocationKindRemote,
		},
		{
			Kind:         ArtifactKindDirectory,
			Location:     "/b",
			LocationKind: LocationKindLocal,
		},
	}

	dirs := ac.Find(WithKind(ArtifactKindDirectory))
	require.Len(t, dirs, 2)

	containers := ac.Find(WithKind(ArtifactKindContainer))
	require.Len(t, containers, 1)
	require.Equal(t, "img:v1", containers[0].Location)

	archives := ac.Find(WithKind(ArtifactKindArchive))
	require.Empty(t, archives)
}

func Test_ArtifactCollection_Find_WithLocationKindFilter(
	t *testing.T,
) {
	ac := ArtifactCollection{
		{
			Kind:         ArtifactKindContainer,
			Location:     "local-img",
			LocationKind: LocationKindLocal,
		},
		{
			Kind:         ArtifactKindContainer,
			Location:     "registry.io/img",
			LocationKind: LocationKindRemote,
		},
	}

	local := ac.Find(WithLocationKind(LocationKindLocal))
	require.Len(t, local, 1)
	require.Equal(t, "local-img", local[0].Location)

	remote := ac.Find(WithLocationKind(LocationKindRemote))
	require.Len(t, remote, 1)
	require.Equal(t, "registry.io/img", remote[0].Location)
}

func Test_ArtifactCollection_Find_WithTake(t *testing.T) {
	ac := ArtifactCollection{
		{
			Kind:         ArtifactKindDirectory,
			Location:     "/a",
			LocationKind: LocationKindLocal,
		},
		{
			Kind:         ArtifactKindDirectory,
			Location:     "/b",
			LocationKind: LocationKindLocal,
		},
		{
			Kind:         ArtifactKindDirectory,
			Location:     "/c",
			LocationKind: LocationKindLocal,
		},
	}

	result := ac.Find(WithTake(2))
	require.Len(t, result, 2)
	require.Equal(t, "/a", result[0].Location)
	require.Equal(t, "/b", result[1].Location)

	// Take more than available
	result = ac.Find(WithTake(100))
	require.Len(t, result, 3)
}

func Test_ArtifactCollection_Find_CombinedFilters(
	t *testing.T,
) {
	ac := ArtifactCollection{
		{
			Kind:         ArtifactKindContainer,
			Location:     "local-img",
			LocationKind: LocationKindLocal,
		},
		{
			Kind:         ArtifactKindContainer,
			Location:     "reg/img1",
			LocationKind: LocationKindRemote,
		},
		{
			Kind:         ArtifactKindContainer,
			Location:     "reg/img2",
			LocationKind: LocationKindRemote,
		},
		{
			Kind:         ArtifactKindDirectory,
			Location:     "/build",
			LocationKind: LocationKindLocal,
		},
	}

	// Combine kind + locationKind + take
	result := ac.Find(
		WithKind(ArtifactKindContainer),
		WithLocationKind(LocationKindRemote),
		WithTake(1),
	)
	require.Len(t, result, 1)
	require.Equal(t, "reg/img1", result[0].Location)
}

func Test_ArtifactCollection_FindFirst(t *testing.T) {
	ac := ArtifactCollection{
		{
			Kind:         ArtifactKindDirectory,
			Location:     "/first",
			LocationKind: LocationKindLocal,
		},
		{
			Kind:         ArtifactKindDirectory,
			Location:     "/second",
			LocationKind: LocationKindLocal,
		},
	}

	first, ok := ac.FindFirst(
		WithKind(ArtifactKindDirectory),
	)
	require.True(t, ok)
	require.Equal(t, "/first", first.Location)

	// No match
	_, ok = ac.FindFirst(WithKind(ArtifactKindArchive))
	require.False(t, ok)
}

func Test_ArtifactCollection_FindLast(t *testing.T) {
	ac := ArtifactCollection{
		{
			Kind:         ArtifactKindDirectory,
			Location:     "/first",
			LocationKind: LocationKindLocal,
		},
		{
			Kind:         ArtifactKindDirectory,
			Location:     "/second",
			LocationKind: LocationKindLocal,
		},
		{
			Kind:         ArtifactKindContainer,
			Location:     "img:latest",
			LocationKind: LocationKindRemote,
		},
	}

	last, ok := ac.FindLast(
		WithKind(ArtifactKindDirectory),
	)
	require.True(t, ok)
	require.Equal(t, "/second", last.Location)

	// No match
	_, ok = ac.FindLast(WithKind(ArtifactKindArchive))
	require.False(t, ok)
}

func Test_ArtifactCollection_FindFirst_Empty(t *testing.T) {
	ac := ArtifactCollection{}
	_, ok := ac.FindFirst()
	require.False(t, ok)
}

func Test_ArtifactCollection_FindLast_Empty(t *testing.T) {
	ac := ArtifactCollection{}
	_, ok := ac.FindLast()
	require.False(t, ok)
}

func Test_ArtifactCollection_ToString_Empty(t *testing.T) {
	ac := ArtifactCollection{}
	result := ac.ToString("  ")
	require.Contains(t, result, "No artifacts were found")
}

func Test_ArtifactCollection_ToString_FiltersNonDisplayable(
	t *testing.T,
) {
	ac := ArtifactCollection{
		{
			Kind:         ArtifactKindDeployment,
			Location:     "https://deploy.url",
			LocationKind: LocationKindRemote,
		},
	}

	// ArtifactKindDeployment falls through to default which
	// returns "" — so collection output is empty string.
	result := ac.ToString("")
	require.Empty(t, result)
}

func Test_ArtifactCollection_MarshalJSON(t *testing.T) {
	ac := ArtifactCollection{
		{
			Kind:         ArtifactKindEndpoint,
			Location:     "https://api.example.com",
			LocationKind: LocationKindRemote,
			Metadata:     map[string]string{"label": "API"},
		},
	}

	data, err := ac.MarshalJSON()
	require.NoError(t, err)

	var unmarshaled []*Artifact
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)
	require.Len(t, unmarshaled, 1)
	require.Equal(t, ArtifactKindEndpoint, unmarshaled[0].Kind)
	require.Equal(
		t,
		"https://api.example.com",
		unmarshaled[0].Location,
	)
}

func Test_findFilter_matches(t *testing.T) {
	tests := []struct {
		name     string
		filter   findFilter
		artifact *Artifact
		expected bool
	}{
		{
			name:   "no filter matches everything",
			filter: findFilter{},
			artifact: &Artifact{
				Kind:         ArtifactKindDirectory,
				LocationKind: LocationKindLocal,
			},
			expected: true,
		},
		{
			name: "kind filter match",
			filter: findFilter{
				kind: new(ArtifactKindContainer),
			},
			artifact: &Artifact{
				Kind:         ArtifactKindContainer,
				LocationKind: LocationKindRemote,
			},
			expected: true,
		},
		{
			name: "kind filter mismatch",
			filter: findFilter{
				kind: new(ArtifactKindContainer),
			},
			artifact: &Artifact{
				Kind:         ArtifactKindDirectory,
				LocationKind: LocationKindLocal,
			},
			expected: false,
		},
		{
			name: "locationKind filter match",
			filter: findFilter{
				locationKind: new(LocationKindRemote),
			},
			artifact: &Artifact{
				Kind:         ArtifactKindContainer,
				LocationKind: LocationKindRemote,
			},
			expected: true,
		},
		{
			name: "locationKind filter mismatch",
			filter: findFilter{
				locationKind: new(LocationKindRemote),
			},
			artifact: &Artifact{
				Kind:         ArtifactKindContainer,
				LocationKind: LocationKindLocal,
			},
			expected: false,
		},
		{
			name: "both filters match",
			filter: findFilter{
				kind:         new(ArtifactKindArchive),
				locationKind: new(LocationKindLocal),
			},
			artifact: &Artifact{
				Kind:         ArtifactKindArchive,
				LocationKind: LocationKindLocal,
			},
			expected: true,
		},
		{
			name: "kind matches but locationKind does not",
			filter: findFilter{
				kind:         new(ArtifactKindArchive),
				locationKind: new(LocationKindRemote),
			},
			artifact: &Artifact{
				Kind:         ArtifactKindArchive,
				LocationKind: LocationKindLocal,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(
				t,
				tt.expected,
				tt.filter.matches(tt.artifact),
			)
		})
	}
}


