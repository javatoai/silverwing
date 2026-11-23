package auth

import (
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsTempAccountIsPrivateBeforeWriting(t *testing.T) {
	file, err := createPrivateTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatal("temporary file already contains data")
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	assertPrivateCredentialDACL(t, sd)
}

func TestWindowsResavingAccountReplacesLegacyPermissions(t *testing.T) {
	store := Store{Directory: t.TempDir()}
	legacy := []byte(`{"version":1,"username":"fixture","password":"synthetic-legacy"}`)
	if err := os.WriteFile(store.Path(), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + current.User.Sid.String() + ")(A;;FA;;;SY)(A;;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(store.Path(), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(Credentials{Username: "fixture", Password: "synthetic-legacy"}); err != nil {
		t.Fatal(err)
	}
	assertPrivateCredentialFile(t, store.Path())
}

func assertPrivateCredentialFile(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	assertPrivateCredentialDACL(t, sd)
}

func assertPrivateCredentialDACL(t *testing.T, sd *windows.SECURITY_DESCRIPTOR) {
	t.Helper()
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("Windows DACL inherits the parent directory", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatal("missing Windows DACL", err)
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	var userAllowed, systemAllowed bool
	for i := range int(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 {
			t.Fatal("unexpected Windows DACL entry type or inheritance")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		isUser, isSystem := sid.Equals(current.User.Sid), sid.IsWellKnown(windows.WinLocalSystemSid)
		if !isUser && !isSystem {
			t.Fatal("credential DACL grants access to an unexpected principal")
		}
		userAllowed, systemAllowed = userAllowed || isUser, systemAllowed || isSystem
	}
	if !userAllowed || !systemAllowed {
		t.Fatal("credential DACL does not grant both the current user and SYSTEM access")
	}
}
