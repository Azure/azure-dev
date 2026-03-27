// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package kubectl

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Port_UnmarshalJSON_Float64TargetPort(t *testing.T) {
	input := `{"port":443,"targetPort":8443.0,"protocol":"TCP"}`
	var port Port
	err := json.Unmarshal([]byte(input), &port)
	require.NoError(t, err)
	require.Equal(t, 443, port.Port)
	require.Equal(t, "TCP", port.Protocol)
	// JSON numbers without explicit int type deserialize
	// as float64 in Go.
	require.Equal(t, float64(8443), port.TargetPort)
}

func Test_Port_UnmarshalJSON_NilTargetPort(t *testing.T) {
	input := `{"port":80,"targetPort":null,"protocol":"HTTP"}`
	var port Port
	err := json.Unmarshal([]byte(input), &port)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported type")
}

func Test_Port_UnmarshalJSON_InvalidJSON(t *testing.T) {
	input := `{"port":}`
	var port Port
	err := json.Unmarshal([]byte(input), &port)
	require.Error(t, err)
}

func Test_Deployment_JsonRoundTrip(t *testing.T) {
	dep := Deployment{
		Resource: Resource{
			ApiVersion: "apps/v1",
			Kind:       "Deployment",
			Metadata: ResourceMetadata{
				Name:      "web",
				Namespace: "prod",
			},
		},
		Spec: DeploymentSpec{Replicas: 3},
		Status: DeploymentStatus{
			AvailableReplicas: 3,
			ReadyReplicas:     3,
			Replicas:          3,
			UpdatedReplicas:   3,
		},
	}
	data, err := json.Marshal(dep)
	require.NoError(t, err)

	var restored Deployment
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)
	require.Equal(t, "web", restored.Metadata.Name)
	require.Equal(t, 3, restored.Spec.Replicas)
	require.Equal(t, 3, restored.Status.AvailableReplicas)
}

func Test_Service_JsonRoundTrip(t *testing.T) {
	svc := Service{
		Resource: Resource{
			ApiVersion: "v1",
			Kind:       "Service",
			Metadata: ResourceMetadata{
				Name:      "api",
				Namespace: "staging",
			},
		},
		Spec: ServiceSpec{
			Type:       ServiceTypeLoadBalancer,
			ClusterIp:  "10.0.0.5",
			ClusterIps: []string{"10.0.0.5"},
			Ports: []Port{
				{Port: 80, TargetPort: "http", Protocol: "TCP"},
			},
		},
		Status: ServiceStatus{
			LoadBalancer: LoadBalancer{
				Ingress: []LoadBalancerIngress{
					{Ip: "52.1.2.3"},
				},
			},
		},
	}
	data, err := json.Marshal(svc)
	require.NoError(t, err)

	var restored Service
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)
	require.Equal(t, ServiceTypeLoadBalancer, restored.Spec.Type)
	require.Equal(t, "52.1.2.3",
		restored.Status.LoadBalancer.Ingress[0].Ip,
	)
}

func Test_List_JsonRoundTrip(t *testing.T) {
	list := List[Deployment]{
		Resource: Resource{
			ApiVersion: "v1",
			Kind:       "DeploymentList",
		},
		Items: []Deployment{
			{
				Resource: Resource{
					Metadata: ResourceMetadata{Name: "a"},
				},
				Spec: DeploymentSpec{Replicas: 1},
			},
			{
				Resource: Resource{
					Metadata: ResourceMetadata{Name: "b"},
				},
				Spec: DeploymentSpec{Replicas: 2},
			},
		},
	}
	data, err := json.Marshal(list)
	require.NoError(t, err)

	var restored List[Deployment]
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)
	require.Len(t, restored.Items, 2)
	require.Equal(t, "a", restored.Items[0].Metadata.Name)
	require.Equal(t, "b", restored.Items[1].Metadata.Name)
}

func Test_ResourceType_Constants(t *testing.T) {
	require.Equal(t, ResourceType("deployment"),
		ResourceTypeDeployment)
	require.Equal(t, ResourceType("ing"),
		ResourceTypeIngress)
	require.Equal(t, ResourceType("svc"),
		ResourceTypeService)
}

func Test_ServiceType_Constants(t *testing.T) {
	require.Equal(t, ServiceType("ClusterIP"),
		ServiceTypeClusterIp)
	require.Equal(t, ServiceType("LoadBalancer"),
		ServiceTypeLoadBalancer)
	require.Equal(t, ServiceType("NodePort"),
		ServiceTypeNodePort)
	require.Equal(t, ServiceType("ExternalName"),
		ServiceTypeExternalName)
}

func Test_OutputType_Constants(t *testing.T) {
	require.Equal(t, OutputType("json"), OutputTypeJson)
	require.Equal(t, OutputType("yaml"), OutputTypeYaml)
}

func Test_DryRunType_Constants(t *testing.T) {
	require.Equal(t, DryRunType("none"), DryRunTypeNone)
	require.Equal(t, DryRunType("client"), DryRunTypeClient)
	require.Equal(t, DryRunType("server"), DryRunTypeServer)
}

func Test_KubeConfigEnvVarName(t *testing.T) {
	require.Equal(t, "KUBECONFIG", KubeConfigEnvVarName)
}

func Test_ResourceMetadata_Annotations(t *testing.T) {
	jsonStr := `{
		"name":"test","namespace":"ns",
		"Annotations":{"key":"value"}
	}`
	var meta ResourceMetadata
	err := json.Unmarshal([]byte(jsonStr), &meta)
	require.NoError(t, err)
	require.Equal(t, "test", meta.Name)
	require.Equal(t, "value", meta.Annotations["key"])
}

func Test_Ingress_JsonRoundTrip(t *testing.T) {
	host := "example.com"
	ing := Ingress{
		Resource: Resource{
			ApiVersion: "networking.k8s.io/v1",
			Kind:       "Ingress",
			Metadata: ResourceMetadata{
				Name: "my-ingress",
			},
		},
		Spec: IngressSpec{
			IngressClassName: "nginx",
			Tls: []IngressTls{{
				Hosts:      []string{"example.com"},
				SecretName: "tls-secret",
			}},
			Rules: []IngressRule{{
				Host: &host,
				Http: IngressRuleHttp{
					Paths: []IngressPath{{
						Path:     "/",
						PathType: "Prefix",
					}},
				},
			}},
		},
		Status: IngressStatus{
			LoadBalancer: LoadBalancer{
				Ingress: []LoadBalancerIngress{
					{Ip: "10.0.0.1"},
				},
			},
		},
	}
	data, err := json.Marshal(ing)
	require.NoError(t, err)

	var restored Ingress
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)
	require.Equal(t, "nginx", restored.Spec.IngressClassName)
	require.Equal(t, "example.com",
		*restored.Spec.Rules[0].Host,
	)
	require.Equal(t, "tls-secret",
		restored.Spec.Tls[0].SecretName,
	)
	require.Equal(t, "/",
		restored.Spec.Rules[0].Http.Paths[0].Path,
	)
	require.Equal(t, "10.0.0.1",
		restored.Status.LoadBalancer.Ingress[0].Ip,
	)
}

func Test_IngressRule_NilHost(t *testing.T) {
	rule := IngressRule{
		Host: nil,
		Http: IngressRuleHttp{
			Paths: []IngressPath{{Path: "/api"}},
		},
	}
	data, err := json.Marshal(rule)
	require.NoError(t, err)

	var restored IngressRule
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)
	require.Nil(t, restored.Host)
}
