// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package osutil

import (
	"context"
	"errors"
	"fmt"
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
	return retryFileSystemOperation(ctx, fmt.Sprintf("rename of %s to %s", old, new), func() error {
		return os.Rename(old, new)
	})
}

// RemoveAll removes path and any children it contains, retrying transient Windows file locks.
func RemoveAll(ctx context.Context, path string) error {
	return retryFileSystemOperation(ctx, fmt.Sprintf("remove of %s", path), func() error {
		return os.RemoveAll(path)
	})
}

func retryFileSystemOperation(ctx context.Context, description string, operation func() error) error {
	return retry.Do(ctx, retry.WithMaxRetries(10, retry.NewConstant(time.Second)), func(context.Context) error {
		err := operation()
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
			errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			log.Printf("%s failed due to a transient file lock, allowing retry", description)
			return retry.RetryableError(err)
		}
		return err
	})
}
