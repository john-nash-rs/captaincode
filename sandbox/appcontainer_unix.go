//go:build !windows

package sandbox

// appContainerIdentity is the Windows container this harness would place a
// worker in. It is declared here so that the fields and functions that name it
// type-check on every platform, and it is never constructed: the Unix
// backends express their boundary on the command line, so there is nothing for
// an identity to identify.
type appContainerIdentity struct{}

func (a *appContainerIdentity) free() {}

// appContainerAvailable is only meaningful on Windows, where it reports
// whether the mechanism exists on this build of the OS. HasBackend never asks
// on this platform.
func appContainerAvailable() bool { return false }
