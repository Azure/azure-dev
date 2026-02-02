// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
"context"
"testing"

"github.com/azure/azure-dev/cli/azd/pkg/environment"
"github.com/azure/azure-dev/cli/azd/test/mocks"
"github.com/stretchr/testify/require"
)

func TestServiceCondition_Integration(t *testing.T) {
const testProj = `
name: test-proj
services:
  always-enabled:
    project: src/api
    language: js
    host: containerapp
  conditional-enabled:
    project: src/web
    language: js
    host: appservice
    condition: ${DEPLOY_WEB}
  conditional-disabled:
    project: src/worker
    language: python
    host: containerapp
    condition: ${DEPLOY_WORKER}
`

mockContext := mocks.NewMockContext(context.Background())
projectConfig, err := Parse(*mockContext.Context, testProj)
require.Nil(t, err)
require.NotNil(t, projectConfig)
require.Len(t, projectConfig.Services, 3)

// Create environment with condition variables
env := environment.NewWithValues("test-env", map[string]string{
"DEPLOY_WEB":    "true",
"DEPLOY_WORKER": "false",
})

// Test that conditions are evaluated correctly
alwaysEnabled := projectConfig.Services["always-enabled"]
require.True(t, alwaysEnabled.IsEnabled(env.Getenv), "Service without condition should be enabled")

conditionalEnabled := projectConfig.Services["conditional-enabled"]
require.True(t, conditionalEnabled.IsEnabled(env.Getenv), "Service with condition=true should be enabled")

conditionalDisabled := projectConfig.Services["conditional-disabled"]
require.False(t, conditionalDisabled.IsEnabled(env.Getenv), "Service with condition=false should be disabled")
}

func TestServiceCondition_WithDifferentValues(t *testing.T) {
tests := []struct {
name        string
envValue    string
shouldEnable bool
}{
{"true", "true", true},
{"TRUE", "TRUE", true},
{"True", "True", true},
{"1", "1", true},
{"yes", "yes", true},
{"YES", "YES", true},
{"Yes", "Yes", true},
{"false", "false", false},
{"0", "0", false},
{"no", "no", false},
{"random", "random", false},
{"empty", "", false},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
const testProj = `
name: test-proj
services:
  test-service:
    project: src/api
    language: js
    host: containerapp
    condition: ${DEPLOY_SERVICE}
`

mockContext := mocks.NewMockContext(context.Background())
projectConfig, err := Parse(*mockContext.Context, testProj)
require.Nil(t, err)

env := environment.NewWithValues("test-env", map[string]string{
"DEPLOY_SERVICE": tt.envValue,
})

service := projectConfig.Services["test-service"]
enabled := service.IsEnabled(env.Getenv)
require.Equal(t, tt.shouldEnable, enabled, "Condition value %s should result in enabled=%v", tt.envValue, tt.shouldEnable)
})
}
}
