package provisioning

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParameterHasDefaultValue(t *testing.T) {
	t.Run("WithNilValue", func(t *testing.T) {
		param := PreviewInputParameter{
			Type:         "string",
			DefaultValue: nil,
		}

		actual := param.HasDefaultValue()
		require.False(t, actual)
	})

	t.Run("WithEmptyString", func(t *testing.T) {
		param := PreviewInputParameter{
			Type:         "string",
			DefaultValue: "",
		}

		actual := param.HasDefaultValue()
		require.True(t, actual)
	})
}

func TestParameterHasValue(t *testing.T) {
	t.Run("WithNilValue", func(t *testing.T) {
		param := PreviewInputParameter{
			Type:  "string",
			Value: nil,
		}

		actual := param.HasValue()
		require.False(t, actual)
	})

	t.Run("WithEmptyString", func(t *testing.T) {
		param := PreviewInputParameter{
			Type:  "string",
			Value: "",
		}

		actual := param.HasValue()
		require.True(t, actual)
	})
}
