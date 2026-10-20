package sandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"
)

// Command is one isolated child process, prepared but not started.
//
// It exists because of Windows. Everywhere else the isolation is spelled on
// the command line — a sandbox-exec profile, a line of bwrap flags — so the
// launch is a plain exec.Cmd and os/exec does the pipes, the waiting and the
// cancellation. On Windows the isolation is the container the process is born
// into, and the container is chosen by an attribute list handed to
// CreateProcess. os/exec has nowhere to put one: SysProcAttr has carried no
// security capabilities since the type was written and the proposals to add
// it have never shipped, and a running process cannot be moved into a
// container afterwards. That call therefore has to be ours, and once it is,
// so does everything that follows from it.
//
// The fields are the os/exec ones the call sites were already written
// against, kept with their names and their meanings, so that nothing outside
// this package has to change shape.
type Command struct {
	// Env is the exact environment the child runs with. Nothing is inherited
	// from the operator's shell.
	Env []string
	// Dir is the child's working directory.
	Dir string
	// Stdin, Stdout and Stderr follow os/exec: a nil Stdout or Stderr is the
	// null device, a nil Stdin is an empty one.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// WaitDelay follows os/exec. Once the process has exited it bounds how
	// long its output may still be arriving. A worker that leaves something
	// holding its pipe open is not a worker whose answer can be trusted to
	// be complete, so the wait ends in an error rather than in silence.
	WaitDelay time.Duration

	// ctx is cancelled to stop the child. It is the context the caller
	// passed to Command, and the same one that decides the deadline.
	ctx context.Context
	// path is the program to run and args its arguments, with args[0] the
	// program again the way each platform expects to find it.
	path string
	args []string
	// root is the workspace and writes whether it may be modified. Unix
	// carries that decision on the command line it builds; Windows carries
	// it as access control entries on the directory itself, which is why it
	// has to be known here rather than only at the call site.
	root   string
	writes bool
	// container is the Windows AppContainer the process will belong to, kept
	// from the moment its grants are placed until they are removed. It is nil
	// on the other platforms, whose isolation ends when the process is
	// created.
	container *appContainerIdentity
	// denied and executableGrant record what was changed on disk, so that
	// cleanup removes exactly those entries and not whatever happens to be
	// true at cleanup time. A boundary placed for one run and guessed at
	// afterwards is how an operator ends up locked out of their own keys.
	denied   []string
	exeGrant string

	started bool
	// state is a pointer because it holds the wait group that tracks the
	// output copies, and a wait group must not be copied after first use.
	state *platformState
}

// CombinedOutput runs the child and returns everything it wrote to either
// stream.
//
// One buffer receives both, and the platform side gives them one pipe when
// they are the same destination: two pipes feeding one unsynchronised
// bytes.Buffer from two goroutines is a data race, and os/exec avoids it by
// sharing a descriptor.
func (c *Command) CombinedOutput() ([]byte, error) {
	if c.Stdout != nil || c.Stderr != nil {
		return nil, errors.New("sandbox: output is already set before CombinedOutput")
	}
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	err := c.Run()
	return buf.Bytes(), err
}

// Run starts the child and waits for it to finish.
func (c *Command) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}

// interfaceEqual reports whether two destinations are the same object, which
// is what makes them eligible for a shared pipe. The comparison panics when
// the dynamic type is not comparable, and a writer need not be — a slice
// type is legal — so the panic is taken rather than passed on.
func interfaceEqual(a, b any) (equal bool) {
	defer func() { recover() }()
	return a == b
}

// background returns the context to run under when a Command was built
// without one. Nothing constructs it that way today; a nil here would
// otherwise become a panic inside os/exec or a wait that never returns.
func background(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
