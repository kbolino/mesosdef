package model

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/yourbasic/graph"
)

// Graph is a graph of deployments connected by their dependencies.
// Ideally, this should be a directed acyclic graph (DAG) and some methods
// will return errors if the graph is not a DAG.
// A graph g is a DAG if len(g.Cycles()) == 0.
type Graph struct {
	deployments []DeploymentRef
	index       map[DeploymentRef]int
	rawGraph    *graph.Mutable
}

// Build builds a graph from the given deployments.
// Returns a non-nil error if the graph has already been built, if any of the
// explicit dependencies can't be resolved, or if any of the dependency
// filters are invalid.
func (g *Graph) Build(deployments ...Deployment) error {
	if len(g.deployments) != 0 {
		return fmt.Errorf("graph has already been built")
	} else if len(deployments) == 0 {
		return nil
	}
	g.deployments = make([]DeploymentRef, len(deployments))
	g.index = make(map[DeploymentRef]int, len(deployments))
	for i, deployment := range deployments {
		ref := deployment.Ref()
		g.deployments[i] = ref
		g.index[ref] = i
	}
	g.rawGraph = graph.New(len(deployments))
	for i, _ := range deployments {
		deployment := &deployments[i]
		for j := range deployment.Dependencies {
			dependency := &deployment.Dependencies[j]
			dependents, err := findDependents(dependency, deployments)
			if err != nil {
				return fmt.Errorf("finding dependents of deployment.%s.%s: %w",
					deployment.Type, deployment.Name, err)
			}
			var c int64
			if dependency.WaitForHealthy {
				c = 1
			}
			for _, k := range dependents {
				g.rawGraph.AddCost(i, k, c)
			}
		}
		for j := range deployment.DependencyOf {
			inverseDependency := &deployment.DependencyOf[j]
			inverseDependents, err := findDependents(inverseDependency, deployments)
			if err != nil {
				return fmt.Errorf("finding inverse dependents of deployment %s.%s: %w",
					deployment.Type, deployment.Name, err)
			}
			var c int64
			if inverseDependency.WaitForHealthy {
				c = 1
			}
			for _, k := range inverseDependents {
				g.rawGraph.AddCost(k, i, c)
			}
		}
	}
	return nil
}

// Cycles returns a list of all dependency cycles in the graph.
func (g *Graph) Cycles() [][]DeploymentRef {
	components := graph.StrongComponents(g.rawGraph)
	var cycles [][]DeploymentRef
	for _, component := range components {
		if len(component) != 1 {
			cycle := make([]DeploymentRef, len(component))
			for i, j := range component {
				cycle[i] = g.deployments[j]
			}
			cycles = append(cycles, cycle)
		}
	}
	return cycles
}

// DeployOrder returns a list of all deployments in the graph, sorted in the
// order they would be deployed.
// This is equivalent to a reverse topological sort of the dependency graph.
// Returns a non-nil error if and only if there are cycles in the graph.
func (g *Graph) DeployOrder() ([]DeploymentRef, error) {
	topSort, ok := graph.TopSort(g.rawGraph)
	if !ok {
		return nil, fmt.Errorf("dependency cycles exist")
	}
	deployments := make([]DeploymentRef, len(g.deployments))
	for i := range topSort {
		j := topSort[len(topSort)-1-i]
		deployments[i] = g.deployments[j]
	}
	return deployments, nil
}

// Dependencies returns a list of all resolved dependencies for a deployment.
// Returns a non-nil error if and only if the deployment is not in the graph.
func (g *Graph) Dependencies(deployment DeploymentRef) ([]DependencyRef, error) {
	v, ok := g.index[deployment]
	if !ok {
		return nil, fmt.Errorf("deployment %s.%s not in graph", deployment.Type, deployment.Name)
	}
	var dependencies []DependencyRef
	g.rawGraph.Visit(v, func(w int, c int64) bool {
		dependency := g.deployments[w]
		waitForHealthy := c != 0
		dependencies = append(dependencies, DependencyRef{
			Type:           dependency.Type,
			Name:           dependency.Name,
			WaitForHealthy: waitForHealthy,
		})
		return false
	})
	return dependencies, nil
}

// findDependents returns a slice of all the deployment indices that match
// the given dependency spec.
func findDependents(dependency *DependencySpec, deployments []Deployment) ([]int, error) {
	filters := dependency.Filters
	if dependency.Name != "" {
		if len(filters) != 0 {
			return nil, fmt.Errorf("dependency can have name attribute or filter blocks but not both")
		} else if dependency.Type == "*" {
			return nil, fmt.Errorf("dependency type cannot be \"*\" if name attribute is specified")
		}
		filters = []Filter{
			Filter{
				Key:   "name",
				Value: dependency.Name,
			},
		}
	}
	var dependents []int
	for i := range deployments {
		deployment := &deployments[i]
		switch dependency.Type {
		case "*":
			// do nothing
		case "marathon_app", "chronos_job":
			if dependency.Type != deployment.Type {
				continue
			}
		default:
			return nil, fmt.Errorf("unknown dependency type \"%s\", only \"*\", \"marathon_app\", "+
				"and \"chronos_job\" are supported", dependency.Type)
		}
		allMatch := true
		for i := range filters {
			filter := &filters[i]
			matches, err := filterMatches(filter, deployment)
			if err != nil {
				return nil, err
			}
			if !matches {
				allMatch = false
				break
			}
		}
		if allMatch {
			dependents = append(dependents, i)
		}
	}
	if dependency.Name != "" && len(dependents) != 1 {
		return nil, fmt.Errorf("dependent deployment %s.%s not found", dependency.Type, dependency.Name)
	}
	return dependents, nil
}

// filterMatches returns true if and only if the given filter matches the
// given deployment.
func filterMatches(filter *Filter, deployment *Deployment) (bool, error) {
	values := filter.Values
	if len(values) == 0 {
		if filter.Value == "" {
			return false, fmt.Errorf("filter must have one of value or values attribute")
		}
		values = []string{filter.Value}
	}
	var compareTo []string
	switch filter.Key {
	case "name":
		compareTo = []string{deployment.Name}
	case "labels":
		compareTo = deployment.Labels
	default:
		return false, fmt.Errorf("unknown filter key \"%s\", only \"name\" and \"labels\" supported", filter.Key)
	}
	if len(compareTo) == 0 {
		return filter.Negate, nil
	}
	for _, val := range values {
		if filter.Glob || filter.Regexp {
			valRegexp := val
			if filter.Glob {
				var err error
				valRegexp, err = globToRegexp(val)
				if err != nil {
					return false, fmt.Errorf("invalid glob pattern \"%s\": %w", val, err)
				}
			}
			compiled, err := regexp.Compile(valRegexp)
			if err != nil {
				return false, fmt.Errorf("invalid regexp pattern \"%s\": %w", valRegexp, err)
			}
			for _, cmp := range compareTo {
				if compiled.MatchString(cmp) {
					return !filter.Negate, nil
				}
			}
		} else {
			for _, cmp := range compareTo {
				if val == cmp {
					return !filter.Negate, nil
				}
			}
		}
	}
	return filter.Negate, nil
}

// globToRegexp converts a glob expression into a regular expression.
func globToRegexp(glob string) (string, error) {
	var result strings.Builder
	result.WriteRune('^')
	backslash := false
	for i, r := range glob {
		switch r {
		case '\\':
			if backslash {
				result.WriteString("\\\\")
				backslash = false
			} else {
				backslash = true
			}
		case '*':
			if backslash {
				result.WriteString("\\*")
				backslash = false
			} else {
				result.WriteString(".*")
			}
		case '?':
			if backslash {
				result.WriteString("\\?")
			} else {
				result.WriteString(".")
			}
		case '[':
			if backslash {
				result.WriteString("\\[")
			} else {
				return "", fmt.Errorf("character classes not supported in glob yet")
			}
		case ']':
			if backslash {
				result.WriteString("\\]")
			} else {
				return "", fmt.Errorf("character classes not supported in glob yet")
			}
		default:
			if backslash {
				return "", fmt.Errorf("unknown escape sequence \"\\%c\"", r)
			} else {
				result.WriteString(regexp.QuoteMeta(glob[i : i+utf8.RuneLen(r)]))
			}
		}
	}
	result.WriteRune('$')
	return result.String(), nil
}
