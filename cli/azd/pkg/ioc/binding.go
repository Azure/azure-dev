// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ioc

// binding represents the metadata used for an IoC registration consisting of a optional name and resolver.
type binding struct {
	name     string
	resolver any
}
