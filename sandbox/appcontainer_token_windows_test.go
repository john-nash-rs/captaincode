//go:build windows

package sandbox

import (
	"context"
	"testing"
	"time"
	"unsafe"

	"github.com/john-nash-rs/captaincode/toolbox"
	"golang.org/x/sys/windows"
)

// TestWorkerHasExpectedAppContainerIdentity checks the token Windows actually
// creates for the child. ACL enforcement depends on this SID being the same
// identity that cageACL names on disk.
func TestWorkerHasExpectedAppContainerIdentity(t *testing.T) {
	root := t.TempDir()
	worker := Worker{Executable: shellFor(t), Registry: toolbox.Registry{Root: root, MaxBytes: 512}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command, err := worker.Command(ctx, shellRun(t, "probe", "exit 0"))
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var token windows.Token
	if err := windows.OpenProcessToken(windows.Handle(command.state.process), windows.TOKEN_QUERY, &token); err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	var isAppContainer uint32
	var returned uint32
	if err := windows.GetTokenInformation(token, 29, (*byte)(unsafe.Pointer(&isAppContainer)), uint32(unsafe.Sizeof(isAppContainer)), &returned); err != nil {
		t.Fatal(err)
	}
	if isAppContainer == 0 {
		t.Fatal("worker token is not an AppContainer token")
	}
	type appContainerInfo struct{ sid *windows.SID }
	var info appContainerInfo
	if err := windows.GetTokenInformation(token, 31, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), &returned); err != nil {
		t.Fatal(err)
	}
	if info.sid == nil {
		t.Fatal("worker token has no AppContainer SID")
	}
	actual := info.sid.String()
	expected := command.container.sid.String()
	if actual != expected {
		t.Errorf("worker AppContainer SID %s differs from ACL SID %s", actual, expected)
	}
	if err := command.Wait(); err != nil {
		t.Errorf("worker exit: %v", err)
	}
}
