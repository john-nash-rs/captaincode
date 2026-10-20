//go:build windows

package keyring

import (
	"testing"

	"golang.org/x/sys/windows"
)

// The mode a file reports on Windows is a compatibility view of the read-only
// attribute and says nothing about who may read it, so the private check that
// runs everywhere else cannot run here. This reads the access control list
// instead, which is the thing that actually decides.
func TestKeyFileACLGivesOnlyTheCurrentUser(t *testing.T) {
	isolate(t)
	if err := Store("sk-or-v1-secret-value"); err != nil {
		t.Fatal(err)
	}
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}

	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		t.Fatal(err)
	}
	user, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	if !windows.EqualSid(owner, user) {
		t.Fatal("the key file is not owned by the current user")
	}

	// Two entries and no more: this user and SYSTEM. A third is another
	// account with a claim on the credential, which is what Store exists to
	// prevent and what os.Chmod, which only sets the read-only attribute,
	// never could.
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	var entries uint16
	if dacl != nil {
		entries = dacl.AceCount
	}
	if entries != 2 {
		t.Fatalf("the key file has %d ACL entries, want 2", entries)
	}
}
