// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
)

// utf8BOM is what Windows editors and PowerShell's Set-Content write ahead of
// otherwise valid UTF-8.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ReadFileNoBOM reads a file the user may have edited by hand, without the byte
// order mark a Windows editor puts in front of it.
//
// Neither encoding/json nor yaml.v3 skips one, and the error they raise names a
// character rather than the cause: "invalid character 'ï' looking for beginning
// of value" is not something a developer can act on.
func ReadFileNoBOM(path string) ([]byte, error) {
	data, err := readFileOverContention(path)
	if err != nil {
		return nil, err
	}
	return bytes.TrimPrefix(data, utf8BOM), nil
}
