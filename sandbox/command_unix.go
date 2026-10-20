//go:build !windows

package sandbox

import (
	"errors"
	"os/exec"
)

// platformState is the started child. On Unix it is an os/exec.Cmd, which
// already owns the process group, the cancellation and the pipe draining, so
// nothing here has to restate them.
type platformState struct {
	cmd *exec.Cmd
}

func (c *Command) Start() error {
	if c.started {
		return errors.New("sandbox: command already started")
	}
	c.started = true
	ctx := background(c.ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, c.path, c.args...)
	cmd.Env = c.Env
	cmd.Dir = c.Dir
	cmd.Stdin = c.Stdin
	cmd.Stdout = c.Stdout
	cmd.Stderr = c.Stderr
	cmd.WaitDelay = c.WaitDelay
	// The process group and the group-wide kill. Without them a tool that
	// started a subprocess outlived its own deadline; the job object closes
	// the same gap on the other platform.
	isolate(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	c.state = &platformState{cmd: cmd}
	return nil
}

func (c *Command) Wait() error {
	if c.state == nil || c.state.cmd == nil {
		return errors.New("sandbox: command was not started")
	}
	return c.state.cmd.Wait()
}
