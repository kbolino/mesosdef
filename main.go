package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kbolino/mesosdef/model"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/zclconf/go-cty/cty"
)

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
			if !model.IsValidIdentifier(name) {
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
			if !model.IsValidIdentifier(name) {
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
	// validate and index frameworks
	frameworksByRef := make(map[model.FrameworkRef]*model.Framework)
	for i := range root.Frameworks {
		framework := &root.Frameworks[i]
		switch framework.Type {
		case "marathon", "chronos":
			// ok
		default:
			return fmt.Errorf("invalid framework type \"%s\"", framework.Type)
		}
		if !model.IsValidIdentifier(framework.Name) {
			return fmt.Errorf("invalid framework name \"%s\"", framework.Name)
		}
		if _, exists := frameworksByRef[framework.Ref()]; exists {
			return fmt.Errorf("duplicate framework %s.%s", framework.Type, framework.Name)
		}
		frameworksByRef[framework.Ref()] = framework
	}
	// validate and index deployments
	deploymentsByRef := make(map[model.DeploymentRef]*model.Deployment)
	for i := range root.Deployments {
		deployment := &root.Deployments[i]
		frameworkType := ""
		switch deployment.Type {
		case "marathon_app":
			frameworkType = "marathon"
		case "chronos_job":
			frameworkType = "chronos"
		default:
			return fmt.Errorf("invalid deployment type \"%s\"", deployment.Type)
		}
		if !model.IsValidIdentifier(deployment.Name) {
			return fmt.Errorf("invalid deployment name \"%s\"", deployment.Name)
		}
		if _, exists := deploymentsByRef[deployment.Ref()]; exists {
			return fmt.Errorf("duplicate deployment %s.%s", deployment.Type, deployment.Name)
		}
		frameworkName := deployment.Framework
		if frameworkName == "" {
			frameworkName = "default"
		}
		frameworkRef := model.FrameworkRef{
			Type: frameworkType,
			Name: frameworkName,
		}
		if _, exists := frameworksByRef[frameworkRef]; !exists {
			return fmt.Errorf("no framework %s.%s defined for deployment %s.%s", frameworkType, frameworkName,
				deployment.Type, deployment.Name)
		}
		deploymentsByRef[deployment.Ref()] = deployment
	}
	// create deployment dependency graph
	var graph model.Graph
	graph.Build(root.Deployments...)
	// check for dependency cycles
	cycles := graph.Cycles()
	if len(cycles) != 0 {
		var message strings.Builder
		message.WriteString("dependency cycle(s) detected:\n")
		for _, cycle := range cycles {
			var deploymentNames []string
			for _, deployment := range cycle {
				deploymentNames = append(deploymentNames, fmt.Sprintf("%s.%s", deployment.Type, deployment.Name))
			}
			fmt.Fprintf(&message, "\t=> %s <=\n", strings.Join(deploymentNames, " <==> "))
		}
		return fmt.Errorf("%s", message.String())
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
