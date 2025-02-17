// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ioc

type ServiceLocator interface {
	Resolve(instance any) error
	ResolveNamed(name string, instance any) error
	Invoke(resolver any) error
}
