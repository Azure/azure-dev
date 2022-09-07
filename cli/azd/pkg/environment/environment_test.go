// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsValidEnvironmentName(t *testing.T) {
	assert.True(t, IsValidEnvironmentName("simple"))
	assert.True(t, IsValidEnvironmentName("a-name-with-hyphens"))
	assert.True(t, IsValidEnvironmentName("C()mPl3x_ExAmPl3-ThatIsVeryLong"))

	assert.False(t, IsValidEnvironmentName(""))
	assert.False(t, IsValidEnvironmentName("no*allowed"))
	assert.False(t, IsValidEnvironmentName("no spaces"))
	assert.False(t, IsValidEnvironmentName("12345678901234567890123456789012345678901234567890123456789012345"))
}

func TestDeleteVariable(t *testing.T) {
	env := Environment{}
	assert.Equal(t, map[string]string(nil), env.CopyValues())
	env.Init()
	assert.Equal(t, map[string]string{}, env.CopyValues())
	env.SetVariable("key", "value")
	assert.Equal(t, "value", env.ValueOf("key"))
	assert.True(t, env.HasValue("key"))
	env.DeleteVariable("key")
	assert.False(t, env.HasValue("key"))
	value, found := env.GetValue("key")
	assert.Equal(t, "", value)
	assert.False(t, found)
}

func TestToStringArray(t *testing.T) {
	env := Environment{}
	env.Init()
	env.SetVariable("key", "value")
	expected := []string{"KEY=value"}
	assert.Equal(t, expected, env.ToStringArray())
}

func TestUpperCase(t *testing.T) {
	env := Environment{}
	env.Init()

	validateCombinations := func(expected string) {
		for _, key := range []string{
			"some_key", "SOME_KEY", "some_Key", "SOME_key", "Some_Key", "Some_Key", "Some_key"} {
			assert.Equal(t, expected, env.ValueOf(key))
		}
	}

	validateCombinations("")
	env.SetVariable("some_key", "value")
	validateCombinations("value")
	assert.Equal(t, "", env.ValueOf("someKey"))

	env.SetVariable("SOME_KEY", "other_value")
	validateCombinations("other_value")

	env.DeleteVariable("Some_Key")
	validateCombinations("")
}
