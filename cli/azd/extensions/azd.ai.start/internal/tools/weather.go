package tools

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/tmc/langchaingo/callbacks"
)

// WeatherTool implements the Tool interface for getting weather information
type WeatherTool struct {
	CallbacksHandler callbacks.Handler
}

func (t WeatherTool) Name() string {
	return "weather"
}

func (t WeatherTool) Description() string {
	return "Get current weather conditions for a city. Input: city name (e.g., 'San Diego' or 'New York')"
}

func (t WeatherTool) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("weather: %s", input))
	}

	city := strings.TrimSpace(input)
	if city == "" {
		err := fmt.Errorf("city name is required")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Initialize random seed based on current time
	rand.Seed(time.Now().UnixNano())

	// Generate more realistic temperature based on city
	var temperature int
	cityLower := strings.ToLower(city)

	// Assign temperature ranges based on typical climate
	if strings.Contains(cityLower, "san diego") || strings.Contains(cityLower, "los angeles") ||
		strings.Contains(cityLower, "miami") || strings.Contains(cityLower, "phoenix") {
		// Warm climate cities: 65-85°F
		temperature = rand.Intn(21) + 65
	} else if strings.Contains(cityLower, "seattle") || strings.Contains(cityLower, "portland") ||
		strings.Contains(cityLower, "chicago") || strings.Contains(cityLower, "new york") {
		// Moderate climate cities: 45-75°F
		temperature = rand.Intn(31) + 45
	} else if strings.Contains(cityLower, "alaska") || strings.Contains(cityLower, "minneapolis") ||
		strings.Contains(cityLower, "denver") {
		// Cold climate cities: 25-55°F
		temperature = rand.Intn(31) + 25
	} else {
		// Default range for unknown cities: 50-80°F
		temperature = rand.Intn(31) + 50
	}

	// Weather conditions with probabilities
	conditions := []string{
		"sunny", "sunny", "sunny", "sunny", // 40% chance
		"partly cloudy", "partly cloudy", "partly cloudy", // 30% chance
		"cloudy", "cloudy", // 20% chance
		"rainy", // 10% chance
	}
	condition := conditions[rand.Intn(len(conditions))]

	// Add some variety to the response format
	responseTemplates := []string{
		"It's %d°F and %s in %s",
		"Current weather in %s: %d°F and %s",
		"The weather in %s is %d°F with %s skies",
		"%s is experiencing %s weather at %d°F",
	}

	template := responseTemplates[rand.Intn(len(responseTemplates))]

	var response string
	if strings.Contains(template, "It's %d°F and %s in %s") {
		response = fmt.Sprintf(template, temperature, condition, city)
	} else if strings.Contains(template, "Current weather in %s: %d°F and %s") {
		response = fmt.Sprintf(template, city, temperature, condition)
	} else if strings.Contains(template, "The weather in %s is %d°F with %s skies") {
		response = fmt.Sprintf(template, city, temperature, condition)
	} else {
		// "%s is experiencing %s weather at %d°F"
		response = fmt.Sprintf(template, city, condition, temperature)
	}

	// Add some additional details occasionally
	if rand.Intn(3) == 0 {
		extras := []string{
			"Light breeze from the west.",
			"Humidity is comfortable.",
			"Perfect day to be outside!",
			"Visibility is excellent.",
			"No precipitation expected.",
		}
		if condition == "rainy" {
			extras = []string{
				"Light rain expected to continue.",
				"Bring an umbrella!",
				"Rain should clear up by evening.",
			}
		}
		extra := extras[rand.Intn(len(extras))]
		response += ". " + extra
	}

	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, response)
	}

	return response, nil
}
