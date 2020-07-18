package main

type Root struct {
	Mesos       *Mesos       `hcl:"mesos,block"`
	Frameworks  []Framework  `hcl:"framework,block"`
	Deployments []Deployment `hcl:"deployment,block"`
}

type Mesos struct {
	Zookeepers string   `hcl:"zookeepers,attr"`
	Masters    []string `hcl:"masters,attr"`
}

type Framework struct {
	Type                string         `hcl:"type,label"`
	Name                string         `hcl:"name,label"`
	MesosName           string         `hcl:"mesos_name,attr"`
	Masters             []string       `hcl:"masters,attr"`
	CreatedByDeployment *DeploymentRef `hcl:"created_by_deployment,block"`
}

type DeploymentRef struct {
	Type string `hcl:"type,attr"`
	Name string `hcl:"name,attr"`
}

type Deployment struct {
	Type         string          `hcl:"type,label"`
	Name         string          `hcl:"name,label"`
	Framework    string          `hcl:"framework,optional"`
	Deploy       string          `hcl:"deploy,attr"`
	Labels       []string        `hcl:"labels,optional"`
	Dependencies []DependencyRef `hcl:"dependency,block"`
	DependencyOf []DependencyRef `hcl:"dependency_of,block"`
}

type DependencyRef struct {
	Type           string   `hcl:"type,attr"`
	Name           string   `hcl:"name,optional"`
	WaitForHealthy bool     `hcl:"wait_for_healthy,optional"`
	Filters        []Filter `hcl:"filter,block"`
}

type Filter struct {
	Key    string   `hcl:"key,attr"`
	Value  string   `hcl:"value,optional"`
	Values []string `hcl:"values,optional"`
	Glob   bool     `hcl:"glob,optional"`
	Regexp bool     `hcl:"regexp,optional"`
	Negate bool     `hcl:"negate,optional"`
}
