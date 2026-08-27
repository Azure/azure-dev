// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows && !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris

package osutil

import (
	"fmt"
	"runtime"
)

func readContainedFile(_, _ string) ([]byte, error) {
	return nil, fmt.Errorf("secure contained file reads are not supported on %s", runtime.GOOS)
}
