//go:build windows && (amd64 || arm64) && !mygo_nowebview

package windows

import "testing"

// TestLoaderVariables checks the user data folder and the arguments taken
// from WebView2's environment variables, as Microsoft's loader takes them.
// Only a relative folder asks for the executable's directory.
func TestLoaderVariables(t *testing.T) {
	const (
		exeDir   = `C:\Apps\MyGo`
		userData = `C:\Users\test\WebView2`
		args     = "--remote-debugging-port=0"
	)
	for _, tt := range []struct {
		name, folder, args, wantDir string
		wantExeDir                  bool
	}{
		{"neither variable", "", "", userData, false},
		{"absolute folder", `D:\Profiles\MyGo`, "", `D:\Profiles\MyGo`, false},
		{"relative folder", `profiles\test`, "", `C:\Apps\MyGo\profiles\test`, true},
		{"arguments only", "", args, userData, false},
		{"both", `profiles\test`, args, `C:\Apps\MyGo\profiles\test`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			vars := map[string]string{
				"WEBVIEW2_USER_DATA_FOLDER":             tt.folder,
				"WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS": tt.args,
			}
			asked := false
			dir, args := loaderVariables(func(key string) string { return vars[key] }, func() string {
				asked = true
				return exeDir
			}, userData)
			if dir != tt.wantDir || args != tt.args {
				t.Errorf("loaderVariables = (%q, %q), want (%q, %q)", dir, args, tt.wantDir, tt.args)
			}
			if asked != tt.wantExeDir {
				t.Errorf("asked for the executable's directory: %v, want %v", asked, tt.wantExeDir)
			}
		})
	}
}
