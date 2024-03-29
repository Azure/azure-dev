package apphost

import (
	"fmt"
)

// buildAcaIngress builds the Azure Container Apps ingress configuration from the provided bindings.
func buildAcaIngress(bindings map[string]*Binding, defaultIngressPort int) (*genContainerAppIngress, error) {
	if len(bindings) == 0 {
		return nil, nil
	}

	if err := validateInput(bindings); err != nil {
		return nil, err
	}

	endpointByContainerPort := make(map[string][]*Binding)
	for _, binding := range bindings {
		bindingKey := "default" // default is for those with no container port
		if binding.TargetPort != nil {
			bindingKey = fmt.Sprintf("%d", *binding.TargetPort)
		}
		endpointByContainerPort[bindingKey] = append(endpointByContainerPort[bindingKey], binding)
	}

	endpointByContainerPortProperties := make(map[string]*endpointGroupProperties)
	for containerPort, bindings := range endpointByContainerPort {
		props := &endpointGroupProperties{httpOnly: true}
		for _, binding := range bindings {
			if binding.TargetPort != nil {
				props.port = *binding.TargetPort
			}
			if binding.External {
				props.external = true
			}
			if binding.Scheme != acaIngressSchemaHttp && binding.Scheme != acaIngressSchemaHttps {
				props.httpOnly = false
			}
			if binding.Transport == acaIngressTransportHttp2 {
				props.hasHttp2 = true
			}
		}
		endpointByContainerPortProperties[containerPort] = props
	}

	countExternalGroups := 0
	for _, props := range endpointByContainerPortProperties {
		if props.external {
			countExternalGroups++
		}
		if props.external && !props.httpOnly {
			return nil, fmt.Errorf("External non-HTTP(s) endpoints are not supported")
		}
	}
	if countExternalGroups > 1 {
		return nil, fmt.Errorf("Multiple external endpoints are not supported")
	}

	var httpOnlyGroups []string
	for groupKey, props := range endpointByContainerPortProperties {
		if props.httpOnly {
			httpOnlyGroups = append(httpOnlyGroups, groupKey)
		}
	}

	var ingress string
	if len(httpOnlyGroups) == 1 {
		ingress = httpOnlyGroups[0]
	}

	if ingress == "" {
		// We have more than one, pick prefer external one
		var externalHttpOnly []string
		for _, groupKey := range httpOnlyGroups {
			if endpointByContainerPortProperties[groupKey].external {
				externalHttpOnly = append(externalHttpOnly, groupKey)
			}
		}

		if len(externalHttpOnly) == 1 {
			ingress = externalHttpOnly[0]
		} else if len(httpOnlyGroups) > 1 {
			return nil, fmt.Errorf("Multiple internal only HTTP(s) endpoints are not supported")
		}
	}

	additionalPortsCount := len(endpointByContainerPort)
	if ingress != "" {
		additionalPortsCount--
	}
	if additionalPortsCount > 5 {
		return nil, fmt.Errorf("More than 5 additional ports are not supported. " +
			"See https://learn.microsoft.com/en-us/azure/container-apps/ingress-overview#tcp for more details.")
	}

	finalIngress := &genContainerAppIngress{}

	if ingress != "" {
		props := endpointByContainerPortProperties[ingress]

		finalIngress.External = props.external
		finalIngress.TargetPort = defaultIngressPort
		finalIngress.Transport = acaIngressSchemaHttp
		if props.hasHttp2 {
			finalIngress.Transport = acaIngressTransportHttp2
		}
		if props.hasHttp2 || !props.external {
			finalIngress.AllowInsecure = true
		}

	} else {
		port := 0
		for groupKey := range endpointByContainerPort {
			port = endpointByContainerPortProperties[groupKey].port
			ingress = groupKey
			break
		}
		finalIngress.Transport = acaIngressSchemaTcp
		finalIngress.TargetPort = port
	}

	for groupKey, props := range endpointByContainerPortProperties {
		if groupKey == ingress {
			continue
		}
		finalIngress.AdditionalPortMappings = append(finalIngress.AdditionalPortMappings,
			genContainerAppIngressAdditionalPortMappings{
				genContainerAppIngressPort: genContainerAppIngressPort{
					TargetPort: props.port,
				},
			})
	}

	return finalIngress, nil
}

type endpointGroupProperties struct {
	port     int
	external bool
	httpOnly bool
	hasHttp2 bool
}

const (
	acaIngressSchemaTcp      string = "tcp"
	acaIngressSchemaHttp     string = "http"
	acaIngressSchemaHttps    string = "https"
	acaIngressTransportHttp2 string = "http2"
	acaIngressTransportHttp  string = "http"
	acaIngressProtocolHttp   string = "http"
)

func validateInput(bindings map[string]*Binding) error {
	for name, binding := range bindings {
		if binding == nil {
			return fmt.Errorf("binding %q is empty", name)
		}

		switch binding.Scheme {
		case acaIngressSchemaTcp:
			if binding.TargetPort == nil {
				return fmt.Errorf("binding %q has scheme %q but no container port", name, binding.Scheme)
			}
		case acaIngressSchemaHttp:
		case acaIngressSchemaHttps:
		default:
			return fmt.Errorf("binding %q has invalid scheme %q", name, binding.Scheme)
		}
	}

	return nil
}
