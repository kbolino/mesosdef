package model

type OptionalFloat32 struct {
	raw float32
	set bool
}

type DeployAppRequest struct {
	AppDefinition
}

type AppDefinition struct {
	ID                         string                 `json:"id"`
	AcceptedResourceRoles      []string               `json:"acceptedResourceRoles"`
	Args                       []string               `json:"args"`
	Cmd                        string                 `json:"cmd"`
	Constraints                [][]string             `json:"constraints"`
	Container                  *Container             `json:"container"`
	CPUs                       float32                `json:"cpus"`
	Disk                       float32                `json:"disk"`
	Env                        map[string]interface{} `json:"env"`
	Fetch                      []FetchArtifact        `json:"fetch"`
	GPUs                       float32                `json:"gpus"`
	HealthChecks               []HealthCheck          `json:"healthChecks"`
	Instances                  int32                  `json:"instances"`
	Labels                     map[string]string      `json:"labels"`
	Mem                        float32                `json:"mem"`
	Networks                   []Network              `json:"networks"`
	PortDefinitions            []PortDefinition       `json:"portDefinitions"`
	Ports                      []int32                `json:"port"`
	RequirePorts               bool                   `json:"requirePorts"`
	TaskKillGradePeriodSeconds int32                  `json:"taskKillGracePeriodSeconds"`
	UnreachableStrategy        *UnreachableStrategy   `json:"unreachableStrategy"`
	UpgradeStrategy            *UpgradeStrategy       `json:"upgradeStrategy"`
	URIs                       []string               `json:"uris"`
	User                       string                 `json:"user"`
	Version                    string                 `json:"version"`
	VersionInfo                *VersionInfo           `json:"versionInfo"`
}

type Container struct {
	Docker  *Docker  `json:"docker"`
	Type    string   `json:"type"`
	Volumes []Volume `json:"volumes"`
}

type Docker struct {
	ForcePullImage bool              `json:"forcePullImage"`
	Image          string            `json:"image"`
	Network        string            `json:"network"`
	Parameters     []DockerParameter `json:"parameters"`
	PortMappings   []PortMapping     `json:"portMappings"`
}

type DockerParameter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type HealthCheck struct {
	GracePeriodSeconds      int32   `json:"gracePeriodSeconds"`
	HTTPStatusCodesForReady []int32 `json:"httpStatusCodesForReady"`
	IgnoreHttp1XX           bool    `json:"ignoreHttp1xx`
	IntervalSeconds         int32   `json:"intervalSeconds"`
	MaxConsecutiveFailures  int32   `json:"maxConsecutiveFailures"`
	Path                    string  `json:"path"`
	PortIndex               int32   `json:"portIndex"`
	PortName                string  `json:"portName"`
	PreserveLastResponse    bool    `json:"preserveLastResponse"`
	Protocol                string  `json:"protocol"`
	TimeoutSeconds          int32   `json:"timeoutSeconds"`
}

type FetchArtifact struct {
	Cache      bool   `json:"cache"`
	DestPath   string `json:"destPath"`
	Executable bool   `json:"executable"`
	Extract    bool   `json:"extract"`
	URI        string `json:"uri"`
}

type Network struct {
	Mode string `json:"mode"`
	Name string `json:"name"`
}

type PortDefinition struct {
	Labels   map[string]string `json:"labels"`
	Name     string            `json:"name"`
	Port     int32             `json:"port"`
	Protocol string            `json:"string"`
}

type PortMapping struct {
	ContainerPort int32  `json:"containerPort"`
	HostPort      int32  `json:"hostPort"`
	Protocol      string `json:"protocol"`
	ServicePort   int32  `json:"servicePort"`
}

type UnreachableStrategy struct {
	ExpungeAfterSeconds  int32 `json:"expungeAfterSeconds"`
	InactiveAfterSeconds int32 `json:"inactiveAfterSeconds"`
}

type UpgradeStrategy struct {
	MaximumOverCapacity   float32 `json:"maximumOverCapacity"`
	MinimumHealthCapacity float32 `json:"minimumHealthCapacity"`
}

type VersionInfo struct {
	LastConfigChangeAt string `json:"lastConfigChangeAt"`
	LastScalingAt      string `json:"lastScalingAt"`
}

type Volume struct {
	ContainerPath string `json:"containerPath"`
	HostPath      string `json:"hostPath"`
	Mode          string `json:"mode"`
}

type DeployAppResponse struct {
	DeploymentID string
	Version      string
}
