// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_getAzdoConnection(t *testing.T) {
	ctx := context.Background()
	t.Run("empty organization name error", func(t *testing.T) {
		_, err := GetConnection(ctx, "", "")
		assert.EqualError(t, err, "organization name is required")
	})

	t.Run("empty pat error", func(t *testing.T) {
		_, err := GetConnection(ctx, "fake_org", "")
		assert.EqualError(t, err, "personal access token is required")
	})
	t.Run("returns a connection", func(t *testing.T) {
		connection, err := GetConnection(ctx, "fake_org", "fake_pat")
		assert.Nil(t, err)
		assert.NotNil(t, connection)
	})
}
