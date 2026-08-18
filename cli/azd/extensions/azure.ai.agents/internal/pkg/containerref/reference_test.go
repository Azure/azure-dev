// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package containerref

// cSpell:ignore containerref

import "testing"

func TestIsFullyQualified(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		image string
		want  bool
	}{
		{name: "registry and tag", image: "registry.example.com/team/agent:v1", want: true},
		{name: "localhost and port", image: "localhost:5000/agent:latest", want: true},
		{
			name:  "digest",
			image: "registry.example.com/agent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			want:  true,
		},
		{name: "unqualified", image: "agent:v1", want: false},
		{name: "URL scheme", image: "https://registry.example.com/agent:v1", want: false},
		{name: "empty", image: "", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := IsFullyQualified(test.image); got != test.want {
				t.Errorf("IsFullyQualified(%q) = %t, want %t", test.image, got, test.want)
			}
		})
	}
}
