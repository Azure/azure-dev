// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"strings"
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

// TrimBOM drops a leading byte order mark from text already in hand.
//
// It exists for the readers that cannot use ReadFileNoBOM: a validator
// streaming a large dataset a line at a time only ever sees the mark on its
// first line, and holding the whole file in memory to strip three bytes would
// be the wrong trade. Sharing the definition is the point -- a second copy is
// how the upload path and the validator came to disagree about whether a file
// PowerShell wrote is acceptable.
func TrimBOM(text string) string {
	return strings.TrimPrefix(text, string(utf8BOM))
}
