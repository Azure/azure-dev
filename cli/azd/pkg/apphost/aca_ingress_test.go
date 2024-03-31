package apphost

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/stretchr/testify/assert"
)

func TestBuildAcaIngress(t *testing.T) {
	// Test case 1: Empty bindings
	ingress, err := buildAcaIngress(map[WithIndexKey]*Binding{}, 8080)
	assert.NoError(t, err)
	assert.Nil(t, ingress)

	// Test case 2: Common project
	bindings := map[WithIndexKey]*Binding{
		{string: "http", Index: 0}: {
			Scheme:    acaIngressSchemaHttp,
			Transport: acaIngressTransportHttp,
			Protocol:  acaIngressProtocolHttp,
		},
		{string: "https", Index: 1}: {
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
	bindings = map[WithIndexKey]*Binding{
		{string: "80", Index: 0}: {
			External:  true,
			Scheme:    acaIngressSchemaHttp,
			Transport: acaIngressTransportHttp2,
		},
		{string: "443", Index: 1}: {
			External:  true,
			Scheme:    acaIngressSchemaHttps,
			Transport: acaIngressTransportHttp2,
		},
		{string: "8080", Index: 2}: {
			TargetPort: to.Ptr(90),
			External:   true,
			Scheme:     acaIngressSchemaHttp,
			Transport:  acaIngressTransportHttp2,
		},
	}
	ingress, err = buildAcaIngress(bindings, 8080)
	assert.Error(t, err)
	assert.EqualError(t, err, "Multiple external endpoints are not supported")
	assert.Nil(t, ingress)

	// Test case 6: external non-HTTP(s) endpoints
	bindings = map[WithIndexKey]*Binding{
		{string: "a", Index: 0}: {
			Scheme:     acaIngressSchemaTcp,
			TargetPort: to.Ptr(99),
			External:   true,
		},
		{string: "b", Index: 0}: {
			Scheme:     acaIngressSchemaTcp,
			TargetPort: to.Ptr(33),
		},
		{string: "c", Index: 1}: {
			TargetPort: to.Ptr(10),
			Scheme:     acaIngressSchemaTcp,
		},
	}
	ingress, err = buildAcaIngress(bindings, 8080)
	assert.Error(t, err)
	assert.EqualError(t, err, "External non-HTTP(s) endpoints are not supported")
	assert.Nil(t, ingress)

	// Test case 7: no http
	bindings = map[WithIndexKey]*Binding{
		{string: "a", Index: 0}: {
			Scheme:     acaIngressSchemaTcp,
			TargetPort: to.Ptr(33),
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
	bindings = map[WithIndexKey]*Binding{{string: "a", Index: 0}: nil}
	ingress, err = buildAcaIngress(bindings, 8080)
	assert.Error(t, err)
	assert.EqualError(t, err, `binding "a" is empty`)
	assert.Nil(t, ingress)

	// Test case 9: invalid schema
	bindings = map[WithIndexKey]*Binding{{string: "a", Index: 0}: {Scheme: "invalid"}}
	ingress, err = buildAcaIngress(bindings, 8080)
	assert.Error(t, err)
	assert.EqualError(t, err, `binding "a" has invalid scheme "invalid"`)
	assert.Nil(t, ingress)

	// Test case 10: additional
	bindings = map[WithIndexKey]*Binding{
		{string: "a", Index: 0}: {
			Scheme:     acaIngressSchemaTcp,
			TargetPort: to.Ptr(33),
		},
		{string: "i", Index: 1}: {
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

	// Test case 11: tcp with no port
	bindings = map[WithIndexKey]*Binding{{string: "a", Index: 0}: {Scheme: acaIngressSchemaTcp}}
	ingress, err = buildAcaIngress(bindings, 8080)
	assert.Error(t, err)
	assert.EqualError(t, err, `binding "a" has scheme "tcp" but no container port`)
	assert.Nil(t, ingress)
}
