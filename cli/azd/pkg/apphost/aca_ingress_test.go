package apphost

import (
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/custommaps"
	"github.com/stretchr/testify/assert"
)

func TestBuildAcaIngress(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		ingress, ingressBinding, err := buildAcaIngress(custommaps.WithOrder[Binding]{}, 8080)
		assert.NoError(t, err)
		assert.Nil(t, ingress)
		assert.Nil(t, ingressBinding)
	})

	t.Run("common case", func(t *testing.T) {
		bindingsManifest := `{
			"http": {
				"scheme":    "http",
				"transport": "http",
				"protocol":  "http"
			},
			"https": {
				"scheme":    "https",
				"transport": "http2",
				"protocol":  "http"
			}}`

		var bindings custommaps.WithOrder[Binding]
		err := json.Unmarshal([]byte(bindingsManifest), &bindings)
		assert.NoError(t, err)

		expectedIngress := &genContainerAppIngress{
			genContainerAppIngressPort: genContainerAppIngressPort{
				External:   false,
				TargetPort: 8080,
			},
			Transport:              acaIngressTransportHttp2,
			AllowInsecure:          true,
			AdditionalPortMappings: []genContainerAppIngressAdditionalPortMappings(nil),
			UsingDefaultPort:       true,
		}
		ingress, ingressBinding, err := buildAcaIngress(bindings, 8080)
		assert.NoError(t, err)
		assert.Equal(t, expectedIngress, ingress)
		assert.Equal(t, []string{"http", "https"}, ingressBinding)
	})

	t.Run("multi external ports", func(t *testing.T) {
		bindingsManifest := `{
			"http": {
				"external": true,
				"scheme":    "http",
				"transport": "http2"
			},
			"https": {
				"external": true,
				"scheme":    "https",
				"transport": "http2"
			},
			"other": {
				"TargetPort": 90,
				"external": true,
				"scheme":    "https",
				"transport": "http2"
			}}`

		var bindings custommaps.WithOrder[Binding]
		err := json.Unmarshal([]byte(bindingsManifest), &bindings)
		assert.NoError(t, err)

		ingress, ingressBinding, err := buildAcaIngress(bindings, 8080)
		assert.Error(t, err)
		assert.EqualError(t, err, "Multiple external endpoints are not supported")
		assert.Nil(t, ingress)
		assert.Equal(t, []string(nil), ingressBinding)
	})

	t.Run("external non-HTTP(s) endpoints", func(t *testing.T) {
		bindingsManifest := `{
			"http": {
				"external": true,
				"scheme":    "tcp",
				"targetPort": 99
			},
			"https": {
				"scheme":    "tcp",
				"targetPort": 33
			},
			"other": {
				"TargetPort": 10,
				"scheme":    "tcp"
			}}`

		var bindings custommaps.WithOrder[Binding]
		err := json.Unmarshal([]byte(bindingsManifest), &bindings)
		assert.NoError(t, err)

		ingress, ingressBinding, err := buildAcaIngress(bindings, 8080)
		assert.Error(t, err)
		assert.EqualError(t, err, "External non-HTTP(s) endpoints are not supported")
		assert.Nil(t, ingress)
		assert.Equal(t, []string(nil), ingressBinding)
	})

	t.Run("no http", func(t *testing.T) {
		bindingsManifest := `{
			"http": {
				"scheme":    "tcp",
				"targetPort": 33
			}}`

		var bindings custommaps.WithOrder[Binding]
		err := json.Unmarshal([]byte(bindingsManifest), &bindings)
		assert.NoError(t, err)

		expectedIngress := &genContainerAppIngress{
			genContainerAppIngressPort: genContainerAppIngressPort{
				External:   false,
				TargetPort: 33,
			},
			Transport:              acaIngressSchemaTcp,
			AdditionalPortMappings: []genContainerAppIngressAdditionalPortMappings(nil),
		}
		ingress, ingressBinding, err := buildAcaIngress(bindings, 8080)
		assert.NoError(t, err)
		assert.Equal(t, expectedIngress, ingress)
		assert.Equal(t, []string{"http"}, ingressBinding)
	})

	t.Run("invalid schema", func(t *testing.T) {
		bindingsManifest := `{
			"http": {
				"scheme":    "invalid",
				"targetPort": 33
			}}`

		var bindings custommaps.WithOrder[Binding]
		err := json.Unmarshal([]byte(bindingsManifest), &bindings)
		assert.NoError(t, err)

		ingress, ingressBinding, err := buildAcaIngress(bindings, 8080)
		assert.Error(t, err)
		assert.EqualError(t, err, `binding "http" has invalid scheme "invalid"`)
		assert.Nil(t, ingress)
		assert.Equal(t, []string(nil), ingressBinding)
	})

	t.Run("additional ports", func(t *testing.T) {
		bindingsManifest := `{
			"http": {
				"scheme":    "tcp",
				"targetPort": 33
			},
			"https": {
				"scheme":    "http",
				"external": true
			}}`

		var bindings custommaps.WithOrder[Binding]
		err := json.Unmarshal([]byte(bindingsManifest), &bindings)
		assert.NoError(t, err)

		expectedIngress := &genContainerAppIngress{
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
			UsingDefaultPort: true,
		}
		ingress, ingressBinding, err := buildAcaIngress(bindings, 8080)
		assert.NoError(t, err)
		assert.Equal(t, expectedIngress, ingress)
		assert.Equal(t, []string{"https"}, ingressBinding)
	})

	t.Run("tcp with no port", func(t *testing.T) {
		bindingsManifest := `{
			"a": {
				"scheme":    "tcp"
			}}`

		var bindings custommaps.WithOrder[Binding]
		err := json.Unmarshal([]byte(bindingsManifest), &bindings)
		assert.NoError(t, err)

		ingress, ingressBinding, err := buildAcaIngress(bindings, 8080)
		assert.Error(t, err)
		assert.EqualError(t, err, `binding "a" has scheme "tcp" but no container port`)
		assert.Nil(t, ingress)
		assert.Equal(t, []string(nil), ingressBinding)
	})
}
