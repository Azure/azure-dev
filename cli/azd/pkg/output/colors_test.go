package output

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_WithHyperlink(t *testing.T) {
	t.Run("Link and name", func(t *testing.T) {
		actual := WithHyperlink("https://aka.ms/azd", "azd")
		expected := "\033]8;;https://aka.ms/azd\007azd\033]8;;\007"

		require.Equal(t, expected, actual)
	})

	t.Run("Link only", func(t *testing.T) {
		actual := WithHyperlink("https://aka.ms/azd", "")
		expected := "\033]8;;https://aka.ms/azd\007https://aka.ms/azd\033]8;;\007"

		require.Equal(t, expected, actual)
	})

	t.Run("No color", func(t *testing.T) {
		os.Setenv("NO_COLOR", "true")

		actual := WithHyperlink("https://aka.ms/azd", "")
		expected := "https://aka.ms/azd"

		require.Equal(t, expected, actual)
	})
}
