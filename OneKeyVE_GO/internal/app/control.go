package app

import (
	"errors"
	"os"
	"sync"

	"onekeyvego/internal/procutil"
)

var ErrStopped = errors.New("processing stopped by user")

type RunController struct {
	mu            sync.Mutex
	cond          *sync.Cond
	paused        bool
	stopRequested bool
	current       *os.Process
}

func NewRunController() *RunController {
	controller := &RunController{}
	controller.cond = sync.NewCond(&controller.mu)
	return controller
}

func (c *RunController) WaitIfPaused() error {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for c.paused && !c.stopRequested {
		c.cond.Wait()
	}
	if c.stopRequested {
		return ErrStopped
	}
	return nil
}

func (c *RunController) SetPaused(paused bool) error {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	if c.stopRequested {
		c.mu.Unlock()
		return ErrStopped
	}
	if c.paused == paused {
		c.mu.Unlock()
		return nil
	}
	c.paused = paused
	current := c.current
	if !paused {
		c.cond.Broadcast()
	}
	c.mu.Unlock()

	if current == nil {
		return nil
	}
	if paused {
		return procutil.SuspendProcess(current)
	}
	return procutil.ResumeProcess(current)
}

func (c *RunController) RequestStop() error {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	if c.stopRequested {
		c.mu.Unlock()
		return nil
	}
	c.stopRequested = true
	current := c.current
	wasPaused := c.paused
	c.paused = false
	c.cond.Broadcast()
	c.mu.Unlock()

	if current == nil {
		return nil
	}
	if wasPaused {
		_ = procutil.ResumeProcess(current)
	}
	if err := current.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func (c *RunController) StopRequested() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopRequested
}

func (c *RunController) IsPaused() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paused
}

func (c *RunController) AttachProcess(process *os.Process) error {
	if c == nil || process == nil {
		return nil
	}

	c.mu.Lock()
	c.current = process
	paused := c.paused
	stopRequested := c.stopRequested
	c.mu.Unlock()

	if stopRequested {
		if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return ErrStopped
	}
	if paused {
		return procutil.SuspendProcess(process)
	}
	return nil
}

func (c *RunController) DetachProcess(process *os.Process) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if process == nil || c.current == process {
		c.current = nil
	}
}
