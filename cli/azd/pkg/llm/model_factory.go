package llm

import (
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

type ModelFactory struct {
	serviceLocator ioc.ServiceLocator
}

func NewModelFactory(serviceLocator ioc.ServiceLocator) *ModelFactory {
	return &ModelFactory{
		serviceLocator: serviceLocator,
	}
}

func (f *ModelFactory) CreateModelContainer(modelType LlmType, opts ...ModelOption) (*ModelContainer, error) {
	var modelProvider ModelProvider
	if err := f.serviceLocator.ResolveNamed(string(modelType), &modelProvider); err != nil {
		return nil, err
	}

	return modelProvider.CreateModelContainer(opts...)
}

type ModelProvider interface {
	CreateModelContainer(opts ...ModelOption) (*ModelContainer, error)
}
