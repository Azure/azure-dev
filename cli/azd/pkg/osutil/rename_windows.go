// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows
// +build windows

package osutil

import (
	"context"
	"errors"
	"log"
	"os"
	"time"

	"github.com/sethvargo/go-retry"
	"golang.org/x/sys/windows"
)

// Rename is like os.Rename except it will retry the operation, up to 10 times, waiting a second between each retry when the
// Rename fails due to what may be transient file system errors. This can help work around issues where the file may
// temporary be opened by a virus scanner or some other process which prevents us from renaming the file.
func Rename(ctx context.Context, old, new string) error {
	return retry.Do(ctx, retry.WithMaxRetries(10, retry.NewConstant(1*time.Second)), func(ctx context.Context) error {
		err := os.Rename(old, new)
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			// If some other process has a open handle to the source file, Rename can fail with ERROR_SHARING_VIOLATION.
			log.Printf("rename of %s to %s failed due to ERROR_SHARING_VIOLATION, allowing retry", old, new)
			return retry.RetryableError(err)
		} else if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			// If the target file has already exists and is in use, Rename can fail with ERROR_ACCESS_DENIED.
			log.Printf("rename of %s to %s failed due to ERROR_ACCESS_DENIED, allowing retry", old, new)
			return retry.RetryableError(err)
		}
		return err
	})
}
