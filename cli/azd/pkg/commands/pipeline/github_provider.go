// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import "github.com/azure/azure-dev/cli/azd/pkg/tools"

type gitHubScmProvider struct {
}

func (p *gitHubScmProvider) requiredTools() []tools.ExternalTool {
	return nil
}

type gitHubCiProvider struct {
}

func (p *gitHubCiProvider) requiredTools() []tools.ExternalTool {
	return nil
}
