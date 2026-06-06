// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package inspector serves the embedded Agent Inspector SPA and proxies
// its localhost calls over a JSON-RPC WebSocket.
package inspector

import (
	"embed"
	"io/fs"
)

//go:embed all:assets
var embeddedAssets embed.FS

// Assets returns the SPA bundle rooted at the assets directory.
func Assets() fs.FS {
	sub, err := fs.Sub(embeddedAssets, "assets")
	if err != nil {
		panic(err)
	}
	return sub
}
