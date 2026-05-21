// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSelectDownloadMode(t *testing.T) {
	tests := []struct {
		name        string
		outputName  string
		all         bool
		wantNamed   bool
		wantAll     bool
		wantDefault bool
	}{
		{
			name:        "no flags -> default only",
			wantDefault: true,
		},
		{
			name:        "output-name=default treated as no flag -> default only",
			outputName:  "default",
			wantDefault: true,
		},
		{
			name:        "empty output-name -> default only",
			outputName:  "",
			wantDefault: true,
		},
		{
			name:       "named output -> named only",
			outputName: "my-output",
			wantNamed:  true,
		},
		{
			name:    "all -> every named + default",
			all:     true,
			wantAll: true,
		},
		// NB: cobra's MarkFlagsMutuallyExclusive prevents (all && outputName) at
		// the CLI layer, so we don't test that combination here.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			named, all, def := selectDownloadMode(tt.outputName, tt.all)
			assert.Equal(t, tt.wantNamed, named, "wantNamed")
			assert.Equal(t, tt.wantAll, all, "wantAll")
			assert.Equal(t, tt.wantDefault, def, "wantDefault")
		})
	}
}
