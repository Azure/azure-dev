package llm

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal"
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
		return nil, &internal.ErrorWithSuggestion{
			Err:        fmt.Errorf("The model type '%s' is not supported. Support types include: azure, ollama", modelType),
			Suggestion: "Use `azd config set` to set the model type and any model specific options, such as the model name or version.",
		}
	}

	return modelProvider.CreateModelContainer(opts...)
}

type ModelProvider interface {
	CreateModelContainer(opts ...ModelOption) (*ModelContainer, error)
}
