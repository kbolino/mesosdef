package deploy

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/kbolino/mesosdef/model"
)

type Status int32

const (
	StatusNotReady Status = iota
	StatusReady
	StatusDeploying
	StatusWaitingUntilHealthy
	StatusHealthy
	StatusDeployError
	StatusHealthError
	StatusPanicError
)

func (s Status) String() string {
	switch s {
	case StatusNotReady:
		return "StatusNotReady"
	case StatusReady:
		return "StatusReady"
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
	case StatusPanicError:
		return "StatusPanicError"
	default:
		return "unknown"
	}
}

type Deployment struct {
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
// Ref panics if d is not readied.
func (d *Deployment) Ref() model.DeploymentRef {
	return d.deployer.DeploymentRef()
}

func (d *Deployment) Status() Status {
	return Status(atomic.LoadInt32(&d.status))
}

func (d *Deployment) WaitUntilDeployed() error {
	<-d.deployChan
	d.deployMutex.Lock()
	err := d.deployError
	d.deployMutex.Unlock()
	return err
}

func (d *Deployment) WaitUntilHealthy() error {
	<-d.healthyChan
	d.healthyMutex.Lock()
	err := d.healthyError
	d.healthyMutex.Unlock()
	return err
}

func (d *Deployment) cancel(err error) error {
	if !d._swapStatus(StatusReady, StatusDeployError) {
		return fmt.Errorf("deployment is not ready or already started")
	}
	d.deployMutex.Lock()
	d.deployError = err
	d.deployMutex.Unlock()
	close(d.deployChan)
	close(d.healthyChan)
	return nil
}

func (d *Deployment) deploy() error {
	if !d._swapStatus(StatusReady, StatusDeploying) {
		return fmt.Errorf("deployment is not ready or already started")
	}
	defer func() {
		if r := recover(); r != nil {
			d._setStatus(StatusPanicError)
			panic(r)
		}
	}()
	err := d._deploy()
	if err != nil {
		d._setStatus(StatusDeployError)
		return err
	}
	d._setStatus(StatusWaitingUntilHealthy)
	if err := d._waitUntilHealthy(); err != nil {
		d._setStatus(StatusHealthError)
		return err
	}
	d._setStatus(StatusHealthy)
	return nil
}

func (d *Deployment) ready(deployer Deployer) error {
	if deployer == nil {
		return fmt.Errorf("deployer is nil")
	} else if !d._swapStatus(StatusNotReady, StatusReady) {
		return fmt.Errorf("deployment is already readied")
	}
	d.deployer = deployer
	d.deployChan = make(chan struct{}, 0)
	d.healthyChan = make(chan struct{}, 0)
	d._setStatus(StatusReady)
	return nil
}

func (d *Deployment) _deploy() error {
	defer close(d.deployChan)
	d.deployMutex.Lock()
	defer d.deployMutex.Unlock()
	if err := d.deployer.Deploy(); err != nil {
		err = fmt.Errorf("failed to deploy to framework: %w", err)
		d.deployError = err
		return err
	}
	return nil
}

func (d *Deployment) _setStatus(status Status) {
	atomic.StoreInt32(&d.status, int32(status))
}

func (d *Deployment) _swapStatus(old, new Status) bool {
	return atomic.CompareAndSwapInt32(&d.status, int32(old), int32(new))
}

func (d *Deployment) _waitUntilHealthy() error {
	defer close(d.healthyChan)
	d.healthyMutex.Lock()
	defer d.healthyMutex.Unlock()
	if err := d.deployer.WaitUntilHealthy(); err != nil {
		err = fmt.Errorf("failed to wait until framework considered deployment healthy: %w", err)
		d.healthyError = err
		return err
	}
	return nil
}

type Deployer interface {
	DeploymentRef() model.DeploymentRef
	Deploy() error
	WaitUntilHealthy() error
}
