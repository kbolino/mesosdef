package model

import (
	"regexp"
)

var regexpValidIdentifier = regexp.MustCompile("^[a-zA-Z_][a-zA-Z0-9_]*$")

// IsValidIdentifier returns true if and only if s is a valid identifier.
func IsValidIdentifier(s string) bool {
	return regexpValidIdentifier.MatchString(s)
}

// Root is the root of a declarative configuration, consisting of a mesos
// block, one or more framework blocks, and one or more deployment blocks.
type Root struct {
	Mesos       *Mesos       `hcl:"mesos,block"`
	Frameworks  []Framework  `hcl:"framework,block"`
	Deployments []Deployment `hcl:"deployment,block"`
}

// Mesos is a block that specifies the parameters of an Apache Mesos cluster.
type Mesos struct {
	Zookeepers string   `hcl:"zookeepers,attr"`
	Masters    []string `hcl:"masters,attr"`
}

// FrameworkRef is a block that references a framework.
type FrameworkRef struct {
	Type string `hcl:"type,attr"`
	Name string `hcl:"name,attr"`
}

// Framework is a block that specifies the parameters of a Mesos framework,
// such as Marathon or Chronos.
type Framework struct {
	Type                string         `hcl:"type,label"`
	Name                string         `hcl:"name,label"`
	MesosName           string         `hcl:"mesos_name,attr"`
	Masters             []string       `hcl:"masters,attr"`
	CreatedByDeployment *DeploymentRef `hcl:"created_by_deployment,block"`
}

// Ref returns the FrameworkRef for f.
func (f *Framework) Ref() FrameworkRef {
	return FrameworkRef{
		Type: f.Type,
		Name: f.Name,
	}
}

// DeploymentRef is a block that references a deployment.
type DeploymentRef struct {
	Type string `hcl:"type,attr"`
	Name string `hcl:"name,attr"`
}

// Deployment is a block that defines the parameters of a deployment into
// a Mesos framework.
// If the framework is not specified, it is identical to the value "default".
type Deployment struct {
	Type         string           `hcl:"type,label"`
	Name         string           `hcl:"name,label"`
	Framework    string           `hcl:"framework,optional"`
	Deploy       string           `hcl:"deploy,attr"`
	Labels       []string         `hcl:"labels,optional"`
	Dependencies []DependencySpec `hcl:"dependency,block"`
	DependencyOf []DependencySpec `hcl:"dependency_of,block"`
}

// Ref returns the DeploymentRef for d.
func (d *Deployment) Ref() DeploymentRef {
	return DeploymentRef{
		Type: d.Type,
		Name: d.Name,
	}
}

// DependencyRef is a block that defines the parameters of a specific
// dependency relationship to exactly one deployment.
type DependencyRef struct {
	Type           string `hcl:"type,attr"`
	Name           string `hcl:"name,attr"`
	WaitForHealthy bool   `hcl:"wait_for_healthy,optional"`
}

// DeploymentRef returns the DeploymentRef for r.
func (r DependencyRef) DeploymentRef() DeploymentRef {
	return DeploymentRef{
		Type: r.Type,
		Name: r.Name,
	}
}

// DependencySpec is a block that defines the parameters of an abstract
// dependency relationship to zero or more deployments.
// A DependencySpec can take one of two forms: it can be identical to a
// DependencyRef, or it can omit the dependent's name and use zero or more
// filter blocks to narrow down the targets.
// In the latter form, the dependent's type can be specified as "*" to target
// all types of deployments.
type DependencySpec struct {
	Type           string   `hcl:"type,attr"`
	Name           string   `hcl:"name,optional"`
	WaitForHealthy bool     `hcl:"wait_for_healthy,optional"`
	Filters        []Filter `hcl:"filter,block"`
}

// Filter is a block that specifies the criteria used to narrow down the
// targets of a dependency relationship.
type Filter struct {
	Key    string   `hcl:"key,attr"`
	Value  string   `hcl:"value,optional"`
	Values []string `hcl:"values,optional"`
	Glob   bool     `hcl:"glob,optional"`
	Regexp bool     `hcl:"regexp,optional"`
	Negate bool     `hcl:"negate,optional"`
}
