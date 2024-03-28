package apphost

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/stretchr/testify/assert"
)

func TestBuildAcaIngress(t *testing.T) {
	// Test case 1: Empty bindings
	ingress, err := buildAcaIngress(map[string]*Binding{}, 8080)
	assert.NoError(t, err)
	assert.Nil(t, ingress)

	// Test case 2: Common project
	bindings := map[string]*Binding{
		"http": {
			Scheme:    acaIngressSchemaHttp,
			Transport: acaIngressTransportHttp,
			Protocol:  acaIngressProtocolHttp,
		},
		"https": {
			Scheme:    acaIngressSchemaHttps,
			Transport: acaIngressTransportHttp2,
			Protocol:  acaIngressProtocolHttp,
		},
	}
	expectedIngress := &genContainerAppIngress{
		genContainerAppIngressPort: genContainerAppIngressPort{
			External:   false,
			TargetPort: 8080,
		},
		Transport:              acaIngressTransportHttp2,
		AllowInsecure:          true,
		AdditionalPortMappings: []genContainerAppIngressAdditionalPortMappings(nil),
	}
	ingress, err = buildAcaIngress(bindings, 8080)
	assert.NoError(t, err)
	assert.Equal(t, expectedIngress, ingress)

	// Test case 3: Multiple external endpoints
	bindings = map[string]*Binding{
		"80": {
			External:  true,
			Scheme:    acaIngressSchemaHttp,
			Transport: acaIngressTransportHttp2,
		},
		"443": {
			External:  true,
			Scheme:    acaIngressSchemaHttps,
			Transport: acaIngressTransportHttp2,
		},
		"8080": {
			ContainerPort: to.Ptr(90),
			External:      true,
			Scheme:        acaIngressSchemaHttp,
			Transport:     acaIngressTransportHttp2,
		},
	}
	ingress, err = buildAcaIngress(bindings, 8080)
	assert.Error(t, err)
	assert.EqualError(t, err, "Multiple external endpoints are not supported")
	assert.Nil(t, ingress)

	// Test case 4: Multiple internal only HTTP(s) endpoints
	bindings = map[string]*Binding{
		"http": {
			Scheme:    acaIngressSchemaHttp,
			Transport: acaIngressTransportHttp2,
		},
		"https": {
			Scheme:    acaIngressSchemaHttps,
			Transport: acaIngressTransportHttp2,
		},
		"other": {
			ContainerPort: to.Ptr(90),
			Scheme:        acaIngressSchemaHttp,
			Transport:     acaIngressTransportHttp2,
		},
	}
	ingress, err = buildAcaIngress(bindings, 8080)
	assert.Error(t, err)
	assert.EqualError(t, err, "Multiple internal only HTTP(s) endpoints are not supported")
	assert.Nil(t, ingress)

	// Test case 5: More than 5 additional ports
	bindings = map[string]*Binding{
		"80": {
			ContainerPort: to.Ptr(80),
			External:      false,
			Scheme:        acaIngressSchemaHttp,
			Transport:     acaIngressTransportHttp2,
		},
		"443": {
			ContainerPort: to.Ptr(443),
			External:      false,
			Scheme:        acaIngressSchemaHttps,
			Transport:     acaIngressTransportHttp2,
		},
		"8080": {
			ContainerPort: to.Ptr(8080),
			External:      true,
			Scheme:        acaIngressSchemaHttp,
			Transport:     acaIngressTransportHttp2,
		},
		"8081": {
			ContainerPort: to.Ptr(8081),
			External:      false,
			Scheme:        acaIngressSchemaHttp,
			Transport:     acaIngressTransportHttp2,
		},
		"8082": {
			ContainerPort: to.Ptr(8082),
			External:      false,
			Scheme:        acaIngressSchemaHttp,
			Transport:     acaIngressTransportHttp2,
		},
		"8083": {
			ContainerPort: to.Ptr(8083),
			External:      false,
			Scheme:        acaIngressSchemaHttp,
			Transport:     acaIngressTransportHttp2,
		},
		"8084": {
			ContainerPort: to.Ptr(8084),
			External:      false,
			Scheme:        acaIngressSchemaHttp,
			Transport:     acaIngressTransportHttp2,
		},
	}
	ingress, err = buildAcaIngress(bindings, 8080)
	assert.Error(t, err)
	assert.EqualError(t, err, "More than 5 additional ports are not supported. "+
		"See https://learn.microsoft.com/en-us/azure/container-apps/ingress-overview#tcp for more details.")
	assert.Nil(t, ingress)

	// Test case 6: external non-HTTP(s) endpoints
	bindings = map[string]*Binding{
		"a": {
			Scheme:   acaIngressSchemaTcp,
			External: true,
		},
		"b": {
			Scheme:        acaIngressSchemaTcp,
			ContainerPort: to.Ptr(33),
		},
		"c": {
			ContainerPort: to.Ptr(10),
			Scheme:        acaIngressSchemaTcp,
		},
	}
	ingress, err = buildAcaIngress(bindings, 8080)
	assert.Error(t, err)
	assert.EqualError(t, err, "External non-HTTP(s) endpoints are not supported")
	assert.Nil(t, ingress)

	// Test case 7: no http
	bindings = map[string]*Binding{
		"a": {
			Scheme:        acaIngressSchemaTcp,
			ContainerPort: to.Ptr(33),
		},
	}
	expectedIngress = &genContainerAppIngress{
		genContainerAppIngressPort: genContainerAppIngressPort{
			External:   false,
			TargetPort: 33,
		},
		Transport:              acaIngressSchemaTcp,
		AdditionalPortMappings: []genContainerAppIngressAdditionalPortMappings(nil),
	}
	ingress, err = buildAcaIngress(bindings, 8080)
	assert.NoError(t, err)
	assert.Equal(t, expectedIngress, ingress)

	// Test case 8: empty
	bindings = map[string]*Binding{"a": nil}
	ingress, err = buildAcaIngress(bindings, 8080)
	assert.Error(t, err)
	assert.EqualError(t, err, `binding "a" is empty`)
	assert.Nil(t, ingress)

	// Test case 9: invalid schema
	bindings = map[string]*Binding{"a": {Scheme: "invalid"}}
	ingress, err = buildAcaIngress(bindings, 8080)
	assert.Error(t, err)
	assert.EqualError(t, err, `binding "a" has invalid scheme "invalid"`)
	assert.Nil(t, ingress)

	// Test case 10: additional
	bindings = map[string]*Binding{
		"a": {
			Scheme:        acaIngressSchemaTcp,
			ContainerPort: to.Ptr(33),
		},
		"i": {
			Scheme:   acaIngressSchemaHttp,
			External: true,
		},
	}
	expectedIngress = &genContainerAppIngress{
		genContainerAppIngressPort: genContainerAppIngressPort{
			External:   true,
			TargetPort: 8080,
		},
		Transport: acaIngressSchemaHttp,
		AdditionalPortMappings: []genContainerAppIngressAdditionalPortMappings{
			{
				genContainerAppIngressPort: genContainerAppIngressPort{
					TargetPort: 33,
				},
			},
		},
	}
	ingress, err = buildAcaIngress(bindings, 8080)
	assert.NoError(t, err)
	assert.Equal(t, expectedIngress, ingress)
}
