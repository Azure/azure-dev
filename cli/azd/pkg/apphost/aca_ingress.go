package apphost

import (
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/custommaps"
)

// buildAcaIngress builds the Azure Container Apps ingress configuration from the provided bindings.
func buildAcaIngress(
	bindings custommaps.WithOrder[Binding], defaultIngressPort int) (*genContainerAppIngress, []string, error) {
	if len(bindings.OrderedKeys()) == 0 {
		return nil, nil, nil
	}

	if err := validateBindings(bindings); err != nil {
		return nil, nil, err
	}
	acaPorts := mapBindingsToPorts(bindings)

	if err := validateExternalBindings(acaPorts); err != nil {
		return nil, nil, err
	}

	ingress := httpIngress(acaPorts)

	additionalPortsCount := len(acaPorts)
	if ingress != "" {
		additionalPortsCount--
	}
	if additionalPortsCount > 5 {
		log.Println("More than 5 additional ports are not supported. " +
			"See https://learn.microsoft.com/azure/container-apps/ingress-overview#tcp for more details.")
	}

	return pickIngress(acaPorts, ingress, defaultIngressPort)
}

type endpointGroupProperties struct {
	port        int
	external    bool
	httpOnly    bool
	hasHttp2    bool
	exposedPort int
}

const (
	acaIngressSchemaTcp      string = "tcp"
	acaIngressSchemaHttp     string = "http"
	acaIngressSchemaHttps    string = "https"
	acaIngressTransportHttp2 string = "http2"
	acaIngressTransportHttp  string = "http"
	acaDefaultHttpPort       string = "80"
	acaDefaultHttpsPort      string = "443"
	// a target port that is resolved at deployment time
	acaTemplatedTargetPort string = "{{ targetPortOrDefault 0 }}"
)

func validateBindings(bindings custommaps.WithOrder[Binding]) error {
	for _, name := range bindings.OrderedKeys() {
		binding, found := bindings.Get(name)
		if !found {
			return fmt.Errorf("binding %q not found", name)
		}
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

// bindingWithOrder allows to move the key name and index from the binding to the group, so the name and order is not lost.
type bindingWithOrder struct {
	*Binding
	name  string
	order int
}

// groupBindingsByPort groups the bindings by the container port.
// bindings with no container port are grouped under the "default" key.
func groupBindingsByPort(bindings custommaps.WithOrder[Binding]) map[string][]*bindingWithOrder {
	endpointByContainerPort := make(map[string][]*bindingWithOrder)
	for order, name := range bindings.OrderedKeys() {
		binding, _ := bindings.Get(name)
		bindingKey := "default" // default is for those with no container port
		if binding.TargetPort != nil {
			bindingKey = fmt.Sprintf("%d", *binding.TargetPort)
		}
		endpointByContainerPort[bindingKey] = append(endpointByContainerPort[bindingKey], &bindingWithOrder{
			Binding: binding,
			name:    name,
			order:   order,
		})
	}
	return endpointByContainerPort
}

// groupProperties iterate over the bindings from a group and returns the properties of the group.
// Properties:
// port, external, httpOnly, hasHttp2
func groupProperties(endpointByTargetPort map[string][]*bindingWithOrder) map[string]*endpointGroupProperties {
	endpointByTargetPortProperties := make(map[string]*endpointGroupProperties)
	for containerPort, bindings := range endpointByTargetPort {
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
			if binding.Port != nil {
				props.exposedPort = *binding.Port
			}
		}
		endpointByTargetPortProperties[containerPort] = props
	}
	return endpointByTargetPortProperties
}

// validateExternalBindings check the there is not more than one binding group exported or if there are non-http bindings
// exported.
func validateExternalBindings(endpointByTargetPortProperties map[string]*acaPort) error {
	countExternalGroups := 0
	for _, props := range endpointByTargetPortProperties {
		if props.external {
			countExternalGroups++
		}
		if props.external && !props.httpOnly {
			return fmt.Errorf("External non-HTTP(s) endpoints are not supported")
		}
	}
	if countExternalGroups > 1 {
		return fmt.Errorf("Multiple external endpoints are not supported")
	}
	return nil
}

// endpointByTargetPortProperties try to find the right http ingress group to use.
// If there is just one http group, it will be used.
// If there are more than one http group, take the one which is external, and if there is no external one, take the group
// which contains the first http binding referenced in the manifest (the one with the lowest index key).
func httpIngress(endpointByTargetPortProperties map[string]*acaPort) string {
	var httpOnlyGroups []string
	for groupKey, props := range endpointByTargetPortProperties {
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
		for _, httpGroup := range httpOnlyGroups {
			if endpointByTargetPortProperties[httpGroup].external {
				externalHttpOnly = append(externalHttpOnly, httpGroup)
			}
		}

		if len(externalHttpOnly) == 1 {
			ingress = externalHttpOnly[0]
		} else if len(httpOnlyGroups) > 1 {
			minIndex := 0
			// iterate the groups which are http only.
			// then, update the ingress to the group name where we find the binding with the lowest index.
			for _, httpGroup := range httpOnlyGroups {
				port := endpointByTargetPortProperties[httpGroup]
				for _, binding := range port.bindings {
					if ingress == "" || binding.order < minIndex {
						minIndex = binding.order
						ingress = httpGroup
					}
				}
			}
		}
	}
	return ingress
}

type acaPort struct {
	*endpointGroupProperties
	bindings []*bindingWithOrder
}

func mapBindingsToPorts(bindings custommaps.WithOrder[Binding]) map[string]*acaPort {
	endpointByTargetPort := groupBindingsByPort(bindings)
	endpointByTargetPortProperties := groupProperties(endpointByTargetPort)

	mergedResult := make(map[string]*acaPort, len(endpointByTargetPort))
	for groupKey := range endpointByTargetPort {
		mergedResult[groupKey] = &acaPort{
			endpointGroupProperties: endpointByTargetPortProperties[groupKey],
			bindings:                endpointByTargetPort[groupKey],
		}
	}
	return mergedResult
}

func pickIngress(endpointByTargetPortProperties map[string]*acaPort, httpIngress string, defaultPort int) (
	*genContainerAppIngress, []string, error) {
	finalIngress := &genContainerAppIngress{}
	var bindingNamesFromIngress []string
	ingress := httpIngress
	if ingress != "" {
		props := endpointByTargetPortProperties[ingress]
		for _, binding := range props.bindings {
			if props.httpOnly && binding.Port != nil && binding.Scheme == acaIngressSchemaHttp && *binding.Port != 80 {
				// Main ingress http with non default port rule
				return nil, nil,
					fmt.Errorf(
						"Binding %s can't be mapped to main ingress because it has port %d defined. "+
							"main ingress only supports port 80 for http scheme.",
						binding.name, *binding.Port)
			}
			if props.httpOnly && binding.Port != nil && binding.Scheme == acaIngressSchemaHttps && *binding.Port != 443 {
				// Main ingress https with non default port rule
				return nil, nil,
					fmt.Errorf(
						"Binding %s can't be mapped to main ingress because it has port %d defined. "+
							"main ingress only supports port 443 for https scheme.",
						binding.name, *binding.Port)
			}
			bindingNamesFromIngress = append(bindingNamesFromIngress, binding.name)
		}

		finalIngress.External = props.external
		finalIngress.TargetPort = props.port
		if finalIngress.TargetPort == 0 {
			finalIngress.TargetPort = defaultPort
			finalIngress.UsingDefaultPort = true
		}
		finalIngress.Transport = acaIngressSchemaHttp
		if props.hasHttp2 {
			finalIngress.Transport = acaIngressTransportHttp2
		}
		if props.hasHttp2 || !props.external {
			finalIngress.AllowInsecure = true
		}

	} else {
		// ingress still empty b/c no http ingress. pick the first group with lowest index
		minIndex := 0
		for groupKey, group := range endpointByTargetPortProperties {
			for _, binding := range group.bindings {
				if ingress == "" || binding.order < minIndex {
					minIndex = binding.order
					ingress = groupKey
				}
			}
		}
		selectedIngress := endpointByTargetPortProperties[ingress]
		for _, binding := range selectedIngress.bindings {
			bindingNamesFromIngress = append(bindingNamesFromIngress, binding.name)
		}
		port := selectedIngress.port
		finalIngress.Transport = acaIngressSchemaTcp
		finalIngress.TargetPort = port
		finalIngress.ExposedPort = selectedIngress.exposedPort
	}

	for groupKey, props := range endpointByTargetPortProperties {
		if groupKey == ingress {
			continue
		}
		finalIngress.AdditionalPortMappings = append(finalIngress.AdditionalPortMappings,
			genContainerAppIngressAdditionalPortMappings{
				genContainerAppIngressPort: genContainerAppIngressPort{
					TargetPort:  props.port,
					ExposedPort: props.exposedPort,
				},
			})
	}

	return finalIngress, bindingNamesFromIngress, nil
}
