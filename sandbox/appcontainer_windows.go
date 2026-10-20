//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows isolates a worker by putting it in an AppContainer. That is the one
// mechanism on the platform that removes the network without administrator
// rights, and it is also the strictest: an AppContainer is denied the user's
// files, the registry and other processes by default, and reaches only what a
// descriptor explicitly names it in.
//
// The consequence, and the reason this file is shaped the way it is, is that
// the container starts with nothing and everything it needs must be granted.
// Three grants cover it:
//
//   - the workspace, so the worker can do the job it was asked to do;
//   - the directory holding the worker binary, so the OS can load it;
//   - nothing else. Windows already grants every AppContainer read and execute
//     access to System32 and the system libraries, which is why cmd.exe and
//     the standard tools run inside without any grant of ours.
//
// This is where the two modes stop being alike. macOS expresses "may write"
// as a line in a profile and Linux as a different bind flag; both are decided
// while building a command line. An AppContainer cannot be written into after
// it starts, so here "may write" is an access control entry on the directory,
// placed before the process exists and removed after it is gone.
//
// Writes are refused when the write hole cannot be cut without the
// administrator. An operator is told why rather than being handed a worker
// that can write outside its workspace.

// appContainerName is the profile this harness creates. The name is the
// identity: the same name always derives the same SID, so the profile is
// created once and reused by every later run, and a second machine or a
// second user gets a different SID from the same code.
const appContainerName = "CaptainCode.ToolWorker"

// hresultAlreadyExists is returned by CreateAppContainerProfile when the
// profile is already there, which is the ordinary case after the first run.
const hresultAlreadyExists = 0x800700b7

// appContainerIdentity is the identity of the container, and the only way to
// release it.
type appContainerIdentity struct {
	sid *windows.SID
}

func (a *appContainerIdentity) free() {
	if a == nil || a.sid == nil {
		return
	}
	// The SID this ends up holding came from CreateAppContainerProfile or
	// DeriveAppContainerSidFromAppContainerName, both of which allocate from
	// the process heap and expect the caller to release it. It is not the
	// same kind of value that ACLFromEntries returns, which is why the free
	// lives here and not at every use.
	_ = windows.FreeSid(a.sid)
	a.sid = nil
}

var (
	moduserenv                        = windows.NewLazySystemDLL("userenv.dll")
	procCreateAppContainerProfile     = moduserenv.NewProc("CreateAppContainerProfile")
	procDeriveAppContainerSidFromName = moduserenv.NewProc("DeriveAppContainerSidFromAppContainerName")
)

// appContainerAvailable reports whether the two userenv entry points this
// relies on are present. Both exist from Windows 8, so a false here is a
// machine older than the mechanism rather than a missing dependency.
func appContainerAvailable() bool {
	for _, proc := range []*windows.LazyProc{
		procCreateAppContainerProfile,
		procDeriveAppContainerSidFromName,
	} {
		if err := proc.Find(); err != nil {
			return false
		}
	}
	return true
}

// openAppContainer returns the container's SID, creating the profile the
// first time and deriving it afterwards.
func openAppContainer() (*appContainerIdentity, error) {
	if !appContainerAvailable() {
		return nil, errors.New("appcontainer support is unavailable on this Windows version")
	}
	name, err := windows.UTF16PtrFromString(appContainerName)
	if err != nil {
		return nil, err
	}
	var sid *windows.SID
	// CreateAppContainerProfile takes a display name, a description and a
	// package name. The container has no user interface, so all three are the
	// same string: it is an identifier, not something anyone will read.
	result, _, _ := procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(name)),
		0,
		0,
		uintptr(unsafe.Pointer(&sid)),
	)
	switch uint32(result) {
	case 0:
		return &appContainerIdentity{sid: sid}, nil
	case hresultAlreadyExists:
		// The profile outlives every run, so this is the normal path.
	default:
		return nil, fmt.Errorf("create appcontainer profile: HRESULT 0x%08x", uint32(result))
	}
	// Derived rather than found. Windows stores the SID in the profile, and a
	// derivation that disagrees with it would hand this run an identity the
	// profile's own access entries do not name.
	result, _, _ = procDeriveAppContainerSidFromName.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&sid)),
	)
	if result != 0 {
		return nil, fmt.Errorf("derive appcontainer sid: HRESULT 0x%08x", uint32(result))
	}
	return &appContainerIdentity{sid: sid}, nil
}

// cageACL is where the boundary is actually drawn. AppContainer access rules
// use an allowlist: package SID deny ACEs are not a reliable way to subtract
// access already granted elsewhere, so secret paths are never granted access.
// It returns every path changed so cleanup removes only this run's entries.
func cageACL(root string, sid *appContainerIdentity, writes bool) ([]string, error) {
	var changed []string
	var err error
	if writes {
		changed, err = grantContainerWrite(root, sid)
	} else {
		changed, err = grantContainerRead(root, sid)
	}
	if err != nil {
		revokeContainerPaths(changed, sid)
		return nil, err
	}
	return changed, nil
}

// cageCleanup undoes whatever cageACL and grantExecutable did.
func cageCleanup(_ string, sid *appContainerIdentity, changed []string, executableGrant string) {
	revokeContainerPaths(changed, sid)
	if executableGrant != "" {
		for _, subject := range containerAccessSIDs(sid) {
			_ = revokeEntry(executableGrant, subject)
		}
	}
}

// containerAccessSIDs is the set of identities an access entry is written
// against.
//
// Only the container's own SID is granted access to the workspace. Granting
// ALL APPLICATION PACKAGES here would give every AppContainer the same access.
func containerAccessSIDs(sid *appContainerIdentity) []*windows.SID {
	return []*windows.SID{sid.sid}
}

// grantContainerRead gives access only to existing workspace objects that are
// outside the credential paths. The root ACE is not inherited, so it cannot
// silently grant access to a secret directory beneath it.
func grantContainerRead(root string, sid *appContainerIdentity) ([]string, error) {
	mask := windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE)
	return grantWorkspace(root, sid, mask)
}

// grantContainerWrite gives write access only to workspace objects outside
// the credential paths. Per-directory inheritance lets the worker use files
// it creates beneath an allowed directory; the root entry is non-inheriting.
func grantContainerWrite(root string, sid *appContainerIdentity) ([]string, error) {
	return grantWorkspace(root, sid, windows.GENERIC_ALL)
}

func grantWorkspace(root string, sid *appContainerIdentity, mask windows.ACCESS_MASK) ([]string, error) {
	var changed []string
	grant := func(path string, isDir bool) error {
		if err := setContainerEntries(path, containerAccessSIDs(sid), windows.GRANT_ACCESS, mask, isDir); err != nil {
			return err
		}
		changed = append(changed, path)
		return nil
	}
	if err := grant(root, false); err != nil {
		return changed, err
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		first := strings.SplitN(relative, string(os.PathSeparator), 2)[0]
		for _, secret := range secretNames {
			if strings.EqualFold(first, secret) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		return grant(path, entry.IsDir())
	})
	if err != nil {
		return changed, fmt.Errorf("grant workspace access: %w", err)
	}
	return changed, nil
}

// secretNames are the directories inside a workspace that hold credentials and
// are none of a tool call's business.
//
// They are the same names the other two backends refuse, which is deliberate:
// a rule that applies on two platforms and not the third is a rule an operator
// cannot rely on.
var secretNames = []string{".git", ".env", ".ssh", ".aws", ".codex"}

func revokeContainerPaths(paths []string, sid *appContainerIdentity) {
	for _, path := range paths {
		for _, subject := range containerAccessSIDs(sid) {
			_ = revokeEntry(path, subject)
		}
	}
}

// executableGrant names the container in the directory holding the worker
// binary.
//
// Windows grants every AppContainer read and execute in System32 and the
// system library directories, so a worker installed there needs nothing. A
// worker beside a checkout does not, and without this the OS refuses to load
// the binary and the operator sees the sandbox fail for a reason that has
// nothing to do with the sandbox.
//
// It returns the directory it touched, or "" when none was needed, so the
// caller knows what to undo.
func grantExecutable(executable string, sid *appContainerIdentity) (string, error) {
	directory := filepath.Dir(executable)
	if directory == "." || directory == "" {
		return "", nil
	}
	if isSystemDirectory(directory) {
		return "", nil
	}
	if err := setContainerEntries(directory, []*windows.SID{sid.sid}, windows.GRANT_ACCESS,
		windows.FILE_GENERIC_READ|windows.FILE_GENERIC_EXECUTE, true); err != nil {
		return "", err
	}
	return directory, nil
}

// isSystemDirectory reports whether a path is one Windows already grants every
// AppContainer.
//
// This is not an optimisation. A grant here is temporary by design, and the
// matching removal names only the container's SID, so touching a system
// directory would be harmless — but the system directories' own factory
// entries are not ours, and the fewer of them this process rewrites the
// better. Membership is by path so that it cannot change depending on whether
// the operator happens to be elevated.
func isSystemDirectory(path string) bool {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = os.Getenv("WINDIR")
	}
	if root == "" {
		root = `C:\Windows`
	}
	windows := filepath.Join(root, "System32")
	programFiles := os.Getenv("ProgramFiles")
	programFilesX86 := os.Getenv("ProgramFiles(x86)")
	for _, candidate := range []string{windows, programFiles, programFilesX86} {
		if candidate == "" {
			continue
		}
		cleaned := filepath.Clean(candidate)
		if strings.EqualFold(filepath.Clean(path), cleaned) {
			return true
		}
		// The whole subtree, not only the directory itself: System32\x is as
		// much the system's as System32 is.
		if strings.HasPrefix(strings.ToLower(filepath.Clean(path)), strings.ToLower(cleaned)+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// setContainerEntries writes an access entry for each subject on one path.
func setContainerEntries(path string, subjects []*windows.SID, mode windows.ACCESS_MODE, mask windows.ACCESS_MASK, inherit bool) error {
	entries := make([]windows.EXPLICIT_ACCESS, 0, len(subjects))
	// The trustees hold pointers into these SIDs, and the standard library is
	// explicit that such a pointer must be pinned for as long as the trustee
	// lives. Without this the collector may move a SID while the call that
	// reads it is running, which on a security descriptor is not a crash but a
	// grant written for the wrong identity.
	var pinner runtime.Pinner
	defer pinner.Unpin()
	held := make([]*windows.SID, 0, len(subjects))
	var pinned []*windows.SID
	for _, subject := range subjects {
		if subject == nil {
			continue
		}
		held = append(held, subject)
		pinned = append(pinned, subject)
		inheritance := uint32(windows.NO_INHERITANCE)
		if inherit {
			inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
		}
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: mask,
			AccessMode:        mode,
			Inheritance:       inheritance,
			Trustee:           trusteeFor(subject),
		})
	}
	if len(entries) == 0 {
		return nil
	}
	pinner.Pin(&pinned[0])
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read access list of %s: %w", path, err)
	}
	var existing *windows.ACL
	if descriptor != nil {
		existing, _, err = descriptor.DACL()
		if err != nil && !errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) {
			return fmt.Errorf("read access list of %s: %w", path, err)
		}
	}
	// ACLFromEntries merges the new entries with the ones already there, which
	// is what makes this additive: the operator's own access is untouched, and
	// cleanup removes only what was added.
	//
	// The list it returns is on the Go heap. It must not be passed to
	// LocalFree, which is what the Windows heap expects and the Go allocator
	// does not provide.
	acl, err := windows.ACLFromEntries(entries, existing)
	if err != nil {
		return fmt.Errorf("build access list for %s: %w", path, err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return fmt.Errorf("set access list on %s: %w", path, err)
	}
	runtime.KeepAlive(held)
	return nil
}

// trusteeFor is the one way a trustee is built in this file, so that the
// pinning requirement is in one place rather than restated at each entry.
func trusteeFor(sid *windows.SID) windows.TRUSTEE {
	return windows.TRUSTEE{
		TrusteeForm:  windows.TRUSTEE_IS_SID,
		TrusteeType:  windows.TRUSTEE_IS_USER,
		TrusteeValue: windows.TrusteeValueFromSID(sid),
	}
}

// revokeEntry removes this worker's entries for a subject from a path.
//
// The OS is asked to do the removal rather than this function walking the list
// and rebuilding it. Enumerating the entries by hand means reading raw ACE
// memory and having to understand every entry type that might be present, and
// the types this harness never writes — an audit entry, a callback entry — are
// exactly the ones a hand-written loop is most likely to drop.
//
// ACLFromEntries with REVOKE_ACCESS merges the request into the list already
// there and returns one with those entries gone and everything else untouched.
// Both inheritance variants are named, because the entry being removed may
// have been written as either and the request only matches the shape it
// describes.
func revokeEntry(path string, subject *windows.SID) error {
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(subject)

	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read access list of %s: %w", path, err)
	}
	var existing *windows.ACL
	if descriptor != nil {
		existing, _, err = descriptor.DACL()
		if err != nil && !errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) {
			return fmt.Errorf("read access list of %s: %w", path, err)
		}
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		{
			// REVOKE_ACCESS matches permissions, rather than treating
			// GENERIC_ALL as a wildcard. Entries written here include both
			// generic rights and file-specific masks, so request every bit
			// when removing this dedicated AppContainer SID's entries.
			AccessPermissions: windows.ACCESS_MASK(0xffffffff),
			AccessMode:        windows.REVOKE_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee:           trusteeFor(subject),
		},
		{
			AccessPermissions: windows.ACCESS_MASK(0xffffffff),
			AccessMode:        windows.REVOKE_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee:           trusteeFor(subject),
		},
	}, existing)
	if err != nil {
		return fmt.Errorf("rebuild access list for %s: %w", path, err)
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
