package output

import (
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/require"
)

func Test_WithHyperlink(t *testing.T) {
	t.Run("Link and name", func(t *testing.T) {
		color.NoColor = false
		actual := WithHyperlink("https://aka.ms/azd", "azd")
		expected := "\x1b[96m\x1b]8;;https://aka.ms/azd\aazd\x1b]8;;\a\x1b[0m"

		require.Equal(t, expected, actual)
	})

	t.Run("Link only", func(t *testing.T) {
		color.NoColor = false
		actual := WithHyperlink("https://aka.ms/azd", "")
		expected := "\x1b[96m\x1b]8;;https://aka.ms/azd\ahttps://aka.ms/azd\x1b]8;;\a\x1b[0m"

		require.Equal(t, expected, actual)
	})

	t.Run("No color", func(t *testing.T) {
		color.NoColor = true
		actual := WithHyperlink("https://aka.ms/azd", "")
		expected := "https://aka.ms/azd"

		require.Equal(t, expected, actual)
	})
}
