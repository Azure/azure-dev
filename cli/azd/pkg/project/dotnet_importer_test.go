package project

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMapToStringSlice(t *testing.T) {
	// Test case 1: Empty map
	m1 := make(map[string]string)
	expected1 := []string(nil)
	result1 := mapToStringSlice(m1, ":")
	slices.Sort(result1)
	assert.Equal(t, expected1, result1)

	// Test case 2: Map with values
	m2 := map[string]string{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}
	expected2 := []string{"key1:value1", "key2:value2", "key3:value3"}
	result2 := mapToStringSlice(m2, ":")
	slices.Sort(result2)
	assert.Equal(t, expected2, result2)

	// Test case 3: Map with empty values
	m3 := map[string]string{
		"key1": "",
		"key2": "",
		"key3": "",
	}
	expected3 := []string{"key1", "key2", "key3"}
	result3 := mapToStringSlice(m3, ":")
	slices.Sort(result3)
	assert.Equal(t, expected3, result3)
}
