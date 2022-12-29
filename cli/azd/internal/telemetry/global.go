package telemetry

import (
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry/baggage"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/atomic"
)

// Attributes that are global and set on all events
var global atomic.Value

// Attributes that are only set on command-level usage events
var usage atomic.Value

// mutex for multiple writers
var globalMu sync.Mutex
var usageMu sync.Mutex

func init() {
	global.Store(baggage.NewBaggage())
	usage.Store(baggage.NewBaggage())
}

func SetGlobalAttributes(attributes ...attribute.KeyValue) {
	globalMu.Lock()
	defer globalMu.Unlock()

	baggage := global.Load().(baggage.Baggage)
	newBaggage := baggage.Set(attributes...)

	global.Store(newBaggage)
}

func GetGlobalAttributes() []attribute.KeyValue {
	baggage := global.Load().(baggage.Baggage)
	return baggage.Attributes()
}

func SetUsageAttributes(attributes ...attribute.KeyValue) {
	usageMu.Lock()
	defer usageMu.Unlock()

	baggage := usage.Load().(baggage.Baggage)
	newBaggage := baggage.Set(attributes...)

	usage.Store(newBaggage)
}

func GetUsageAttributes() []attribute.KeyValue {
	baggage := usage.Load().(baggage.Baggage)
	return baggage.Attributes()
}

// Sets environment-related attributes globally for telemetry purposes.
func SetEnvironment(env *environment.Environment) {
	attributes := []attribute.KeyValue{
		fields.SubscriptionIdKey.String(env.GetSubscriptionId()),
	}

	SetGlobalAttributes(attributes...)
}

// Sets project config related attributes for telemetry purposes.
// func SetProjectConfig(projectConfig *project.Config) {
// 	SetGlobalAttributes(fields.TemplateIdKey.String(resource.Sha256Hash(strings.ToLower(projectConfig.Metadata.Template))))
// }

// Sets the template name globally for telemetry purposes.
func SetTemplateName(templateName string) {
	SetGlobalAttributes(fields.TemplateIdKey.String(resource.Sha256Hash(strings.ToLower(templateName))))
}
