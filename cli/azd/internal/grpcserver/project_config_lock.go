// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import "sync"

// ProjectConfigMutationLocker serializes azure.yaml mutations performed by gRPC services.
type ProjectConfigMutationLocker interface {
	sync.Locker
}

// NewProjectConfigMutationLocker creates the process-level project configuration mutation lock.
func NewProjectConfigMutationLocker() ProjectConfigMutationLocker {
	return &sync.Mutex{}
}
