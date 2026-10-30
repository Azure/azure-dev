// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMonitoringConnectionName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "azd-appinsights-b35f1607b6f65", MonitoringConnectionName("project"))
	assert.Equal(t, "azd-appinsights-6bf3686a1d175", MonitoringConnectionName("declmon-tf-new-20261030a"))
	assert.Equal(t, MonitoringConnectionName("PROJECT"), MonitoringConnectionName("project"))
	long := strings.Repeat("long-project-name-", 8)
	assert.LessOrEqual(t, len(MonitoringConnectionName(long)), 32)
	assert.NotEqual(t, MonitoringConnectionName(long+"a"), MonitoringConnectionName(long+"b"))
	assert.Regexp(t, `^[a-zA-Z0-9][a-zA-Z0-9_-]{2,32}$`, MonitoringConnectionName(long))
}
