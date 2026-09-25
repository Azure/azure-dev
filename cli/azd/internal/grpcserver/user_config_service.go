// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// configService is the implementation of ConfigServiceServer.
type userConfigService struct {
	azdext.UnimplementedUserConfigServiceServer

	configManager config.UserConfigManager
}

// NewConfigService creates a new instance of configService.
func NewUserConfigService(userConfigManager config.UserConfigManager) (azdext.UserConfigServiceServer, error) {
	if _, err := userConfigManager.Load(); err != nil {
		return nil, fmt.Errorf("failed to load user config: %w", err)
	}

	return &userConfigService{
		configManager: userConfigManager,
	}, nil
}

func (s *userConfigService) Get(
	ctx context.Context,
	req *azdext.GetUserConfigRequest,
) (*azdext.GetUserConfigResponse, error) {
	userConfig, err := s.configManager.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load user config: %w", err)
	}

	value, exists := userConfig.Get(req.Path)

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
	userConfig, err := s.configManager.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load user config: %w", err)
	}

	value, exists := userConfig.GetString(req.Path)

	return &azdext.GetUserConfigStringResponse{
		Value: value,
		Found: exists,
	}, nil
}

func (s *userConfigService) GetSection(
	ctx context.Context,
	req *azdext.GetUserConfigSectionRequest,
) (*azdext.GetUserConfigSectionResponse, error) {
	userConfig, err := s.configManager.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load user config: %w", err)
	}

	var section map[string]any

	exists, err := userConfig.GetSection(req.Path, &section)
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

	if err := s.configManager.Mutate(ctx, func(_ context.Context, userConfig config.Config) (bool, error) {
		currentValue, exists := userConfig.Get(req.Path)
		if exists && reflect.DeepEqual(currentValue, value) {
			return false, nil
		}
		if err := userConfig.Set(req.Path, value); err != nil {
			return false, fmt.Errorf("failed to set value: %w", err)
		}
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	return &azdext.EmptyResponse{}, nil
}

func (s *userConfigService) Unset(ctx context.Context, req *azdext.UnsetUserConfigRequest) (*azdext.EmptyResponse, error) {
	if err := s.configManager.Mutate(ctx, func(_ context.Context, userConfig config.Config) (bool, error) {
		if err := userConfig.Unset(req.Path); err != nil {
			return false, fmt.Errorf("failed to unset value: %w", err)
		}
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	return &azdext.EmptyResponse{}, nil
}

func (s *userConfigService) GetMapEntry(
	ctx context.Context,
	req *azdext.GetUserConfigMapEntryRequest,
) (*azdext.GetUserConfigMapEntryResponse, error) {
	userConfig, err := s.configManager.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load user config: %w", err)
	}

	value, found, err := userConfig.GetRawMapEntry(req.Path, req.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to get map entry: %w", err)
	}

	valueBytes, revision, err := marshalMapEntry(value, found)
	if err != nil {
		return nil, err
	}

	return &azdext.GetUserConfigMapEntryResponse{
		Value:    valueBytes,
		Found:    found,
		Revision: revision,
	}, nil
}

func (s *userConfigService) SetMapEntry(
	ctx context.Context,
	req *azdext.SetUserConfigMapEntryRequest,
) (*azdext.EmptyResponse, error) {
	var value any
	if err := json.Unmarshal(req.Value, &value); err != nil {
		return nil, fmt.Errorf("failed to unmarshal value: %w", err)
	}

	if err := s.configManager.Mutate(ctx, func(_ context.Context, userConfig config.Config) (bool, error) {
		currentValue, found, err := userConfig.GetRawMapEntry(req.Path, req.Key)
		if err != nil {
			return false, err
		}
		if found && reflect.DeepEqual(currentValue, value) {
			return false, nil
		}
		if err := userConfig.SetRawMapEntry(req.Path, req.Key, value); err != nil {
			return false, err
		}
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	return &azdext.EmptyResponse{}, nil
}

func (s *userConfigService) DeleteMapEntry(
	ctx context.Context,
	req *azdext.DeleteUserConfigMapEntryRequest,
) (*azdext.EmptyResponse, error) {
	if err := s.configManager.Mutate(ctx, func(_ context.Context, userConfig config.Config) (bool, error) {
		_, found, err := userConfig.GetRawMapEntry(req.Path, req.Key)
		if err != nil {
			return false, err
		}
		if !found {
			return false, nil
		}
		if err := userConfig.DeleteRawMapEntry(req.Path, req.Key); err != nil {
			return false, err
		}
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	return &azdext.EmptyResponse{}, nil
}

func (s *userConfigService) CompareExchangeMapEntry(
	ctx context.Context,
	req *azdext.CompareExchangeUserConfigMapEntryRequest,
) (*azdext.CompareExchangeUserConfigMapEntryResponse, error) {
	var setValue any
	switch req.Operation {
	case azdext.UserConfigMapEntryOperation_USER_CONFIG_MAP_ENTRY_OPERATION_SET:
		if err := json.Unmarshal(req.Value, &setValue); err != nil {
			return nil, fmt.Errorf("failed to unmarshal value: %w", err)
		}
	case azdext.UserConfigMapEntryOperation_USER_CONFIG_MAP_ENTRY_OPERATION_DELETE:
	default:
		return nil, fmt.Errorf("unsupported map entry operation: %s", req.Operation)
	}

	response := &azdext.CompareExchangeUserConfigMapEntryResponse{}
	if err := s.configManager.Mutate(ctx, func(_ context.Context, userConfig config.Config) (bool, error) {
		currentValue, found, err := userConfig.GetRawMapEntry(req.Path, req.Key)
		if err != nil {
			return false, err
		}

		currentBytes, currentRevision, err := marshalMapEntry(currentValue, found)
		if err != nil {
			return false, err
		}
		response.Value = currentBytes
		response.Found = found
		response.Revision = currentRevision
		if currentRevision != req.ExpectedRevision {
			return false, nil
		}

		changed := false
		switch req.Operation {
		case azdext.UserConfigMapEntryOperation_USER_CONFIG_MAP_ENTRY_OPERATION_SET:
			changed = !found || !reflect.DeepEqual(currentValue, setValue)
			if changed {
				if err := userConfig.SetRawMapEntry(req.Path, req.Key, setValue); err != nil {
					return false, err
				}
			}
			response.Value, response.Revision, err = marshalMapEntry(setValue, true)
			response.Found = true
		case azdext.UserConfigMapEntryOperation_USER_CONFIG_MAP_ENTRY_OPERATION_DELETE:
			changed = found
			if changed {
				if err := userConfig.DeleteRawMapEntry(req.Path, req.Key); err != nil {
					return false, err
				}
			}
			response.Value, response.Revision, err = marshalMapEntry(nil, false)
			response.Found = false
		}
		response.Exchanged = err == nil
		if err != nil {
			return false, err
		}
		return changed, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to compare and exchange map entry: %w", err)
	}

	return response, nil
}

func marshalMapEntry(value any, found bool) ([]byte, string, error) {
	if !found {
		return nil, "", nil
	}

	valueBytes, err := json.Marshal(value)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal map entry: %w", err)
	}
	return valueBytes, fmt.Sprintf("%x", sha256.Sum256(valueBytes)), nil
}
