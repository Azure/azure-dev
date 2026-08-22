// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// `init` printed "Using data source: traces (Application Insights)" whether or
// not the project had one, so a scaffold that could not produce a sample was
// reported with a green check. init makes no service calls, so it cannot
// verify a connection it never saw.
func TestTraceSourceOnlyClaimsAConnectionItFound(t *testing.T) {
	connected := UsingTraceSource(true)
	assert.Contains(t, connected, "Application Insights")

	unverified := UsingTraceSource(false)
	assert.Contains(t, unverified, "traces",
		"the source is still what was chosen")
	assert.NotContains(t, unverified, "(Application Insights)",
		"naming the connection asserts something nobody checked")
	assert.Truef(t, strings.Contains(unverified, "only if"),
		"the reader has to learn the run may find no rows: %s", unverified)
}
