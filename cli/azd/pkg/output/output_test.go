package output

import (
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ColorWriter(t *testing.T) {
	t.Run("NoEnvVarSet", func(t *testing.T) {
		writer := GetDefaultWriter()
		writerType := fmt.Sprintf("%v", reflect.TypeOf(writer))

		require.Equal(t, "*os.File", writerType)
	})

	t.Run("WithEmptyEnvVar", func(t *testing.T) {
		os.Setenv("NO_COLOR", "")

		writer := GetDefaultWriter()
		writerType := fmt.Sprintf("%v", reflect.TypeOf(writer))

		require.Equal(t, "*os.File", writerType)
	})
}

func Test_NoColorWriter(t *testing.T) {
	os.Setenv("NO_COLOR", "some-value")

	writer := GetDefaultWriter()
	writerType := fmt.Sprintf("%v", reflect.TypeOf(writer))

	require.Equal(t, "*colorable.NonColorable", writerType)
}
