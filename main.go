package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/kbolino/mesosdef/model"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/zclconf/go-cty/cty"
)

var regexpValidIdentifier = regexp.MustCompile("^[a-zA-Z_][a-zA-Z0-9_]*$")

var (
	flagFile      string
	flagMaxDeploy int
	flagNoenv     bool
	flagVars      stringSliceValue
)

func main() {
	// set up flags
	flag.StringVar(&flagFile, "file", "", "file to parse")
	flag.IntVar(&flagMaxDeploy, "maxDeploy", 5, "maximum number of simultaneous deployments")
	flag.BoolVar(&flagNoenv, "noenv", false, "do not get variables from environment")
	flag.Var(&flagVars, "var", "set a variable var=value, can be repeated")
	flag.Parse()
	// run
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s", err)
		os.Exit(1)
	}
}

func run() error {
	// parse variables from env & args and add to EvalContext
	varCount := len(flagVars)
	environ := os.Environ()
	if !flagNoenv {
		varCount += len(environ)
	}
	ctx := hcl.EvalContext{
		Variables: make(map[string]cty.Value, varCount),
	}
	if !flagNoenv {
		for _, envDef := range environ {
			parts := strings.SplitN(envDef, "=", 2)
			if len(parts) != 2 {
				continue
			}
			name := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if !regexpValidIdentifier.MatchString(name) {
				continue
			}
			ctx.Variables[name] = cty.StringVal(value)
		}
	}
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
	// parse declaration file
	var root model.Root
	if err := hclsimple.DecodeFile(flagFile, &ctx, &root); err != nil {
		return fmt.Errorf("decoding file \"%s\": %w", flagFile, err)
	}
	// create deployment dependency graph
	var graph model.Graph
	graph.Build(root.Deployments...)
	// check for dependency cycles
	cycles := graph.Cycles()
	if len(cycles) != 0 {
		var deploymentNames []string
		for _, deployment := range cycles[0] {
			deploymentNames = append(deploymentNames, fmt.Sprintf("%s.%s", deployment.Type, deployment.Name))
		}
		return fmt.Errorf("dependency cycle detected: %s", strings.Join(deploymentNames, ", "))
	}
	// sort and print graph
	deployOrder, err := graph.DeployOrder()
	if err != nil {
		return err
	}
	for _, deployment := range deployOrder {
		fmt.Printf("%s.%s\n", deployment.Type, deployment.Name)
		dependencies, err := graph.Dependencies(deployment)
		if err != nil {
			return err
		}
		for _, dependency := range dependencies {
			prefix := "immediately after"
			if dependency.WaitForHealthy {
				prefix = "after waiting for"
			}
			fmt.Printf("\t%s %s.%s\n", prefix, dependency.Type, dependency.Name)
		}
	}
	return nil
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
