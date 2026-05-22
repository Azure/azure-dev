// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agents

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsTransientError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		transient bool
	}{
		{"connection reset", fmt.Errorf("read tcp: connection reset by peer"), true},
		{"connection refused", fmt.Errorf("dial tcp: connection refused"), true},
		{"unexpected EOF", fmt.Errorf("unexpected EOF"), true},
		{"not found", fmt.Errorf("not found"), false},
		{"auth error", fmt.Errorf("authorization denied"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.transient, IsTransientError(tt.err))
		})
	}
}
