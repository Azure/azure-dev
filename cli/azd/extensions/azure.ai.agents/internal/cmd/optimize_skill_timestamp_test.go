// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skillTimestampConfig(format, start, end string) string {
	if format == "json" {
		body := strings.Replace(skillOptimizeTestConfig, `"start_time":1792454400`, `"start_time":`+start, 1)
		return strings.Replace(body, `"end_time":1793059200`, `"end_time":`+end, 1)
	}
	return fmt.Sprintf(`optimization_model_configuration: {model: optimizer}
target_configuration: {type: foundry_agent, name: demo-agent, version: "4"}
optimization_configuration:
  type: skill_optimization
  context:
    - type: agent_traces
      trace_window:
        type: fixed
        start_time: %s
        end_time: %s
`, start, end)
}

func TestSkillOptimizeTraceTimestampConfigRoundTrips(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"json", "yaml"} {
		for _, tt := range []struct {
			name  string
			start string
			end   string
		}{
			{"unix", "1792454400", "1793059200"},
			{"iso", `"2026-10-20T00:00:00Z"`, `"2026-10-27T00:00:00Z"`},
			{"mixed", "1792454400", `"2026-10-27T00:00:00Z"`},
			{"fractional iso", `"2026-10-20T01:00:00.1234567+01:00"`, `"2026-10-27T00:00:00.7654321Z"`},
			{"epoch zero", "0", "1"},
		} {
			t.Run(format+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				file := writeTestFile(t, t.TempDir(), "skill."+format, skillTimestampConfig(format, tt.start, tt.end))
				cfg, err := LoadOptimizeConfig(file)
				require.NoError(t, err)
				request, _, err := cfg.ToRequest()
				require.NoError(t, err)
				window := request.OptimizationConfiguration.Context[0].TraceWindow
				assert.JSONEq(t, tt.start, string(window.StartTime))
				assert.JSONEq(t, tt.end, string(window.EndTime))
				encoded, err := json.Marshal(request)
				require.NoError(t, err)
				var decoded optimize_api.OptimizeRequest
				require.NoError(t, json.Unmarshal(encoded, &decoded))
				assert.Equal(t, *window, *decoded.OptimizationConfiguration.Context[0].TraceWindow)
			})
		}
	}
}

func TestSkillOptimizeRejectsInvalidTimestampConfigs(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"json", "yaml"} {
		for _, field := range []string{"start_time", "end_time"} {
			for _, value := range []string{
				`1792454400.5`, `1792454400.0`, `1e3`, `"1792454400"`, `1792454400000`,
				`9223372036854775808`, `253402300800`, `-62135596801`,
				`null`, `true`, `[]`, `{}`, `""`, `"2026-10-20T00:00:00"`,
			} {
				t.Run(format+"/"+field+"/"+value, func(t *testing.T) {
					t.Parallel()
					start, end := "1792454400", "1793059200"
					if field == "start_time" {
						start = value
					} else {
						end = value
					}
					body := skillTimestampConfig(format, start, end)
					file := writeTestFile(t, t.TempDir(), "invalid."+format, body)
					_, err := LoadOptimizeConfig(file)
					require.Error(t, err)
					assert.Contains(t, err.Error(), "timestamp")
				})
			}
		}
	}
}

func TestSkillOptimizeUnixWindowValidation(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		start string
		end   string
	}{
		{"missing start", "", "1793059200"},
		{"missing end", "1792454400", ""},
		{"before epoch", "-1", "1"},
		{"equal bounds", "1792454400", "1792454400"},
		{"inverted", "1793059200", "1792454400"},
		{"future end", "1792454400", "253402300799"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := skillOptimizeRequest(t)
			window := request.OptimizationConfiguration.Context[0].TraceWindow
			window.StartTime = optimize_api.SkillTraceTimestamp(tt.start)
			window.EndTime = optimize_api.SkillTraceTimestamp(tt.end)
			require.ErrorContains(t, validateSkillOptimizeRequest(&request), "fixed trace_window")
		})
	}
	request := skillOptimizeRequest(t)
	request.OptimizationConfiguration.Context[0].TraceWindow = &optimize_api.SkillTraceWindow{
		Type: "trace_ids", TraceIDs: []string{"trace-a"}, LookbackHours: new(1),
		StartTime: optimize_api.SkillTraceTimestamp("0"),
	}
	require.ErrorContains(t, validateSkillOptimizeRequest(&request), "cannot contain start_time")
}

func TestSkillOptimizeTimestampErrorsPrecedeAuthentication(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	for _, value := range []string{`1792454400.0`, `"1792454400"`, `1792454400000`, `null`} {
		t.Run(value, func(t *testing.T) {
			file := writeTestFile(t, t.TempDir(), "invalid.yaml", skillTimestampConfig("yaml", value, "1793059200"))
			cmd := newOptimizeCommand(&azdext.ExtensionContext{NoPrompt: true})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs([]string{"--config", file})
			err := cmd.Execute()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "timestamp")
			assert.NotContains(t, err.Error(), "credential")
			assert.NotContains(t, output.String(), "Job ID")
		})
	}
}

func TestSkillOptimizeUnixExampleLoads(t *testing.T) {
	t.Parallel()
	example := filepath.Join("..", "..", "examples", "skill-optimization.yaml")
	cfg, err := LoadOptimizeConfig(example)
	require.NoError(t, err)
	window := cfg.Request.OptimizationConfiguration.Context[0].TraceWindow
	assert.Equal(t, "1792454400", string(window.StartTime))
	assert.Equal(t, "1793059200", string(window.EndTime))
	before, err := os.ReadFile(example)
	require.NoError(t, err)
	_, _, err = cfg.ToRequest()
	require.NoError(t, err)
	after, err := os.ReadFile(example)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}
