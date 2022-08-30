package events

import "go.opentelemetry.io/otel/attribute"

var (
	MachineIdKey            = attribute.Key("machineId")
	ExecutionEnvironmentKey = attribute.Key("executionEnvironment")
	TerminalTypeKey         = attribute.Key("terminalType")

	ObjectIdKey       = attribute.Key("objectId")
	TenantIdKey       = attribute.Key("tenantId")
	SubscriptionIdKey = attribute.Key("subscriptionId")
	TemplateIdKey     = attribute.Key("templateId")
)
