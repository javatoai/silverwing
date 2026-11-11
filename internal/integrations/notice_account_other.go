//go:build !windows

package integrations

// Native keychains cannot be watched reliably without opening credentials.
// Keep the online account read on these platforms; encrypted-file and explicit
// environment/profile credentials can still supply a local change fence.
func noticeNativeCredentialStamp(string) (string, bool) { return "", false }
