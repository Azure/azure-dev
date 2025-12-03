// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import "regexp"

var ConnectionStringJSONRegex = regexp.MustCompile(`("[\w]*(?:CONNECTION_STRING|ConnectionString)":\s*)"[^"]*"`)
