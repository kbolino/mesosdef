package deploy

import (
	"fmt"
	"sync"

	"github.com/kbolino/mesosdef/model"
)

type Mapper func(ref model.DeploymentRef) (Deployer, error)

type EventType int32

const (
	EventEnqueued EventType = iota
	EventDequeued
	EventError
	EventDependencyFailure
	EventDependencySuccess
	EventDeploymentStarted
	EventDeploymentSuccess
	EventDeploymentFailure
)

func (e EventType) String() string {
	switch e {
	case EventEnqueued:
		return "EventEnqueued"
	case EventDequeued:
		return "EventDequeued"
	case EventError:
		return "EventError"
	case EventDependencyFailure:
		return "EventDependencyFailure"
	case EventDependencySuccess:
		return "EventDependencySuccess"
	case EventDeploymentStarted:
		return "EventDeploymentStarted"
	case EventDeploymentSuccess:
		return "EventDeploymentSuccess"
	case EventDeploymentFailure:
		return "EventDeploymentFailure"
	default:
		return "unknown"
	}
}

type Dependency struct {
	Deployment     *Deployment
	WaitForHealthy bool
}

type Event struct {
	Type       EventType
	WorkerID   int
	Deployment *Deployment
	Dependency Dependency
	Err        error
}

type Listener func(e Event)

type Option func(c *deployerConfig) error

func MaxDeploy(maxDeploy int) Option {
	return func(c *deployerConfig) error {
		if maxDeploy < 1 {
			return fmt.Errorf("maxDeploy is not positive")
		}
		c.maxDeploy = maxDeploy
		return nil
	}
}

func WorkChanSize(workChanSize int) Option {
	return func(c *deployerConfig) error {
		if workChanSize < 0 {
			return fmt.Errorf("workChanSize is negative")
		}
		c.workChanSize = workChanSize
		return nil
	}
}

func EventsChanSize(eventsChanSize int) Option {
	return func(c *deployerConfig) error {
		if eventsChanSize < 0 {
			return fmt.Errorf("eventsChanSize is negative")
		}
		c.eventsChanSize = eventsChanSize
		return nil
	}
}

type deployerConfig struct {
	maxDeploy      int
	workChanSize   int
	eventsChanSize int
	errorsChanSize int
}

type GraphDeployer struct {
	graph             *model.Graph
	mapper            Mapper
	maxDeploy         int
	closeWorkChanOnce sync.Once
	workChan          chan *Deployment
	waitGroup         sync.WaitGroup
	deployments       []Deployment
	deploymentsByRef  map[model.DeploymentRef]*Deployment
	eventsChan        chan Event
	errorsChan        chan error
}

func NewGraphDeployer(graph *model.Graph, mapper Mapper, options ...Option) (*GraphDeployer, error) {
	if graph == nil {
		return nil, fmt.Errorf("graph is nil")
	} else if mapper == nil {
		return nil, fmt.Errorf("mapper is nil")
	}
	c := &deployerConfig{
		maxDeploy:      2,
		workChanSize:   0,
		eventsChanSize: 100,
		errorsChanSize: 1,
	}
	for _, option := range options {
		err := option(c)
		if err != nil {
			return nil, err
		}
	}
	return &GraphDeployer{
		graph:      graph,
		mapper:     mapper,
		maxDeploy:  c.maxDeploy,
		workChan:   make(chan *Deployment, c.workChanSize),
		eventsChan: make(chan Event, c.eventsChanSize),
		errorsChan: make(chan error, c.errorsChanSize),
	}, nil
}

func (d *GraphDeployer) EventsChan() <-chan Event {
	return d.eventsChan
}

func (d *GraphDeployer) Deploy() error {
	defer close(d.eventsChan)
	defer d.closeWorkChan()
	for i := 0; i < d.maxDeploy; i++ {
		d.waitGroup.Add(1)
		go func(workerID int) {
			defer d.waitGroup.Done()
			d.workerMain(workerID)
		}(i + 1)
	}
	deployOrder, err := d.graph.DeployOrder()
	if err != nil {
		return err
	}
	d.deployments = make([]Deployment, len(deployOrder))
	d.deploymentsByRef = make(map[model.DeploymentRef]*Deployment, len(deployOrder))
	for i, deployRef := range deployOrder {
		deployment := &d.deployments[i]
		deployer, err := d.mapper(deployRef)
		if err != nil {
			return fmt.Errorf("mapping deployment %s.%s to deployer: %w", deployRef.Type, deployRef.Name, err)
		}
		if err := deployment.ready(deployer); err != nil {
			return fmt.Errorf("readying deployment %s.%s: %w", deployRef.Type, deployRef.Name, err)
		}
		d.deploymentsByRef[deployRef] = deployment
	}
	for i := range d.deployments {
		deployment := &d.deployments[i]
		d.workChan <- deployment
		d.sendEvent(0, Event{
			Type:       EventEnqueued,
			Deployment: deployment,
		})
	}
	d.closeWorkChan()
	d.waitGroup.Wait()
	// TODO handle multiple errors
	select {
	case err := <-d.errorsChan:
		return err
	default:
		return nil
	}
}

func (d *GraphDeployer) closeWorkChan() {
	d.closeWorkChanOnce.Do(func() {
		close(d.workChan)
	})
}

func (d *GraphDeployer) sendError(err error) {
	select {
	case d.errorsChan <- err:
	default:
	}
}

func (d *GraphDeployer) sendEvent(workerID int, event Event) {
	event.WorkerID = workerID
	select {
	case d.eventsChan <- event:
	default:
	}
}

func (d *GraphDeployer) workerMain(workerID int) {
workLoop:
	for deployment := range d.workChan {
		deployRef := deployment.Ref()
		// resolve dependencies
		dependRefs, err := d.graph.Dependencies(deployRef)
		if err != nil {
			err = fmt.Errorf("cannot resolve dependencies: %w", err)
			deployment.cancel(err)
			d.sendEvent(workerID, Event{
				Type:       EventError,
				Deployment: deployment,
				Err:        err,
			})
			d.sendError(err)
			continue workLoop
		}
		// map dependencies to their deployments
		var dependencies []Dependency
		if len(dependRefs) != 0 {
			dependencies = make([]Dependency, len(dependRefs))
			for i, dependRef := range dependRefs {
				dependency, ok := d.deploymentsByRef[dependRef.DeploymentRef()]
				if !ok {
					err = fmt.Errorf("no deployment exists for dependency %s.%s", dependRef.Type, dependRef.Name)
					deployment.cancel(err)
					d.sendEvent(workerID, Event{
						Type:       EventError,
						Deployment: deployment,
						Err:        err,
					})
					d.sendError(err)
					continue workLoop
				}
				dependencies[i] = Dependency{
					Deployment:     dependency,
					WaitForHealthy: dependRef.WaitForHealthy,
				}
			}
		}
		d.sendEvent(workerID, Event{
			Type:       EventDequeued,
			Deployment: deployment,
		})
		// wait for dependencies
		for i, dependency := range dependencies {
			dependRef := dependRefs[i]
			err := dependency.Deployment.WaitUntilDeployed()
			if err != nil {
				err = fmt.Errorf("dependency %s.%s failed to deploy: %w", dependRef.Type, dependRef.Name, err)
				deployment.cancel(err)
				d.sendEvent(workerID, Event{
					Type:       EventDependencyFailure,
					Deployment: deployment,
					Dependency: dependency,
					Err:        err,
				})
				d.sendError(err)
				continue workLoop
			}
			if dependRef.WaitForHealthy {
				err := dependency.Deployment.WaitUntilHealthy()
				if err != nil {
					err = fmt.Errorf("dependency %s.%s failed to become healthy: %w",
						dependRef.Type, dependRef.Name, err)
					deployment.cancel(err)
					d.sendEvent(workerID, Event{
						Type:       EventDependencyFailure,
						Deployment: deployment,
						Dependency: dependency,
						Err:        err,
					})
					d.sendError(err)
					continue workLoop
				}
			}
			d.sendEvent(workerID, Event{
				Type:       EventDependencySuccess,
				Deployment: deployment,
				Dependency: dependency,
			})
		}
		// start the deployment
		d.sendEvent(workerID, Event{
			Type:       EventDeploymentStarted,
			Deployment: deployment,
		})
		if err := deployment.deploy(); err != nil {
			err = fmt.Errorf("deploying %s.%s: %w", deployRef.Type, deployRef.Name, err)
			d.sendEvent(workerID, Event{
				Type:       EventDeploymentFailure,
				Deployment: deployment,
				Err:        err,
			})
			d.sendError(err)
		} else {
			d.sendEvent(workerID, Event{
				Type:       EventDeploymentSuccess,
				Deployment: deployment,
			})
		}
	}
}
