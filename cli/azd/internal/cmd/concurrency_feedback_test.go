// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"log"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/assert"
)

func TestResolveConcurrencySettingWarnsForNonPositiveValues(t *testing.T) {
	var output bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() {
		log.SetOutput(originalWriter)
	})

	for _, value := range []string{"0", "-1"} {
		output.Reset()
		setting := resolveConcurrencySetting(lookupEnvironment(map[string]string{
			packageConcurrencyEnvVar: value,
		}), packageConcurrencyEnvVar)

		assert.True(t, setting.set)
		assert.Zero(t, setting.value)
		assert.Contains(t, output.String(), "value must be greater than zero")
		assert.Contains(t, output.String(), "lower-precedence concurrency settings will not apply")
	}
}

func TestUpGraphRunOptionsUsesActiveEnvironment(t *testing.T) {
	t.Setenv(packageConcurrencyEnvVar, "1")
	t.Setenv(upConcurrencyEnvVar, "2")
	env := environment.NewWithValues("test", map[string]string{
		packageConcurrencyEnvVar: "3",
		upConcurrencyEnvVar:      "4",
	})

	opts := (&UpGraphAction{env: env}).runOptions()

	assert.Equal(t, 4, opts.MaxConcurrency)
	assert.Equal(t, 3, opts.GroupConcurrency[packageConcurrencyGroup])
	assert.Equal(t, 4, opts.GroupConcurrency[provisionConcurrencyGroup])
	assert.Equal(t, 4, opts.GroupConcurrency[deployConcurrencyGroup])
}
