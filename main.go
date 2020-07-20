package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kbolino/mesosdef/deploy"
	"github.com/kbolino/mesosdef/model"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/zclconf/go-cty/cty"
)

var (
	flagDeployTimeout int
	flagDryRun        bool
	flagFile          string
	flagMaxDeploy     int
	flagNoenv         bool
	flagVars          stringSliceValue
	flagWaitTimeout   int
)

func main() {
	// set up flags
	flag.IntVar(&flagDeployTimeout, "deployTimeout", 30, "timeout for deployment requests, in seconds")
	flag.BoolVar(&flagDryRun, "dryRun", false, "check files and produce graph, but do not deploy")
	flag.StringVar(&flagFile, "file", "", "file to parse")
	flag.IntVar(&flagMaxDeploy, "maxDeploy", 5, "maximum number of simultaneous deployments")
	flag.BoolVar(&flagNoenv, "noenv", false, "do not get variables from environment")
	flag.Var(&flagVars, "var", "set a variable var=value, can be repeated")
	flag.IntVar(&flagWaitTimeout, "waitTimeout", 300, "timeout for waiting until healthy, in seconds")
	flag.Parse()
	// run
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %s\n", err)
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
	// sort and print graph for dry run
	if flagDryRun {
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
	// run mock deployment
	rand.Seed(time.Now().UnixNano())
	deployer := &mockDeployer{
		minDeployTime:      50 * time.Millisecond,
		maxDeployTime:      250 * time.Millisecond,
		deployErrorChance:  0.01,
		minHealthyTime:     200 * time.Millisecond,
		maxHealthyTime:     700 * time.Millisecond,
		healthyErrorChance: 0.01,
	}
	events := make(chan deploy.Event, 100)
	graphDeployer, err := deploy.NewGraphDeployer(&graph, deployer, flagMaxDeploy)
	if err != nil {
		return fmt.Errorf("creating graph deployer: %w", err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range events {
			var otherPart string
			if event.Err != nil {
				otherPart = fmt.Sprintf(", error='%s'", event.Err.Error())
			} else if event.Dependency.Deployment != nil {
				deployRef := event.Dependency.Deployment.Ref()
				otherPart = fmt.Sprintf(", dependency=%s.%s", deployRef.Type, deployRef.Name)
			}
			deployRef := event.Deployment.Ref()
			timeFormat := "2006-01-02T15:04:05.000Z07:00"
			fmt.Printf("%s workerID=%d queueLen=%-3d %-25s %s.%s%s\n", time.Now().Format(timeFormat),
				event.WorkerID, len(events), event.Type, deployRef.Type, deployRef.Name, otherPart)
		}
	}()
	deployErr := graphDeployer.Deploy(events)
	wg.Wait()
	stats := graphDeployer.Stats()
	fmt.Printf("Result: %d successful and %d failed deployments of %d resources in %s\n", stats.SuccessfulDeployments,
		stats.FailedDeployments, stats.TotalDeployments, stats.ElapsedTime.Truncate(time.Millisecond))
	if deployErr != nil {
		return fmt.Errorf("deploying graph: %w", deployErr)
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

type mockDeployer struct {
	minDeployTime      time.Duration
	maxDeployTime      time.Duration
	deployErrorChance  float32
	minHealthyTime     time.Duration
	maxHealthyTime     time.Duration
	healthyErrorChance float32
}

var _ deploy.Deployer = &mockDeployer{}

func (d *mockDeployer) Deploy(ref model.DeploymentRef) error {
	waitTime := d.minDeployTime + time.Duration(rand.Int63n(int64(d.maxDeployTime-d.minDeployTime)))
	time.Sleep(waitTime)
	if d.deployErrorChance != 0 && rand.Float32() < d.deployErrorChance {
		return fmt.Errorf("mock error")
	}
	return nil
}

func (d *mockDeployer) WaitUntilHealthy(ref model.DeploymentRef) error {
	waitTime := d.minHealthyTime + time.Duration(rand.Int63n(int64(d.maxHealthyTime-d.minHealthyTime)))
	time.Sleep(waitTime)
	if d.healthyErrorChance != 0 && rand.Float32() < d.healthyErrorChance {
		return fmt.Errorf("mock error")
	}
	return nil
}
