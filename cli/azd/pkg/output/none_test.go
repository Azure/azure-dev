// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNoneFormatter_Format_Errors(t *testing.T) {
	t.Parallel()
	f := &NoneFormatter{}
	var buf bytes.Buffer
	err := f.Format(map[string]string{"a": "b"}, &buf, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "none")
	require.Empty(t, buf.String())
}
