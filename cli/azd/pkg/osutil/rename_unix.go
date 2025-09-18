// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows
// +build !windows

package osutil

import (
	"context"
	"os"
)

// Rename is like os.Rename in every way. The context is ignored.
func Rename(ctx context.Context, old, new string) error {
	return os.Rename(old, new)
}
