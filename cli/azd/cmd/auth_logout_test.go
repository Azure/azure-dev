// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func Test_NewLogoutAction(t *testing.T) {
	t.Parallel()
	action := newLogoutAction(
		nil, // authManager
		nil, // accountSubManager
		&output.JsonFormatter{},
		&bytes.Buffer{},
		mockinput.NewMockConsole(),
		CmdAnnotations{},
	)
	require.NotNil(t, action)
}
