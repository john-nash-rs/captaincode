//go:build windows

package keyring

import (
	"os"

	"golang.org/x/sys/windows"
)

// private restricts path to the current user and the system accounts.
//
// os.Chmod on Windows sets the read-only attribute and nothing else. The mode
// a file reports there is a compatibility view built from that one attribute
// and carries no permission meaning at all, so a credential written with 0600
// is readable by every account on the machine. The access control list is the
// real mechanism, and this is what replaces it.
//
// The list is protected, meaning it does not inherit from the directory above.
// Inheritance is exactly how the other accounts arrived on the file in the
// first place, so a list that still inherited would undo this function's own
// work on the next call.
func private(path string, mode os.FileMode) error {
	user, err := currentUser()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}

	// Two entries and no more: the account that owns the credential, and the
	// system accounts Windows needs to manage the file. Everyone else is
	// denied by the absence of an entry, which is what a protected list with
	// no inheritance gives.
	//
	// The list returned here lives on the Go heap: ACLFromEntries copies it
	// out of the Windows heap and frees that itself. It must not be handed to
	// LocalFree, which is what the Windows heap allocator expects and the Go
	// allocator does not provide; doing so corrupts the heap and takes the
	// process down with a heap-corruption exit long after this call returns.
	rights := accessFor(mode)
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		grant(rights, user),
		grant(rights, system),
	}, nil)
	if err != nil {
		return err
	}

	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
}

// grant is one allowed entry in the list.
func grant(rights windows.ACCESS_MASK, sid *windows.SID) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: rights,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

// accessFor translates a Unix mode into the access mask it promises: read and
// execute always, write when the mode asked for it.
//
// WRITE_DAC is included for both. Without it the owner cannot apply the list a
// second time, so the next Store would fail with an access denied that names
// the file the user has just asked to write to.
func accessFor(mode os.FileMode) windows.ACCESS_MASK {
	rights := windows.ACCESS_MASK(windows.FILE_GENERIC_READ |
		windows.FILE_GENERIC_EXECUTE |
		windows.READ_CONTROL |
		windows.WRITE_DAC |
		windows.DELETE)
	if mode&0o200 != 0 {
		rights |= windows.FILE_GENERIC_WRITE
	}
	return rights
}

// currentUser is the SID of the account running this process.
func currentUser() (*windows.SID, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}
