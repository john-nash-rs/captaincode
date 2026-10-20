//go:build !windows

package keyring

import "os"

// private restricts path to its owner. On Unix the mode is the mechanism, and
// there is nothing more precise to reach for.
func private(path string, mode os.FileMode) error {
	return os.Chmod(path, mode)
}
