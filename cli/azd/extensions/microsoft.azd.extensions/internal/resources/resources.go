// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"embed"
)

// The `all:` prefix ensures dotfiles such as `.gitignore` are embedded; without it
// `go:embed` skips files and directories whose names begin with `.` or `_`.
//
//go:embed all:languages
var Languages embed.FS
