// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !record

package recordproxy

import "net/http"

// Transport is nil in non-record builds.
var Transport http.RoundTripper
