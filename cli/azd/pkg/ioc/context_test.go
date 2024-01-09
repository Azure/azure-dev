package ioc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Context_Get_With_Container(t *testing.T) {
	t.Run("should set container in context", func(t *testing.T) {
		container := NewNestedContainer(nil)
		ctx := context.Background()

		ctx = WithContainer(ctx, container)

		c, err := GetContainer(ctx)
		require.NoError(t, err)
		require.Equal(t, container, c)
	})

	t.Run("should return error if container not found in context", func(t *testing.T) {
		ctx := context.Background()

		_, err := GetContainer(ctx)
		require.Error(t, err)
	})
}
