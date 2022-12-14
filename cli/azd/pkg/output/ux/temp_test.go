// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"fmt"
	"testing"
)

func TestJsonFormatterSlice(t *testing.T) {
	obj := MessageTitle{
		Title:     "Foo",
		TitleNote: "Bar",
	}
	fmt.Println(string(obj.ToJson()))
}
