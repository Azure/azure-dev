// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import "embed"

//go:embed templates/main.bicep
//go:embed templates/abbreviations.json
//go:embed templates/modules/*.bicep
var templatesFS embed.FS

// TemplatesFS exposes the embedded provisioning templates so callers can
// stage them on disk when invoking the bicep -> ARM compiler.
func TemplatesFS() embed.FS { return templatesFS }
