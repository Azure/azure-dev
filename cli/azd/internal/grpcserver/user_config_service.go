// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/pkg/azdext"
	"github.com/azure/azure-dev/pkg/config"
)

// configService is the implementation of ConfigServiceServer.
type userConfigService struct {
	azdext.UnimplementedUserConfigServiceServer

	configManager config.UserConfigManager
	config        config.Config
}

// NewConfigService creates a new instance of configService.
func NewUserConfigService(userConfigManager config.UserConfigManager) (azdext.UserConfigServiceServer, error) {
	config, err := userConfigManager.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load user config: %w", err)
	}

	return &userConfigService{
		configManager: userConfigManager,
		config:        config,
	}, nil
}

func (s *userConfigService) Get(
	ctx context.Context,
	req *azdext.GetUserConfigRequest,
) (*azdext.GetUserConfigResponse, error) {
	value, exists := s.config.Get(req.Path)

	var valueBytes []byte
	if exists {
		bytes, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal value: %w", err)
		}

		valueBytes = bytes
	}

	return &azdext.GetUserConfigResponse{
		Value: valueBytes,
		Found: exists,
	}, nil
}

func (s *userConfigService) GetString(
	ctx context.Context,
	req *azdext.GetUserConfigStringRequest,
) (*azdext.GetUserConfigStringResponse, error) {
	value, exists := s.config.GetString(req.Path)

	return &azdext.GetUserConfigStringResponse{
		Value: value,
		Found: exists,
	}, nil
}

func (s *userConfigService) GetSection(
	ctx context.Context,
	req *azdext.GetUserConfigSectionRequest,
) (*azdext.GetUserConfigSectionResponse, error) {
	var section map[string]any

	exists, err := s.config.GetSection(req.Path, &section)
	if err != nil {
		return nil, fmt.Errorf("failed to get section: %w", err)
	}

	var valueBytes []byte
	if exists {
		bytes, err := json.Marshal(section)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal value: %w", err)
		}

		valueBytes = bytes
	}

	return &azdext.GetUserConfigSectionResponse{
		Section: valueBytes,
		Found:   exists,
	}, nil
}

func (s *userConfigService) Set(ctx context.Context, req *azdext.SetUserConfigRequest) (*azdext.EmptyResponse, error) {
	var value any
	if err := json.Unmarshal(req.Value, &value); err != nil {
		return nil, fmt.Errorf("failed to unmarshal value: %w", err)
	}

	if err := s.config.Set(req.Path, value); err != nil {
		return nil, fmt.Errorf("failed to set value: %w", err)
	}

	if err := s.configManager.Save(s.config); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	return &azdext.EmptyResponse{}, nil
}

func (s *userConfigService) Unset(ctx context.Context, req *azdext.UnsetUserConfigRequest) (*azdext.EmptyResponse, error) {
	if err := s.config.Unset(req.Path); err != nil {
		return nil, fmt.Errorf("failed to unset value: %w", err)
	}

	if err := s.configManager.Save(s.config); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	return &azdext.EmptyResponse{}, nil
}
