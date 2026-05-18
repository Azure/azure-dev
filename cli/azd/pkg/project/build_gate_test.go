// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildGateRoundTrip(t *testing.T) {
	mu := &sync.Mutex{}
	ctx := ContextWithBuildGate(context.Background(), mu)
	got := BuildGateFromContext(ctx)
	require.Same(t, mu, got)
}

func TestBuildGateFromContext_NilWhenAbsent(t *testing.T) {
	require.Nil(t, BuildGateFromContext(context.Background()))
}

func TestSanitizeTempDirName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"alphanumeric passthrough", "webfrontend", "webfrontend"},
		{"hyphens preserved", "my-api-service", "my-api-service"},
		{"underscores preserved", "my_api", "my_api"},
		{"dots replaced", "my.service", "my_service"},
		{"slashes replaced", "path/to/svc", "path_to_svc"},
		{"spaces replaced", "my service", "my_service"},
		{"mixed unsafe chars", "svc@1.0/beta", "svc_1_0_beta"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, sanitizeTempDirName(tt.in))
		})
	}
}
