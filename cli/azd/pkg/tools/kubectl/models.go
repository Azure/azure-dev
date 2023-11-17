package kubectl

import (
	"encoding/json"
	"fmt"
)

type ResourceType string

const (
	ResourceTypeDeployment ResourceType = "deployment"
	ResourceTypeIngress    ResourceType = "ing"
	ResourceTypeService    ResourceType = "svc"
	KubeConfigEnvVarName   string       = "KUBECONFIG"
)

type Resource struct {
	ApiVersion string           `json:"apiVersion" yaml:"apiVersion"`
	Kind       string           `json:"kind"       yaml:"kind"`
	Metadata   ResourceMetadata `json:"metadata"   yaml:"metadata"`
}

type List[T any] struct {
	Resource
	Items []T `json:"items" yaml:"items"`
}

type ResourceWithSpec[T any, S any] struct {
	Resource
	Spec   T `json:"spec"   yaml:"spec"`
	Status S `json:"status" yaml:"status"`
}

type ResourceMetadata struct {
	Name        string `json:"name"      yaml:"name"`
	Namespace   string `json:"namespace" yaml:"namespace"`
	Annotations map[string]any
}

type Deployment ResourceWithSpec[DeploymentSpec, DeploymentStatus]

type DeploymentSpec struct {
	Replicas int `json:"replicas" yaml:"replicas"`
}

type DeploymentStatus struct {
	AvailableReplicas int `json:"availableReplicas" yaml:"availableReplicas"`
	ReadyReplicas     int `json:"readyReplicas"     yaml:"readyReplicas"`
	Replicas          int `json:"replicas"          yaml:"replicas"`
	UpdatedReplicas   int `json:"updatedReplicas"   yaml:"updatedReplicas"`
}

type Ingress ResourceWithSpec[IngressSpec, IngressStatus]

type IngressSpec struct {
	IngressClassName string        `json:"ingressClassName" yaml:"ingressClassName"`
	Tls              []IngressTls  `json:"tls"              yaml:"tls"`
	Rules            []IngressRule `json:"rules"            yaml:"rules"`
}

type IngressTls struct {
	Hosts      []string `json:"hosts"      yaml:"hosts"`
	SecretName string   `json:"secretName" yaml:"secretName"`
}

type IngressRule struct {
	Host *string         `json:"host" yaml:"host"`
	Http IngressRuleHttp `json:"http" yaml:"http"`
}

type IngressRuleHttp struct {
	Paths []IngressPath `json:"paths" yaml:"paths"`
}

type IngressPath struct {
	Path     string `json:"path"     yaml:"path"`
	PathType string `json:"pathType" yaml:"pathType"`
}

type IngressStatus struct {
	LoadBalancer LoadBalancer `json:"loadBalancer" yaml:"loadBalancer"`
}

type LoadBalancer struct {
	Ingress []LoadBalancerIngress `json:"ingress" yaml:"ingress"`
}

type LoadBalancerIngress struct {
	Ip string `json:"ip" yaml:"ip"`
}

type Service ResourceWithSpec[ServiceSpec, ServiceStatus]

type ServiceType string

const (
	ServiceTypeClusterIp    ServiceType = "ClusterIP"
	ServiceTypeLoadBalancer ServiceType = "LoadBalancer"
	ServiceTypeNodePort     ServiceType = "NodePort"
	ServiceTypeExternalName ServiceType = "ExternalName"
)

type ServiceSpec struct {
	Type       ServiceType `json:"type"       yaml:"type"`
	ClusterIp  string      `json:"clusterIP"  yaml:"clusterIP"`
	ClusterIps []string    `json:"clusterIPs" yaml:"clusterIPs"`
	Ports      []Port      `json:"ports"      yaml:"ports"`
}

type ServiceStatus struct {
	LoadBalancer LoadBalancer `json:"loadBalancer" yaml:"loadBalancer"`
}

type Port struct {
	Port int `json:"port"`
	// The target port can be a valid port number or well known service name like 'redis'
	TargetPort any    `json:"targetPort" yaml:"targetPort"`
	Protocol   string `json:"protocol"   yaml:"protocol"`
}

func (p *Port) UnmarshalJSON(data []byte) error {
	var aux struct {
		Port       int    `json:"port" yaml:"port"`
		TargetPort any    `json:"targetPort" yaml:"targetPort"`
		Protocol   string `json:"protocol" yaml:"protocol"`
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	p.Port = aux.Port
	p.Protocol = aux.Protocol

	switch v := aux.TargetPort.(type) {
	case string, int, float64:
		p.TargetPort = v
		return nil
	default:
		return fmt.Errorf("unsupported type for TargetPort")
	}
}

type KubeConfig struct {
	ApiVersion     string          `yaml:"apiVersion"`
	Clusters       []*KubeCluster  `yaml:"clusters"`
	Contexts       []*KubeContext  `yaml:"contexts"`
	Users          []*KubeUser     `yaml:"users"`
	Kind           string          `yaml:"kind"`
	CurrentContext string          `yaml:"current-context"`
	Preferences    KubePreferences `yaml:"preferences"`
}

type KubeCluster struct {
	Name    string          `yaml:"name"`
	Cluster KubeClusterData `yaml:"cluster"`
}

type KubeClusterData struct {
	CertificateAuthorityData string `yaml:"certificate-authority-data"`
	Server                   string `yaml:"server"`
}

type KubeContext struct {
	Name    string          `yaml:"name"`
	Context KubeContextData `yaml:"context"`
}

type KubeContextData struct {
	Cluster   string `yaml:"cluster"`
	Namespace string `yaml:"namespace"`
	User      string `yaml:"user"`
}

type KubeUser struct {
	Name         string       `yaml:"name"`
	KubeUserData KubeUserData `yaml:"user"`
}

type KubeUserData map[string]any
type KubePreferences map[string]any
