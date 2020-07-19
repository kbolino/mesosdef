package deploy

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/kbolino/mesosdef/model"
)

// Deployer is implemented by any mechanism capable of deploying a resource
// to a framework and checking its health.
type Deployer interface {
	// Deploy executes the deployment of ref, blocking until it the framework
	// reports it is complete.
	Deploy(ref model.DeploymentRef) error
	// WaitUntilHealthy blocks until the deployed resources of ref are
	// considered healthy by the framework.
	// If the framework or resources do not support health checks, then
	// WaitUntilHealthy should return quickly with no error.
	WaitUntilHealthy(ref model.DeploymentRef) error
}

// Status indicates the current state of a deployment.
type Status int32

// Status constants are used according to the following state diagram:
//
//                       StatusNotReady
//                              | (ready)
//                         StatusReady
//                        / (deploy)  \ (cancel)
//                StatusDeploying   StatusCanceled
//               /               \
//    StatusWaitingUntilHealthy  StatusDeployError
//         /                \
//    StatusHealthy   StatusHealthError
//
// It is also possible to enter StatusPanic when a panic occurs in deploy.
const (
	StatusNotReady Status = iota
	StatusReady
	StatusCanceled
	StatusDeploying
	StatusWaitingUntilHealthy
	StatusHealthy
	StatusDeployError
	StatusHealthError
	StatusPanic
)

func (s Status) String() string {
	switch s {
	case StatusNotReady:
		return "StatusNotReady"
	case StatusReady:
		return "StatusReady"
	case StatusCanceled:
		return "StatusCanceled"
	case StatusDeploying:
		return "StatusDeploying"
	case StatusWaitingUntilHealthy:
		return "StatusWaitingUntilHealthy"
	case StatusHealthy:
		return "StatusHealthy"
	case StatusDeployError:
		return "StatusDeployError"
	case StatusHealthError:
		return "StatusHealthError"
	case StatusPanic:
		return "StatusPanic"
	default:
		return "unknown"
	}
}

// Deployment encapsulates the state of an active deployment.
//
// Deployments have two phases:
//    1. the actual deployment to the framework (deploy phase), and
//    2. waiting for the deployed resources to become healthy (healthy phase).
//
// Exposed methods are safe to use from multiple concurrent goroutines.
type Deployment struct {
	ref          model.DeploymentRef
	deployer     Deployer
	status       int32
	deployMutex  sync.Mutex
	deployChan   chan struct{}
	deployError  error
	healthyMutex sync.Mutex
	healthyChan  chan struct{}
	healthyError error
}

// Ref returns the DeploymentRef for d.
func (d *Deployment) Ref() model.DeploymentRef {
	return d.ref
}

// Status returns the current state of d.
func (d *Deployment) Status() Status {
	return Status(atomic.LoadInt32(&d.status))
}

// WaitUntilDeployed blocks until d has completed its deploy phase.
// If an error occurs in the deploy phase, it is returned here.
// WaitUntilDeployed can be called any number of times and will return
// immediately with the deploy phase result if it is called after the
// phase has completed.
func (d *Deployment) WaitUntilDeployed() error {
	<-d.deployChan
	d.deployMutex.Lock()
	err := d.deployError
	d.deployMutex.Unlock()
	return err
}

// WaitUntilHealthy blocks until d has completed its health phase.
// All Deployments have a health phase even if all of the dependenents don't
// actually wait for the deployed resources to become healthy.
// However, if the deploy phase fails, the health phase might not be entered
// and WaitUntilHealthy may block indefinitely.
// Always call WaitUntilDeployed first and check its result, proceeding to
// call WaitUntilHealthy only if the deploy phase has no error.
func (d *Deployment) WaitUntilHealthy() error {
	<-d.healthyChan
	d.healthyMutex.Lock()
	err := d.healthyError
	d.healthyMutex.Unlock()
	return err
}

// cancel signals that the deployment has been canceled, for example if one of
// its dependencies has failed.
// cancel can only be called if d is in state StatusReady.
// If cancel returns no error, d is put in state StatusCanceled.
func (d *Deployment) cancel(err error) error {
	if !d._swapStatus(StatusReady, StatusCanceled) {
		return fmt.Errorf("deployment is not ready or already started")
	}
	d.deployMutex.Lock()
	d.deployError = err
	d.deployMutex.Unlock()
	close(d.deployChan)
	close(d.healthyChan)
	return nil
}

// deploy begins the deployment process, entering the deploy phase and then
// the health phase if the deploy phase succeeds.
// deploy can only be called if d is in state StatusReady.
// If deploy returns no error, d is put in state StatusHealthy.
// Refer to the state diagram for more detail.
func (d *Deployment) deploy() error {
	if !d._swapStatus(StatusReady, StatusDeploying) {
		return fmt.Errorf("deployment is not ready or already started")
	}
	defer func() {
		if r := recover(); r != nil {
			d._setStatus(StatusPanic)
			panic(r)
		}
	}()
	err := d._deployPhase()
	if err != nil {
		d._setStatus(StatusDeployError)
		return err
	}
	d._setStatus(StatusWaitingUntilHealthy)
	if err := d._healthPhase(); err != nil {
		d._setStatus(StatusHealthError)
		return err
	}
	d._setStatus(StatusHealthy)
	return nil
}

// ready initializes d with the given deployer and moves it into state
// StatusReady.
// ready can only be called if d is in state StatusNotReady.
func (d *Deployment) ready(deployer Deployer, ref model.DeploymentRef) error {
	if deployer == nil {
		return fmt.Errorf("deployer is nil")
	} else if !d._swapStatus(StatusNotReady, StatusReady) {
		return fmt.Errorf("deployment is already readied")
	}
	d.ref = ref
	d.deployer = deployer
	d.deployChan = make(chan struct{}, 0)
	d.healthyChan = make(chan struct{}, 0)
	d._setStatus(StatusReady)
	return nil
}

// _deployPhase is the internal implementation of the deploy phase.
func (d *Deployment) _deployPhase() error {
	defer close(d.deployChan)
	d.deployMutex.Lock()
	defer d.deployMutex.Unlock()
	if err := d.deployer.Deploy(d.ref); err != nil {
		err = fmt.Errorf("failed to deploy to framework: %w", err)
		d.deployError = err
		return err
	}
	return nil
}

// _healthPhase is the internal implementation of the health phase.
func (d *Deployment) _healthPhase() error {
	defer close(d.healthyChan)
	d.healthyMutex.Lock()
	defer d.healthyMutex.Unlock()
	if err := d.deployer.WaitUntilHealthy(d.ref); err != nil {
		err = fmt.Errorf("failed to wait until framework considered deployment healthy: %w", err)
		d.healthyError = err
		return err
	}
	return nil
}

// _setStatus is an internal helper to unconditionally set the status of d.
func (d *Deployment) _setStatus(status Status) {
	atomic.StoreInt32(&d.status, int32(status))
}

// _swapStatus is an internal helper to set the status of d to new only if it
// is currently in old, returning true only if it does set the status.
func (d *Deployment) _swapStatus(old, new Status) bool {
	return atomic.CompareAndSwapInt32(&d.status, int32(old), int32(new))
}
