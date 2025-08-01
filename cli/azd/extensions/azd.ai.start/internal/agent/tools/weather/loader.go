package weather

import (
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/tools"
)

// WeatherToolsLoader loads weather-related tools
type WeatherToolsLoader struct {
	callbackHandler callbacks.Handler
}

func NewWeatherToolsLoader(callbackHandler callbacks.Handler) *WeatherToolsLoader {
	return &WeatherToolsLoader{
		callbackHandler: callbackHandler,
	}
}

func (l *WeatherToolsLoader) LoadTools() ([]tools.Tool, error) {
	return []tools.Tool{
		&WeatherTool{CallbacksHandler: l.callbackHandler},
	}, nil
}
