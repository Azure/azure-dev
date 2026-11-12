// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !linux && !darwin && !windows

package localio

import (
	"errors"
	"io"
	"os"
)

func guardDirectory(*os.Root, bool) (io.Closer, error) {
	return nil, errors.New("safe local output is unsupported on this platform")
}

func guardFile(*os.Root, string) error {
	return errors.New("safe local output is unsupported on this platform")
}

func secureStaging(*os.Root) error {
	return errors.New("safe local output is unsupported on this platform")
}
