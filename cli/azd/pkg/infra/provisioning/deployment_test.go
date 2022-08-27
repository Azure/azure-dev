// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParameterHasDefaultValue(t *testing.T) {
	t.Run("WithNilValue", func(t *testing.T) {
		param := InputParameter{
			Type:         "string",
			DefaultValue: nil,
		}

		actual := param.HasDefaultValue()
		require.False(t, actual)
	})

	t.Run("WithEmptyString", func(t *testing.T) {
		param := InputParameter{
			Type:         "string",
			DefaultValue: "",
		}

		actual := param.HasDefaultValue()
		require.True(t, actual)
	})
}

func TestParameterHasValue(t *testing.T) {
	t.Run("WithNilValue", func(t *testing.T) {
		param := InputParameter{
			Type:  "string",
			Value: nil,
		}

		actual := param.HasValue()
		require.False(t, actual)
	})

	t.Run("WithEmptyString", func(t *testing.T) {
		param := InputParameter{
			Type:  "string",
			Value: "",
		}

		actual := param.HasValue()
		require.True(t, actual)
	})
}
