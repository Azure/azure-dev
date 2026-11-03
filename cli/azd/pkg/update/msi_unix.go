// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package update

import (
	"context"
	"io"
)

func (m *Manager) updateViaMSI(context.Context, *VersionInfo, io.Writer) error {
	return newUpdateErrorf(CodeUnsupportedInstallMethod, "MSI updates are only supported on Windows")
}

func (m *Manager) verifyAuthenticode(context.Context, string, io.Writer) error {
	return newUpdateErrorf(CodeUnsupportedInstallMethod, "Authenticode verification is only supported on Windows")
}
