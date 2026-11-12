// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package foundry is the root of the independently versioned shared Go module
// for Microsoft Foundry azd extensions.
//
// Consumers import child packages such as:
//   - github.com/azure/azure-dev/cli/shared/foundry/access
//   - github.com/azure/azure-dev/cli/shared/foundry/ferrors
//
// This module must not require or import github.com/azure/azure-dev/cli/azd or
// any extension's internal packages.
package foundry
