package deploy

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kbolino/mesosdef/model"
)

// EventType indicates the type of an Event produced by a GraphDeployer.
type EventType int32

// EventType constants are used for a particular deployment according to the
// following state diagram, where {} means possible repetition:
//
//                           EventEnqueued
//                                 |
//                           EventDequeued
//                          /             \
//        EventDependenciesResolved        EventDeploymentFailure
//       /                         \
//     {EventDependencySuccess}     EventDependencyFailure
//                 |                        |
//      EventDeploymentStarted              |
//     /                      \             |
//    EventDeploymentSuccess   EventDeploymentFailure
const (
	EventEnqueued EventType = iota
	EventDequeued
	EventError
	EventDependenciesResolved
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
	case EventDependenciesResolved:
		return "EventDependenciesResolved"
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

// Dependency encapsulates the parameters of an active deployment dependency
// target.
type Dependency struct {
	Deployment     *Deployment
	WaitForHealthy bool
}

// Event is produced by GraphDeployer as deployments change state.
type Event struct {
	Type       EventType
	WorkerID   int
	Deployment *Deployment
	Dependency Dependency
	Err        error
}

// Stats contains statistics on the results of a deployment.
type Stats struct {
	TotalDeployments      int32
	SuccessfulDeployments int32
	FailedDeployments     int32
	ElapsedTime           time.Duration
}

// GraphDeployer uses a Deployer to execute the ordered, dependency-conscious
// deployment of all resources described by a graph.
// A single GraphDeployer is meant to execute a single deployment.
type GraphDeployer struct {
	graph             *model.Graph
	deployer          Deployer
	maxDeploy         int
	closeWorkChanOnce sync.Once
	workChan          chan *Deployment
	waitGroup         sync.WaitGroup
	deployments       []Deployment
	deploymentsByRef  map[model.DeploymentRef]*Deployment
	eventsChan        chan<- Event
	errorsChan        chan error
	stats             Stats
}

// NewGraphDeployer creates a new GraphDeployer for the given graph,
// deployer, and options.
func NewGraphDeployer(graph *model.Graph, deployer Deployer, maxDeploy int) (*GraphDeployer, error) {
	if graph == nil {
		return nil, fmt.Errorf("graph is nil")
	} else if deployer == nil {
		return nil, fmt.Errorf("deployer is nil")
	}
	return &GraphDeployer{
		graph:      graph,
		deployer:   deployer,
		maxDeploy:  maxDeploy,
		workChan:   make(chan *Deployment, 0),
		errorsChan: make(chan error, 1),
	}, nil
}

// Stats returns statistics on the deployment, which are only meaningful
// after Deploy has been called.
func (d *GraphDeployer) Stats() Stats {
	return d.stats
}

// Deploy executes the deployment process, blocking until it is complete.
// To monitor the status of the deployment, provide a non-nil events channel.
// Deploy will create a fixed number of worker goroutines to execute the
// and will wait until they all complete.
func (d *GraphDeployer) Deploy(events chan<- Event) error {
	d.eventsChan = events
	defer d.closeEventsChan()
	defer d.closeWorkChan()
	startTime := time.Now()
	defer func() {
		d.stats.ElapsedTime = time.Now().Sub(startTime)
	}()
	for i := 0; i < d.maxDeploy; i++ {
		d.waitGroup.Add(1)
		go func(workerID int) {
			defer d.waitGroup.Done()
			d.workerMain(workerID)
		}(i + 1)
	}
	deployOrder, err := d.graph.DeployOrder()
	if err != nil {
		return fmt.Errorf("resolving deployment order: %w", err)
	}
	d.stats.TotalDeployments = int32(len(deployOrder))
	d.deployments = make([]Deployment, len(deployOrder))
	d.deploymentsByRef = make(map[model.DeploymentRef]*Deployment, len(deployOrder))
	for i, deployRef := range deployOrder {
		deployment := &d.deployments[i]
		if err := deployment.ready(d.deployer, deployRef); err != nil {
			return fmt.Errorf("readying deployment: %w", err)
		}
		d.deploymentsByRef[deployRef] = deployment
	}
	for i := range d.deployments {
		deployment := &d.deployments[i]
		d.sendEvent(0, Event{
			Type:       EventEnqueued,
			Deployment: deployment,
		})
		d.workChan <- deployment
	}
	d.closeWorkChan()
	d.waitGroup.Wait()
	// TODO handle multiple errors?
	select {
	case err := <-d.errorsChan:
		return err
	default:
		return nil
	}
}

func (d *GraphDeployer) closeEventsChan() {
	if d.eventsChan != nil {
		close(d.eventsChan)
	}
}

// closeWorkChan closes the worker channel safely.
func (d *GraphDeployer) closeWorkChan() {
	d.closeWorkChanOnce.Do(func() {
		close(d.workChan)
	})
}

// sendError sends an error to the errors channel without blocking.
func (d *GraphDeployer) sendError(err error) {
	select {
	case d.errorsChan <- err:
	default:
	}
}

// sendEvent sends an event to the events channel if it is non-nil.
// workerID is a mandatory parameter to ensure it gets set.
func (d *GraphDeployer) sendEvent(workerID int, event Event) {
	if d.eventsChan != nil {
		event.WorkerID = workerID
		d.eventsChan <- event
	}
}

// workerMain is the entry point for the worker goroutines, each of which
// should have a distinct workerID.
func (d *GraphDeployer) workerMain(workerID int) {
	for deployment := range d.workChan {
		deployRef := deployment.Ref()
		d.sendEvent(workerID, Event{
			Type:       EventDequeued,
			Deployment: deployment,
		})
		if err := d.workerDeploy(workerID, deployment); err != nil {
			// ignore cancelation errors, if it's too late to cancel then the
			// error came from the deployment anyway
			_ = deployment.cancel(err)
			atomic.AddInt32(&d.stats.FailedDeployments, 1)
			d.sendError(fmt.Errorf("deploying %s.%s: %w", deployRef.Type, deployRef.Name, err))
			d.sendEvent(workerID, Event{
				Type:       EventDeploymentFailure,
				Deployment: deployment,
				Err:        err,
			})
		} else {
			atomic.AddInt32(&d.stats.SuccessfulDeployments, 1)
			d.sendEvent(workerID, Event{
				Type:       EventDeploymentSuccess,
				Deployment: deployment,
				Err:        err,
			})
		}
	}
}

func (d *GraphDeployer) workerDeploy(workerID int, deployment *Deployment) error {
	deployRef := deployment.Ref()
	// resolve dependencies
	dependRefs, err := d.graph.Dependencies(deployRef)
	if err != nil {
		return fmt.Errorf("cannot resolve dependencies: %w", err)
	}
	// map dependencies to their deployments
	var dependencies []Dependency
	if len(dependRefs) != 0 {
		dependencies = make([]Dependency, len(dependRefs))
		for i, dependRef := range dependRefs {
			dependency, ok := d.deploymentsByRef[dependRef.DeploymentRef()]
			if !ok {
				return fmt.Errorf("no deployment exists for dependency %s.%s", dependRef.Type, dependRef.Name)
			}
			dependencies[i] = Dependency{
				Deployment:     dependency,
				WaitForHealthy: dependRef.WaitForHealthy,
			}
		}
	}
	d.sendEvent(workerID, Event{
		Type:       EventDependenciesResolved,
		Deployment: deployment,
	})
	// wait for dependencies
	for i, dependency := range dependencies {
		dependRef := dependRefs[i]
		err := dependency.Deployment.WaitUntilDeployed()
		if err != nil {
			d.sendEvent(workerID, Event{
				Type:       EventDependencyFailure,
				Deployment: deployment,
				Dependency: dependency,
				Err:        err,
			})
			return fmt.Errorf("dependency %s.%s failed to deploy: %w", dependRef.Type, dependRef.Name, err)
		}
		if dependRef.WaitForHealthy {
			err := dependency.Deployment.WaitUntilHealthy()
			if err != nil {
				d.sendEvent(workerID, Event{
					Type:       EventDependencyFailure,
					Deployment: deployment,
					Dependency: dependency,
					Err:        err,
				})
				return fmt.Errorf("dependency %s.%s failed to become healthy: %w",
					dependRef.Type, dependRef.Name, err)
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
	return deployment.deploy()
}
