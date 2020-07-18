package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/yourbasic/graph"
	"github.com/zclconf/go-cty/cty"
)

var (
	flagFile = flag.String("file", "", "file to parse")
)

func main() {
	flag.Parse()
	os.Exit(run())
}

func run() int {
	var root Root
	var ctx hcl.EvalContext
	ctx.Variables = map[string]cty.Value{
		"deploy_root": cty.StringVal("./deploy"),
		"dns_tld":     cty.StringVal("mesos"),
	}
	if err := hclsimple.DecodeFile(*flagFile, &ctx, &root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	deployments := root.Deployments
	g := graph.New(len(deployments))
	for i := range root.Deployments {
		deployment := &root.Deployments[i]
		for j := range deployment.Dependencies {
			dependency := &deployment.Dependencies[j]
			dependents, err := findDependents(dependency, deployments)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: finding dependents of deployment.%s.%s: %s",
					deployment.Type, deployment.Name, err)
				return 1
			}
			var c int64
			if dependency.WaitForHealthy {
				c = 1
			}
			for _, k := range dependents {
				g.AddCost(i, k, c)
			}
		}
		for j := range deployment.DependencyOf {
			inverseDependency := &deployment.DependencyOf[j]
			inverseDependents, err := findDependents(inverseDependency, deployments)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: finding inverse dependents for deploymen.%s.%s: %s",
					deployment.Type, deployment.Name, err)
				return 1
			}
			var c int64
			if inverseDependency.WaitForHealthy {
				c = 1
			}
			for _, k := range inverseDependents {
				g.AddCost(k, i, c)
			}
		}
	}
	components := graph.StrongComponents(g)
	for _, component := range components {
		if len(component) == 1 {
			continue
		}
		var deploymentNames []string
		for _, i := range component {
			deployment := &deployments[i]
			deploymentNames = append(deploymentNames, fmt.Sprintf("%s.%s", deployment.Type, deployment.Name))
		}
		fmt.Fprintf(os.Stderr, "ERROR: dependency cycle detected: %s\n", strings.Join(deploymentNames, ", "))
		return 1
	}
	topSort, ok := graph.TopSort(g)
	if !ok {
		fmt.Fprintln(os.Stderr, "ERROR: topological sort failed")
	}
	for i := len(topSort) - 1; i >= 0; i-- {
		j := topSort[i]
		deployment := &deployments[j]
		fmt.Printf("%s.%s\n", deployment.Type, deployment.Name)
		g.Visit(j, func(w int, c int64) bool {
			dependentDeployment := &deployments[w]
			prefix := "immediately after"
			if c != 0 {
				prefix = "after waiting for"
			}
			fmt.Printf("\t%s %s.%s\n", prefix, dependentDeployment.Type, dependentDeployment.Name)
			return false
		})
	}
	return 0
}

func findDependents(dependency *DependencyRef, deployments []Deployment) ([]int, error) {
	filters := dependency.Filters
	if dependency.Name != "" {
		if len(filters) != 0 {
			return nil, fmt.Errorf("dependency can have name attribute or filter blocks but not both")
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
			return nil, fmt.Errorf("unknown dependency type \"%s\", only \"*\", \"marathon_app\", and \"chronos_job\" supported",
				dependency.Type)
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
	return dependents, nil
}

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
	if !filter.Negate && len(compareTo) == 0 {
		return false, nil
	}
	if filter.Glob {
		return false, fmt.Errorf("glob not supported yet")
	} else if filter.Regexp {
		return false, fmt.Errorf("regexp not supported yet")
	}
	for _, val := range values {
		for _, cmp := range compareTo {
			if val == cmp {
				return !filter.Negate, nil
			}
		}
	}
	return filter.Negate, nil
}
