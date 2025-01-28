package azdgrpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
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

func (s *userConfigService) Get(ctx context.Context, req *azdext.GetRequest) (*azdext.GetResponse, error) {
	value, exists := s.config.Get(req.Path)

	var valueBytes []byte
	if exists {
		bytes, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal value: %w", err)
		}

		valueBytes = bytes
	}

	return &azdext.GetResponse{
		Value: valueBytes,
		Found: exists,
	}, nil
}

func (s *userConfigService) GetString(ctx context.Context, req *azdext.GetStringRequest) (*azdext.GetStringResponse, error) {
	value, exists := s.config.GetString(req.Path)

	return &azdext.GetStringResponse{
		Value: value,
		Found: exists,
	}, nil
}

func (s *userConfigService) GetSection(
	ctx context.Context,
	req *azdext.GetSectionRequest,
) (*azdext.GetSectionResponse, error) {
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

	return &azdext.GetSectionResponse{
		Section: valueBytes,
		Found:   exists,
	}, nil
}

func (s *userConfigService) Set(ctx context.Context, req *azdext.SetRequest) (*azdext.SetResponse, error) {
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

	return &azdext.SetResponse{}, nil
}

func (s *userConfigService) Unset(ctx context.Context, req *azdext.UnsetRequest) (*azdext.UnsetResponse, error) {
	if err := s.config.Unset(req.Path); err != nil {
		return nil, fmt.Errorf("failed to unset value: %w", err)
	}

	if err := s.configManager.Save(s.config); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	return &azdext.UnsetResponse{}, nil
}
