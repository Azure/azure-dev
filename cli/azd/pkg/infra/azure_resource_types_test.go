// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsTopLevelResourceType(t *testing.T) {
	var tests = []struct {
		resourceType string
		result       bool
	}{
		{"", false},
		{"/", false},
		{"/foo", false},
		{"foo", false},
		{"foo/", false},
		{"foo/b", true},
		{"foo/bar", true},
		{"foo/bar/baz", false},
		{"foo/bar/", false},
		{"Microsoft.Storage/storageAccounts", true},
		{"Microsoft.DocumentDB/databaseAccounts/collections", false},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("\"%s\")", test.resourceType), func(t *testing.T) {
			assert.Equal(t, test.result, IsTopLevelResourceType(AzureResourceType(test.resourceType)))
		})
	}
}
