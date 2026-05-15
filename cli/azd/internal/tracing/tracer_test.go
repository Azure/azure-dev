// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tracing

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

type typedSubSpanErr struct{}

func (typedSubSpanErr) Error() string {
	return "typed sub-span error"
}

func TestSubSpanErrorDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "PlainErrorUsesUnclassified",
			err:  errors.New("boom"),
			want: "internal.unclassified",
		},
		{
			name: "WrappedPlainErrorUsesUnclassified",
			err:  fmt.Errorf("ctx: %w", errors.New("boom")),
			want: "internal.unclassified",
		},
		{
			name: "WrappedTypedErrorUsesDeepestType",
			err:  fmt.Errorf("ctx: %w", &typedSubSpanErr{}),
			want: "internal.tracing_typedSubSpanErr",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, subSpanErrorDescription(tt.err))
		})
	}
}
