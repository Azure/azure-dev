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
	env.SetVariable("key2", "value2")
	expected = append(expected, "KEY2=value2")
	assert.Equal(t, expected, env.ToStringArray())
}
