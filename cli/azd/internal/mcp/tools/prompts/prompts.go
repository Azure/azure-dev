// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompts

import (
	_ "embed"
)

//go:embed azd_error_troubleshooting.md
var AzdErrorTroubleShootingPrompt string

//go:embed azd_provision_common_error.md
var AzdProvisionCommonErrorPrompt string
