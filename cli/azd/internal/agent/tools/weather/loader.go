package weather

import (
	"github.com/tmc/langchaingo/tools"
)

// WeatherToolsLoader loads weather-related tools
type WeatherToolsLoader struct{}

func NewWeatherToolsLoader() *WeatherToolsLoader {
	return &WeatherToolsLoader{}
}

func (l *WeatherToolsLoader) LoadTools() ([]tools.Tool, error) {
	return []tools.Tool{
		&WeatherTool{},
	}, nil
}
