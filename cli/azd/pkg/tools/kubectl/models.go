package kubectl

type ResourceType string

const (
	ResourceTypeDeployment ResourceType = "deployment"
	ResourceTypeIngress    ResourceType = "ing"
	ResourceTypeService    ResourceType = "svc"
)

type Resource struct {
	ApiVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Metadata   ResourceMetadata `json:"metadata"`
}

type List[T any] struct {
	Resource
	Items []T `json:"items"`
}

type ResourceWithSpec[T any, S any] struct {
	Resource
	Spec   T `json:"spec"`
	Status S `json:"status"`
}

type ResourceMetadata struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	Annotations map[string]any
}

type Deployment ResourceWithSpec[DeploymentSpec, DeploymentStatus]

type DeploymentSpec struct {
	Replicas int `yaml:"replicas"`
}

type DeploymentStatus struct {
	AvailableReplicas int `yaml:"availableReplicas"`
	ReadyReplicas     int `yaml:"readyReplicas"`
	Replicas          int `yaml:"replicas"`
	UpdatedReplicas   int `yaml:"updatedReplicas"`
}

type Ingress ResourceWithSpec[IngressSpec, IngressStatus]

type IngressSpec struct {
	IngressClassName string `json:"ingressClassName"`
	Tls              *IngressTls
	Rules            []IngressRule
}

type IngressTls struct {
	Hosts      []string `yaml:"hosts"`
	SecretName string   `yaml:"secretName"`
}

type IngressRule struct {
	Host *string         `yaml:"host"`
	Http IngressRuleHttp `yaml:"http"`
}

type IngressRuleHttp struct {
	Paths []IngressPath `yaml:"paths"`
}

type IngressPath struct {
	Path     string `yaml:"path"`
	PathType string `yaml:"pathType"`
}

type IngressStatus struct {
	LoadBalancer LoadBalancer `json:"loadBalancer"`
}

type LoadBalancer struct {
	Ingress []LoadBalancerIngress `json:"ingress"`
}

type LoadBalancerIngress struct {
	Ip string `json:"ip"`
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
	Type       ServiceType `json:"type"`
	ClusterIp  string      `json:"clusterIP"`
	ClusterIps []string    `json:"clusterIPs"`
	Ports      []Port      `json:"ports"`
}

type ServiceStatus struct {
	LoadBalancer LoadBalancer `json:"loadBalancer"`
}

type Port struct {
	Port       int    `json:"port"`
	TargetPort int    `json:"targetPort"`
	Protocol   string `json:"protocol"`
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
	Cluster string `yaml:"cluster"`
	User    string `yaml:"user"`
}

type KubeUser struct {
	Name         string       `yaml:"name"`
	KubeUserData KubeUserData `yaml:"user"`
}

type KubeUserData map[string]any
type KubePreferences map[string]any
