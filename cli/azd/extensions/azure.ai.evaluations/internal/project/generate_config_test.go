// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func generateCfgWithStrategy(strategy string) *GenerateConfig {
	cfg := &GenerateConfig{}
	cfg.Agent.Name = "my-agent"
	cfg.Generate.Rubric = &RubricSpec{Name: "r"}
	cfg.Generate.Dataset = &DatasetSpec{Name: "d", Strategy: strategy}
	return cfg
}

// from-traces used to pass validation and then generate synthetic rows anyway,
// handing back data that looked nothing like what was asked for. Rejecting it
// is better than answering the wrong question.
func TestValidateRejectsUnsupportedDatasetStrategy(t *testing.T) {
	err := generateCfgWithStrategy(StrategyFromTraces).Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported yet")
	require.Contains(t, err.Error(), "agent.context.traces.window",
		"the error should point at the way traces are actually used")
}

func TestValidateAcceptsSupportedDatasetStrategies(t *testing.T) {
	require.NoError(t, generateCfgWithStrategy("").Validate())
	require.NoError(t, generateCfgWithStrategy(StrategySynthetic).Validate())
}

func TestValidateRejectsUnknownDatasetStrategy(t *testing.T) {
	err := generateCfgWithStrategy("made-up").Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid")
}
