//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procThreadAttributeSecurityCapabilities is the attribute that makes the new
// process an AppContainer process.
//
// It is ProcThreadAttributeValue(9, FALSE, TRUE, FALSE) from winnt.h: attribute
// number 9 with the input bit set, which is 0x00020009. The constant does not
// exist in x/sys/windows.
//
// The cost of getting this wrong is not a failed call. A wrong attribute key
// is either rejected outright or accepted and ignored, and the second case
// produces an ordinary process holding every right the operator handed the
// harness: a sandbox that reports success and enforces nothing. It is written
// here as the header defines it, and TestTheCageRefusesHomeSecrets decides
// whether it is right by trying to read a file the container must not reach.
const procThreadAttributeSecurityCapabilities = 0x00020009

// diagnoseWithoutContainer makes buildLauncher start the child without the
// container, so the diagnostic probe can tell a launch fault from a
// containment fault in a single run. Nothing outside the diagnostic sets it.
var diagnoseWithoutContainer = false

// platformState is a started child and everything that has to outlive the
// CreateProcess call: the job object that owns the whole tree, and this side
// of the pipes.
type platformState struct {
	process windows.Handle
	// job owns the process tree. It is configured KILL_ON_JOB_CLOSE, so
	// closing it — including by this process dying — takes every descendant
	// with it. Windows has no kill(-pgid); this is what stands in for it.
	job windows.Handle

	// The child's ends, held only until the child has them.
	childStdin  windows.Handle
	childStdout windows.Handle
	childStderr windows.Handle

	// This side's ends, as files so io.Copy can move bytes through them.
	stdin  *os.File
	stdout *os.File
	stderr *os.File

	// stderrAliasesStdout records that both streams are one pipe, so that the
	// single file is closed once. CombinedOutput is the case: one buffer gets
	// one pipe, because two pipes feeding an unsynchronised buffer from two
	// goroutines is a data race.
	stderrAliasesStdout bool

	copies  sync.WaitGroup
	stopped bool
}

// Start creates the child inside an AppContainer, leaves it suspended, puts
// it in the job that owns it, and only then lets it run.
func (c *Command) Start() error {
	if c.started {
		return errors.New("sandbox: command already started")
	}
	c.started = true
	if err := background(c.ctx).Err(); err != nil {
		return err
	}

	sid, err := openAppContainer()
	if err != nil {
		return err
	}
	// Stored on the Command because the grants it justifies have to be taken
	// back in release, long after this function has returned. A grant whose
	// handle to its identity is gone can never be revoked, and the operator
	// is left with a permanently more permissive workspace.
	c.container = sid

	// Every decision that can refuse the launch is made before the process
	// exists, so there is no window in which a child runs with less
	// protection than was asked for. cageACL fails closed: if the write hole
	// cannot be cut, no process is created at all.
	denied, err := cageACL(c.root, sid, c.writes)
	if err != nil {
		return err
	}
	c.denied = denied
	// The worker binary has to be startable by the container. Windows grants
	// that in the system directories and nowhere else, so a worker beside a
	// checkout needs to be named.
	granted, err := grantExecutable(c.path, sid)
	if err != nil {
		c.takeDownBoundary()
		return err
	}
	c.exeGrant = granted

	job, err := newJob()
	if err != nil {
		c.takeDownBoundary()
		return err
	}

	state := &platformState{}
	started := false
	defer func() {
		if started {
			return
		}
		// A job with KILL_ON_JOB_CLOSE kills whatever it holds when its last
		// handle closes, so abandoning it here is a kill and not a leak:
		// nothing started may outlive a launch that did not finish. The
		// boundary comes off too, because a launch that did not finish is
		// exactly when a grant is easiest to forget.
		windows.CloseHandle(job)
		state.closeAll()
		c.takeDownBoundary()
	}()

	if err := state.openPipes(c); err != nil {
		return err
	}
	launcher, err := buildLauncher(c, sid, state)
	if err != nil {
		return err
	}
	defer launcher.close()

	var info windows.ProcessInformation
	if err := windows.CreateProcess(
		nil,
		launcher.commandLine,
		nil, nil,
		true,
		uint32(windows.CREATE_UNICODE_ENVIRONMENT|
			windows.EXTENDED_STARTUPINFO_PRESENT|
			windows.CREATE_SUSPENDED),
		launcher.environment,
		launcher.directory,
		&launcher.startupInfo.StartupInfo,
		&info,
	); err != nil {
		return fmt.Errorf("create appcontainer process: %w", err)
	}
	// The child's ends are the child's now. Holding a copy of the write end
	// here would keep this process a second owner of it, and a reader with
	// another owner never sees end-of-file: the wait hangs instead of
	// returning when the child is gone.
	state.closeChildPipes()
	launcher.close()

	finished := false
	defer func() {
		if finished {
			return
		}
		// Assigned to the job or not, the child dies here. If the assignment
		// below failed this is the only thing that reaches it; if it
		// succeeded the job would do it anyway when the handle closed.
		windows.TerminateProcess(info.Process, 1)
		windows.CloseHandle(info.Thread)
		windows.CloseHandle(info.Process)
	}()

	if err := windows.AssignProcessToJobObject(job, info.Process); err != nil {
		return fmt.Errorf("assign worker to job: %w", err)
	}
	if _, err := windows.ResumeThread(info.Thread); err != nil {
		return fmt.Errorf("resume worker: %w", err)
	}
	windows.CloseHandle(info.Thread)
	// The process handle is not closed here. It is stored on the state below
	// and closed by exitStatus once the wait has used it; closing it now
	// would leave the state holding a handle that is already gone, and a
	// later wait would be waiting on a number rather than a process.

	// The process may not run until here. A suspended child is not running a
	// tool, so nothing needs to read its output yet; starting the copies
	// after the resume means they cannot race the launch.
	state.process = info.Process
	state.job = job
	state.startCopies(c)
	c.state = state
	started, finished = true, true
	return nil
}

// openPipes builds this process's pipes to the child.
//
// The child is always given three real handles. STARTF_USESTDHANDLES tells
// Windows to use the handles in the startup information and not to invent
// console ones, and a zero there is not "no stream" -- it is a null handle that
// a program writing its output will fail against. cmd.exe is where that shows
// up first: a command as ordinary as `echo hello` exits non-zero having printed
// nothing, which reads like a containment failure and is not one. A stream the
// caller did not ask for is pointed at the null device instead, which is what
// os/exec does on this platform.
func (st *platformState) openPipes(c *Command) error {
	var err error
	if c.Stdin != nil {
		read, write, err := inheritablePipe()
		if err != nil {
			return fmt.Errorf("stdin: %w", err)
		}
		st.childStdin, st.stdin = read, os.NewFile(uintptr(write), "worker-stdin")
	} else if st.childStdin, err = nullDevice(); err != nil {
		return fmt.Errorf("stdin: %w", err)
	}
	if c.Stdout != nil {
		read, write, err := inheritablePipe()
		if err != nil {
			return fmt.Errorf("stdout: %w", err)
		}
		st.childStdout, st.stdout = write, os.NewFile(uintptr(read), "worker-stdout")
	} else if st.childStdout, err = nullDevice(); err != nil {
		return fmt.Errorf("stdout: %w", err)
	}
	if c.Stderr != nil {
		if interfaceEqual(c.Stdout, c.Stderr) {
			st.childStderr, st.stderr = st.childStdout, st.stdout
			st.stderrAliasesStdout = true
		} else {
			read, write, err := inheritablePipe()
			if err != nil {
				return fmt.Errorf("stderr: %w", err)
			}
			st.childStderr, st.stderr = write, os.NewFile(uintptr(read), "worker-stderr")
		}
	} else if st.childStderr, err = nullDevice(); err != nil {
		return fmt.Errorf("stderr: %w", err)
	}
	return nil
}

// nullDevice opens NUL as an inheritable handle for a stream the caller did not
// ask for. It is inherited like any other, so it is closed with the rest.
func nullDevice() (windows.Handle, error) {
	path, err := windows.UTF16PtrFromString("NUL")
	if err != nil {
		return 0, err
	}
	attributes := windows.SecurityAttributes{
		Length:        uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		InheritHandle: 1,
	}
	handle, err := windows.CreateFile(
		path,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		&attributes,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return 0, err
	}
	return handle, nil
}

// startCopies shuttles bytes between the caller's readers and writers and the
// child, which is the work os/exec does for an ordinary command.
func (st *platformState) startCopies(c *Command) {
	// Reading the child's output happens before waiting, so a child that
	// fills a pipe buffer cannot deadlock against a parent waiting for it to
	// exit.
	if st.stdout != nil {
		st.copies.Add(1)
		go func() {
			defer st.copies.Done()
			io.Copy(c.Stdout, st.stdout)
		}()
	}
	if st.stderr != nil && !st.stderrAliasesStdout {
		st.copies.Add(1)
		go func() {
			defer st.copies.Done()
			io.Copy(c.Stderr, st.stderr)
		}()
	}
	if st.stdin != nil {
		st.copies.Add(1)
		go func() {
			defer st.copies.Done()
			io.Copy(st.stdin, c.Stdin)
			// The caller's input has ended. Closing our end gives the child
			// EOF rather than a read that waits for input that will never
			// come.
			st.stdin.Close()
		}()
	}
}

// Wait waits for the child and reports what happened.
func (c *Command) Wait() error {
	st := c.state
	if st == nil || st.process == 0 {
		return errors.New("sandbox: command was not started")
	}
	ctx := background(c.ctx)
	exited := make(chan error, 1)
	go func() { exited <- st.exitStatus() }()

	select {
	case err := <-exited:
		return c.release(st, err)
	case <-ctx.Done():
		// Terminating the job, not the process. TerminateProcess on the child
		// alone would leave anything it spawned running outside the deadline
		// that stopped it.
		st.stop()
		<-exited
		return c.release(st, ctx.Err())
	}
}

// exitStatus waits for the process and reports a non-zero code the way
// os/exec reports one, so callers keep the same shape.
func (st *platformState) exitStatus() error {
	if _, err := windows.WaitForSingleObject(st.process, windows.INFINITE); err != nil {
		return err
	}
	var code uint32
	err := windows.GetExitCodeProcess(st.process, &code)
	windows.CloseHandle(st.process)
	st.process = 0
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("exit status %d", code)
	}
	return nil
}

// stop ends the whole tree.
func (st *platformState) stop() {
	if st.stopped {
		return
	}
	st.stopped = true
	if st.job != 0 {
		_ = windows.TerminateJobObject(st.job, 1)
	}
}

// release drains the remaining output, takes the boundary back off the
// workspace, and returns every handle.
//
// WaitDelay bounds the drain. Once the process has exited the pipes should
// close on their own, but a descendant that inherited them can hold them open
// with nothing left to say, and a wait that never returns is the worst of the
// available failures. On expiry the pipes are closed under the copies, which
// turns the read into an error and lets the call finish.
func (c *Command) release(st *platformState, waitErr error) error {
	drained := make(chan struct{})
	go func() {
		st.copies.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(c.WaitDelay):
		if waitErr == nil {
			waitErr = errors.New("sandbox: output still open after the process exited")
		}
	}
	st.closeParentPipes()
	if st.job != 0 {
		windows.CloseHandle(st.job)
		st.job = 0
	}
	// The grants and denies come off here, after the process is gone, so the
	// workspace is never left more permissive than it was found. Revoking
	// earlier would open a window in which the worker is still running and
	// the grant that let it work has already been withdrawn.
	c.takeDownBoundary()
	return waitErr
}

// takeDownBoundary puts every access list this run changed back the way it
// found it. It is written once and called from the three places a launch can
// end — the failure paths in Start, the defer that covers an abandoned
// launch, and release — because a boundary that is restored on two of the
// three is a boundary that is sometimes permanent.
func (c *Command) takeDownBoundary() {
	if c.container == nil {
		return
	}
	cageCleanup(c.root, c.container, c.denied, c.exeGrant)
	c.container.free()
	c.container = nil
	c.denied = nil
	c.exeGrant = ""
}

// closeChildPipes releases this process's copies of the child's ends.
func (st *platformState) closeChildPipes() {
	for _, handle := range []windows.Handle{st.childStdin, st.childStdout, st.childStderr} {
		if handle != 0 {
			windows.CloseHandle(handle)
		}
	}
	st.childStdin, st.childStdout, st.childStderr = 0, 0, 0
}

func (st *platformState) closeParentPipes() {
	if st.stdin != nil {
		st.stdin.Close()
		st.stdin = nil
	}
	if st.stdout != nil {
		st.stdout.Close()
		st.stdout = nil
	}
	if st.stderr != nil && !st.stderrAliasesStdout {
		st.stderr.Close()
	}
	st.stderr = nil
}

func (st *platformState) closeAll() {
	st.closeChildPipes()
	st.closeParentPipes()
}

// childHandles is the handle list handed to CreateProcess, in the form
// Windows requires: no zeros and no duplicates.
func (st *platformState) childHandles() []windows.Handle {
	seen := map[windows.Handle]bool{}
	var handles []windows.Handle
	for _, handle := range []windows.Handle{st.childStdin, st.childStdout, st.childStderr} {
		if handle == 0 || seen[handle] {
			continue
		}
		seen[handle] = true
		handles = append(handles, handle)
	}
	return handles
}

// launcher holds everything that must stay alive and unchanged between being
// built and being used. CreateProcess reads it all while the new thread is
// suspended, and the attribute list is backed by the Windows heap, so it is
// freed deliberately rather than left to a finaliser.
type launcher struct {
	commandLine *uint16
	directory   *uint16
	environment *uint16
	startupInfo windows.StartupInfoEx
	attributes  *windows.ProcThreadAttributeListContainer
	inherited   []windows.Handle
}

// buildLauncher assembles the command line, environment and startup
// information, including the attribute list that makes the process an
// AppContainer and the handle list that decides exactly which handles cross
// into it.
func buildLauncher(c *Command, sid *appContainerIdentity, st *platformState) (*launcher, error) {
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(c.args))
	if err != nil {
		return nil, err
	}
	directory, err := windows.UTF16PtrFromString(c.Dir)
	if err != nil {
		return nil, err
	}
	environment, err := environmentBlock(c.Env)
	if err != nil {
		return nil, err
	}
	result := &launcher{commandLine: commandLine, directory: directory}
	if len(environment) > 0 {
		result.environment = &environment[0]
	}

	// Two attributes at most: the capabilities and handle list. The
	// handle list matters more than usual here. The child is a container
	// whose whole point is to have nothing, and without the list every
	// inheritable handle in this process would be in it.
	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, fmt.Errorf("create startup attribute list: %w", err)
	}
	result.attributes = attributes

	// No capabilities. The list is empty on purpose: an AppContainer is
	// denied the network by default, and the internet capabilities are what
	// would hand it back. There is no setting that grants them.
	//
	// diagnoseWithoutContainer exists only for the diagnostic probe, which
	// has to start the same child without the container to tell a launch
	// fault from a containment fault. It is never set in production.
	if !diagnoseWithoutContainer {
		capabilities := securityCapabilities{appContainerSid: sid.sid}
		if err := attributes.Update(
			procThreadAttributeSecurityCapabilities,
			unsafe.Pointer(&capabilities),
			unsafe.Sizeof(capabilities),
		); err != nil {
			result.close()
			return nil, fmt.Errorf("set appcontainer capabilities: %w", err)
		}
	}
	result.inherited = st.childHandles()
	if len(result.inherited) > 0 {
		if err := attributes.Update(
			windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
			unsafe.Pointer(&result.inherited[0]),
			uintptr(len(result.inherited))*unsafe.Sizeof(result.inherited[0]),
		); err != nil {
			result.close()
			return nil, fmt.Errorf("set inherited handle list: %w", err)
		}
	}

	result.startupInfo = windows.StartupInfoEx{}
	result.startupInfo.StartupInfo.Cb = uint32(unsafe.Sizeof(result.startupInfo))
	result.startupInfo.StartupInfo.Flags = windows.STARTF_USESTDHANDLES
	result.startupInfo.StartupInfo.StdInput = st.childStdin
	result.startupInfo.StartupInfo.StdOutput = st.childStdout
	result.startupInfo.StartupInfo.StdErr = st.childStderr
	result.startupInfo.ProcThreadAttributeList = attributes.List()
	return result, nil
}

func (l *launcher) close() {
	if l == nil || l.attributes == nil {
		return
	}
	l.attributes.Delete()
	l.attributes = nil
	if l.startupInfo.ProcThreadAttributeList != nil {
		l.startupInfo.ProcThreadAttributeList = nil
	}
}

// inheritablePipe creates a pipe whose ends are both inheritable. The caller
// passes the read end to whichever process consumes bytes and the write end to
// whichever process produces them. Which handles cross into the child is
// decided by the handle list, not by the inherit bit; the bit only makes a
// handle eligible for inheritance.
func inheritablePipe() (read, write windows.Handle, err error) {
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{}))}
	attributes.InheritHandle = 1
	if err := windows.CreatePipe(&read, &write, &attributes, 0); err != nil {
		return 0, 0, fmt.Errorf("create pipe: %w", err)
	}
	return read, write, nil
}

// environmentBlock encodes the child's environment in the Unicode block
// CreateProcess expects: entries sorted case-insensitively, each terminated by
// a NUL, and a final NUL for the block.
func environmentBlock(env []string) ([]uint16, error) {
	if len(env) == 0 {
		return nil, nil
	}
	sorted := append([]string(nil), env...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return strings.ToUpper(sorted[i]) < strings.ToUpper(sorted[j])
	})
	var block []uint16
	for _, entry := range sorted {
		if strings.ContainsRune(entry, 0) {
			return nil, errors.New("environment entry contains a NUL")
		}
		encoded, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, fmt.Errorf("encode environment entry: %w", err)
		}
		block = append(block, encoded...)
	}
	return append(block, 0), nil
}

// newJob creates the object that owns the worker's whole process tree.
func newJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create job object: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("configure job object limits: %w", err)
	}
	// The child gets no desktop: it cannot open windows, read or write the
	// clipboard, change display settings, or post messages into the
	// operator's session. A tool call has no business doing any of that, and
	// these make a whole class of interaction impossible that a file
	// boundary alone would not cover.
	restrictions := windows.JOBOBJECT_BASIC_UI_RESTRICTIONS{
		UIRestrictionsClass: windows.JOB_OBJECT_UILIMIT_DESKTOP |
			windows.JOB_OBJECT_UILIMIT_DISPLAYSETTINGS |
			windows.JOB_OBJECT_UILIMIT_EXITWINDOWS |
			windows.JOB_OBJECT_UILIMIT_GLOBALATOMS |
			windows.JOB_OBJECT_UILIMIT_HANDLES |
			windows.JOB_OBJECT_UILIMIT_READCLIPBOARD |
			windows.JOB_OBJECT_UILIMIT_SYSTEMPARAMETERS |
			windows.JOB_OBJECT_UILIMIT_WRITECLIPBOARD,
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectBasicUIRestrictions,
		uintptr(unsafe.Pointer(&restrictions)),
		uint32(unsafe.Sizeof(restrictions)),
	); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("configure job object UI restrictions: %w", err)
	}
	return job, nil
}

// securityCapabilities is SECURITY_CAPABILITIES from winnt.h. It names the
// container the process belongs to, and the capabilities it holds.
type securityCapabilities struct {
	appContainerSid *windows.SID
	capabilities    *windows.SIDAndAttributes
	capabilityCount uint32
	reserved        uint32
}
