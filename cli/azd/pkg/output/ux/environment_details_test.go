// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/snapshot"
)

func TestEnvironmentDetails(t *testing.T) {
	pp := &EnvironmentDetails{
		Subscription: "Foo (bar)",
		Location:     "Somewhere cool",
	}

	output := pp.ToString("   ")
	snapshot.SnapshotT(t, output)
}
