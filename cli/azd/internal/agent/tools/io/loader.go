// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/agent/security"
	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
)

// IoToolsLoader loads IO-related tools
type IoToolsLoader struct {
	securityManager *security.Manager
}

// NewIoToolsLoader creates a new instance of IoToolsLoader
func NewIoToolsLoader(securityManager *security.Manager) common.ToolLoader {
	return &IoToolsLoader{
		securityManager: securityManager,
	}
}

// NewIoToolsLoaderWithSecurityRoot creates a new instance of IoToolsLoader with a specific security root
// Deprecated: Use NewIoToolsLoader with DI instead
func NewIoToolsLoaderWithSecurityRoot(securityRoot string) common.ToolLoader {
	// This is kept for backward compatibility, but should be removed eventually
	sm, err := security.NewManager(securityRoot)
	if err != nil {
		// Return a loader with nil security manager - this will cause errors but won't panic
		return &IoToolsLoader{}
	}
	return &IoToolsLoader{
		securityManager: sm,
	}
}

// LoadTools loads and returns all IO-related tools
func (l *IoToolsLoader) LoadTools(ctx context.Context) ([]common.AnnotatedTool, error) {
	return []common.AnnotatedTool{
		&CurrentDirectoryTool{},
		&ChangeDirectoryTool{securityManager: l.securityManager},
		&DirectoryListTool{securityManager: l.securityManager},
		&CreateDirectoryTool{securityManager: l.securityManager},
		&DeleteDirectoryTool{securityManager: l.securityManager},
		&ReadFileTool{securityManager: l.securityManager},
		&WriteFileTool{securityManager: l.securityManager},
		&CopyFileTool{securityManager: l.securityManager},
		&MoveFileTool{securityManager: l.securityManager},
		&DeleteFileTool{securityManager: l.securityManager},
		&FileInfoTool{securityManager: l.securityManager},
		&FileSearchTool{securityManager: l.securityManager},
	}, nil
}
