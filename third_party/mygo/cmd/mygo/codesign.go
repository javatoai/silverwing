package main

import (
	"os/exec"
	"time"
)

// codesignPatience is how long codesign signs before the build says what
// it may be waiting for. Signing an app takes a few seconds, most of them
// the timestamp server's.
const codesignPatience = 15 * time.Second

// codesignWaits returns the channel that tells that codesign has run for
// codesignPatience. Tests replace it.
var codesignWaits = func() <-chan time.Time { return time.After(codesignPatience) }

// signCode runs codesign with args, which sign what with identity, and
// returns what it printed.
//
// codesign asks for what it lacks in a dialog on the Mac's screen, the
// password of a locked keychain or leave to use the identity's key, and
// waits for the answer as long as it takes: forever where nobody sees the
// dialog, as behind a locked screen, over SSH or in CI. It prints nothing
// meanwhile, and neither it nor security tells whether a keychain is
// locked without asking for its password in turn. So the build says what
// codesign may be waiting for once it has run long, and goes on waiting.
// Ad hoc signing reads no keychain.
func signCode(what, identity string, args ...string) ([]byte, error) {
	cmd := exec.Command("codesign", args...)
	if identity == "-" {
		return cmd.CombinedOutput()
	}
	signed, told := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(told)
		select {
		case <-signed:
		case <-codesignWaits():
			warnf("codesign still signs %s after %.0f seconds. It may be waiting for an answer in a dialog on this Mac's screen: the password of a locked keychain, or leave to use the signing key.\nWhere nobody can answer, unlock the keychain before the build (security unlock-keychain) and let codesign use the key (security set-key-partition-list).", what, codesignPatience.Seconds())
		}
	}()
	out, err := cmd.CombinedOutput()
	close(signed)
	<-told
	return out, err
}
