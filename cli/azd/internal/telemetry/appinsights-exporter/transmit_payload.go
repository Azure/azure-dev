package appinsightsexporter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"

	"github.com/microsoft/ApplicationInsights-Go/appinsights/contracts"
)

type TelemetryItems []contracts.Envelope

func (items TelemetryItems) Serialize() []byte {
	var result bytes.Buffer
	encoder := json.NewEncoder(&result)

	for _, item := range items {
		end := result.Len()
		if err := encoder.Encode(item); err != nil {
			diagLog.Printf("Telemetry item failed to serialize: %s", err.Error())
			result.Truncate(end)
		}
	}

	return result.Bytes()
}

func (items *TelemetryItems) Deserialize(serialized []byte) {
	scanner := bufio.NewScanner(strings.NewReader(string(serialized)))
	result := TelemetryItems{}

	for scanner.Scan() {
		var envelope contracts.Envelope
		err := json.Unmarshal([]byte(scanner.Text()), &envelope)
		if err != nil {
			diagLog.Printf("Telemetry item failed to deserialize: %s", err.Error())
			continue
		}

		result = append(result, envelope)
	}

	*items = result
}
