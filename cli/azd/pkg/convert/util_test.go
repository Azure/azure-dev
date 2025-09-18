// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package convert

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/stretchr/testify/require"
)

func Test_ToStringWithDefault(t *testing.T) {
	type testCase struct {
		name     string
		input    interface{}
		expected interface{}
	}

	testCases := []testCase{
		{
			name:     "ValidString",
			input:    "apple",
			expected: "apple",
		},
		{
			name:     "NotString",
			input:    1,
			expected: "default",
		},
		{
			name:     "EmptyString",
			input:    "",
			expected: "default",
		},
		{
			name:     "Nil",
			input:    nil,
			expected: "default",
		},
		{
			name:     "StringPointer",
			input:    to.Ptr("apple"),
			expected: "apple",
		},
		{
			name:     "NotStringPointer",
			input:    to.Ptr(1),
			expected: "default",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := ToStringWithDefault(tc.input, "default")
			require.Equal(t, tc.expected, actual)
		})
	}
}

func Test_ToValueWithDefault(t *testing.T) {
	t.Run("String", func(t *testing.T) {
		value := ToValueWithDefault(to.Ptr("apple"), "default")
		require.Equal(t, "apple", value)
	})

	t.Run("Int", func(t *testing.T) {
		value := ToValueWithDefault(to.Ptr(1), 0)
		require.Equal(t, 1, value)
	})

	t.Run("Nil", func(t *testing.T) {
		value := ToValueWithDefault(nil, "default")
		require.Equal(t, "default", value)
	})

	t.Run("EmptyString", func(t *testing.T) {
		value := ToValueWithDefault(to.Ptr(""), "default")
		require.Equal(t, "default", value)
	})
}

type Person struct {
	Name    string
	Address string
}

func Test_ToMap(t *testing.T) {
	t.Run("ValidStruct", func(t *testing.T) {
		input := Person{
			Name:    "John Doe",
			Address: "123 Main St",
		}
		expected := map[string]interface{}{
			"Name":    "John Doe",
			"Address": "123 Main St",
		}
		actual, err := ToMap(input)
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	})

	t.Run("EmptyStruct", func(t *testing.T) {
		input := struct{}{}
		expected := map[string]interface{}{}
		actual, err := ToMap(input)
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	})
}

func Test_ParseDuration(t *testing.T) {
	value := "PT0.3848S"

	duration, err := ParseDuration(value)
	require.NoError(t, err)
	require.NotNil(t, duration)
}
