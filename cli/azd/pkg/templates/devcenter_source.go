package templates

import (
	"context"
	"errors"
)

type DevCenterSource struct{}

func (s *DevCenterSource) Name() string {
	return "Dev Center"
}

func (s *DevCenterSource) ListTemplates(ctx context.Context) ([]*Template, error) {
	// TODO: Implement this method
	return nil, errors.New("not implemented")
}

func (s *DevCenterSource) GetTemplate(ctx context.Context, path string) (*Template, error) {
	// TODO: Implement this method
	return nil, errors.New("not implemented")
}
