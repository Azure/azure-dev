// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package monitor

import "embed"

// Assets contains the self-contained rollout monitor frontend.
//
//go:embed web/index.html web/styles.css web/app.js web/data.mjs
var Assets embed.FS
