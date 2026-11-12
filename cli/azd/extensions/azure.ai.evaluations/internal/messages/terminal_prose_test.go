// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalProseEscapesEveryControlInHandoffValues(t *testing.T) {
	cases := []struct {
		name   string
		render func(string) error
	}{
		{"gate run", func(v string) error { return GateOutlivedTheWait(v, time.Second) }},
		{"interrupted run", func(v string) error { return WaitInterrupted(v, errors.New("interrupted")) }},
		{"required eval", RunMustBeNamed},
		{"artifact with job", func(v string) error { return ArtifactAppearedDuringGeneration(v, "job-1") }},
		{"artifact without job", func(v string) error { return ArtifactAppearedDuringGeneration(v, "") }},
		{"dataset path", func(v string) error { return DatasetNotGeneratedYet("data", v) }},
		{"evaluator path", func(v string) error { return EvaluatorNotGeneratedYet("rubric", v) }},
		{"remote version", func(v string) error { return EvaluatorDrifted("rubric", v, "recorded") }},
		{"recorded version", func(v string) error { return EvaluatorDrifted("rubric", "remote", v) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for control := rune(0); control <= 0x9f; control++ {
				if !unicode.IsControl(control) {
					continue
				}
				t.Run(fmt.Sprintf("U+%04X", control), func(t *testing.T) {
					value := "before" + string(control) + "after"
					var out bytes.Buffer
					_, err := out.WriteString(tc.render(value).Error())
					require.NoError(t, err)
					assert.NotContains(t, out.String(), value)
					assert.Contains(t, out.String(), strconv.Quote(value))
					assert.Equal(t, -1, strings.IndexFunc(out.String(), unicode.IsControl))
				})
			}
		})
	}
}

func TestTerminalProsePreservesSafeTextAndWrappedCause(t *testing.T) {
	for _, value := range []string{"", "safe", "team evals", "C:/Users/Me/quality", "caf\u00e9", "model/connection"} {
		assert.Equal(t, value, terminalValue(value))
	}
	cause := errors.New("interrupted")
	err := WaitInterrupted("run-1", cause)
	assert.ErrorIs(t, err, cause)
	assert.Equal(t, "stopped waiting on run run-1, which is still running: interrupted. "+
		"Pick it back up with `azd ai eval run show run-1`", err.Error())
}
