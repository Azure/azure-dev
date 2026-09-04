// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package errchain preserves the historical internal API for error-chain
// telemetry. The implementation lives in the public package so extensions
// can use the same normalization without importing an internal package.
package errchain

import (
	"github.com/azure/azure-dev/cli/azd/pkg/errorchain"
)

const MaxChainLen = errorchain.MaxChainLen

func Types(err error) []string {
	return errorchain.Types(err)
}

func DeepestNamedType(err error) string {
	return errorchain.DeepestNamedType(err)
}

func IsGenericWrapper(typeName string) bool {
	return errorchain.IsGenericWrapper(typeName)
}

func SanitizeTypeName(name string) string {
	return errorchain.SanitizeTypeName(name)
}
