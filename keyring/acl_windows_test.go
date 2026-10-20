//go:build windows

package keyring

import (
	"testing"
	"unsafe"

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
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	user, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}

	// Windows may assign the token's primary group as the file owner. The
	// protected DACL is the security boundary, so assert its exact trustees.
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
	for i := uint16(0); i < entries; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("ACL entry %d has type %d, want allowed", i, ace.Header.AceType)
		}
		trustee := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !windows.EqualSid(trustee, user) && !windows.EqualSid(trustee, system) {
			t.Fatalf("ACL entry %d grants access to unexpected SID %s", i, trustee.String())
		}
	}
}
