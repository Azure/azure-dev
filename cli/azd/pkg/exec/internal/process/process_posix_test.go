// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package process

import (
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessTerminationError(t *testing.T) {
	descendantErr := errors.New("descendant termination failed")

	tests := []struct {
		name                 string
		rootErr              error
		descendantErr        error
		terminatedDescendant bool
		wantErr              error
	}{
		{
			name: "root and descendants terminated",
		},
		{
			name:                 "finished root with terminated descendant",
			rootErr:              os.ErrProcessDone,
			terminatedDescendant: true,
		},
		{
			name:    "finished root without terminated descendant",
			rootErr: os.ErrProcessDone,
			wantErr: os.ErrProcessDone,
		},
		{
			name:                 "root failure with terminated descendant",
			rootErr:              syscall.EPERM,
			terminatedDescendant: true,
			wantErr:              syscall.EPERM,
		},
		{
			name:          "descendant failure",
			descendantErr: descendantErr,
			wantErr:       descendantErr,
		},
		{
			name:          "root and descendant failures",
			rootErr:       syscall.EPERM,
			descendantErr: descendantErr,
			wantErr:       syscall.EPERM,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := processTerminationError(
				test.rootErr,
				test.descendantErr,
				test.terminatedDescendant,
			)

			if test.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, test.wantErr)
			}
			if test.rootErr != nil &&
				test.descendantErr != nil &&
				!errors.Is(test.rootErr, os.ErrProcessDone) {
				require.ErrorIs(t, err, test.descendantErr)
			}
		})
	}
}
