// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package inspector hosts the standalone Agent Inspector SPA in-process so
// that `azd ai agent invoke --local --inspector` can launch the inspector
// in a browser without any Python/Node runtime dependency on the user's
// machine.
//
// The frontend bundle is the same one used by the VS Code extension built
// in standalone mode (`npm run build:azd` in webview-ui/). Its assets
// are copied into ./assets/ at extension build time and embedded into the
// Go binary via go:embed below.
//
// The backend is a Go reimplementation of the Python rpc_handler.py that
// ships with the agent-dev-cli wheel. The wire protocol is vscode-jsonrpc
// over a single WebSocket at /agentdev/ws/rpc.
package inspector

import (
	"embed"
	"io/fs"
)

//go:embed all:assets
var embeddedAssets embed.FS

// Assets returns an fs.FS rooted at the assets directory so it can be
// served directly by http.FileServer.
func Assets() fs.FS {
	sub, err := fs.Sub(embeddedAssets, "assets")
	if err != nil {
		// fs.Sub only fails on a malformed path; "assets" is a constant.
		panic(err)
	}
	return sub
}
