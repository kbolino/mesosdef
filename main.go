package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/yourbasic/graph"
	"github.com/zclconf/go-cty/cty"
)

var regexpValidIdentifier = regexp.MustCompile("^[a-zA-Z_][a-zA-Z0-9_]*$")

var (
	flagFile = ""
	flagVars = stringSliceValue{}
)

func main() {
	// set up flags
	flag.StringVar(&flagFile, "file", "", "file to parse")
	flag.Var(&flagVars, "var", "set a variable var=value, can be repeated")
	flag.Parse()
	// run
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s", err)
		os.Exit(1)
	}
}

func run() error {
	var root Root
	var ctx hcl.EvalContext
	ctx.Variables = make(map[string]cty.Value, len(flagVars))
	if len(flagVars) != 0 {
		for _, varDef := range flagVars {
			parts := strings.SplitN(varDef, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid variable declaration \"%s\"", varDef)
			}
			name := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if !regexpValidIdentifier.MatchString(name) {
				return fmt.Errorf("invalid variable name \"%s\"", name)
			}
			if len(value) == 0 {
				delete(ctx.Variables, name)
			} else {
				ctx.Variables[name] = cty.StringVal(value)
			}
		}
	}
	if err := hclsimple.DecodeFile(flagFile, &ctx, &root); err != nil {
		return fmt.Errorf("decoding file \"%s\": %w", flagFile, err)
	}
	deployments := root.Deployments
	g := graph.New(len(deployments))
	for i := range root.Deployments {
		deployment := &root.Deployments[i]
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
				g.AddCost(i, k, c)
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
		return fmt.Errorf("dependency cycle detected: %s", strings.Join(deploymentNames, ", "))
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
	return nil
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

type stringSliceValue []string

var _ flag.Value = &stringSliceValue{}

func (v *stringSliceValue) String() string {
	return strings.Join(*v, ", ")
}

func (v *stringSliceValue) Set(value string) error {
	*v = append(*v, value)
	return nil
}
