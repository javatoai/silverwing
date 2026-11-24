//go:build windows

package platform

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func newCLIUserPathTestKey(t *testing.T) (registry.Key, string) {
	t.Helper()
	path := `Software\SilverwingTestCLIUserPath-` + rand.Text()
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := key.Close(); err != nil {
			t.Error(err)
		}
		if err := registry.DeleteKey(registry.CURRENT_USER, path); err != nil {
			t.Error(err)
		}
	})
	return key, path
}

func TestCLIUserPathPreservesEntriesAndRegistryType(t *testing.T) {
	const directory = `C:\Developer Tools\cli-list`
	const path = `"C:\Developer Tools\bin";;%SW_CLI_TEST_TOOLS%\bin;`
	t.Setenv("SW_CLI_TEST_TOOLS", `C:\Developer Tools`)
	for _, tc := range []struct {
		name string
		kind uint32
		old  string
		want string
	}{
		{"first install", registry.NONE, "", directory},
		{"preserve strings", registry.SZ, path, path + directory},
		{"preserve expandable entries", registry.EXPAND_SZ, path, path + directory},
		{"expandable duplicate", registry.EXPAND_SZ, `%SW_CLI_TEST_TOOLS%\cli-list`, `%SW_CLI_TEST_TOOLS%\cli-list`},
		{"literal variable spelling", registry.SZ, `%SW_CLI_TEST_TOOLS%\cli-list`, `%SW_CLI_TEST_TOOLS%\cli-list;` + directory},
		{"quoted case insensitive duplicate", registry.SZ, `"c:/developer tools/CLI-LIST/"`, `"c:/developer tools/CLI-LIST/"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, _ := newCLIUserPathTestKey(t)
			var err error
			switch tc.kind {
			case registry.SZ:
				err = key.SetStringValue("Path", tc.old)
			case registry.EXPAND_SZ:
				err = key.SetExpandStringValue("Path", tc.old)
			}
			if err != nil {
				t.Fatal(err)
			}
			changed, err := appendCLIUserPathValue(context.Background(), key, directory)
			if err != nil || changed != (tc.old != tc.want) {
				t.Fatalf("changed = %v, error = %v", changed, err)
			}
			actual, kind, err := key.GetStringValue("Path")
			wantKind := tc.kind
			if wantKind == registry.NONE {
				wantKind = registry.SZ
			}
			if err != nil || actual != tc.want || kind != wantKind {
				t.Fatalf("PATH = %q, registry type %d, error = %v", actual, kind, err)
			}
			if changed, err := appendCLIUserPathValue(context.Background(), key, directory); err != nil || changed {
				t.Fatalf("reinstall changed PATH again, changed = %v, error = %v", changed, err)
			}
		})
	}
}

func TestCLIUserPathRejectsInvalidRegistryValueWithoutReplacingIt(t *testing.T) {
	key, _ := newCLIUserPathTestKey(t)
	if err := key.SetDWordValue("Path", 123); err != nil {
		t.Fatal(err)
	}
	if changed, err := appendCLIUserPathValue(context.Background(), key, `C:\cli-list`); !errors.Is(err, registry.ErrUnexpectedType) || changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	value, kind, err := key.GetIntegerValue("Path")
	if err != nil || value != 123 || kind != registry.DWORD {
		t.Fatalf("old PATH value lost, value = %d, registry type = %d, error = %v", value, kind, err)
	}
}

func TestCLIUserPathRejectsTooLongAndCanceledUpdates(t *testing.T) {
	key, _ := newCLIUserPathTestKey(t)
	path := `C:\` + strings.Repeat("字", 32760)
	if err := key.SetStringValue("Path", path); err != nil {
		t.Fatal(err)
	}
	if changed, err := appendCLIUserPathValue(context.Background(), key, `C:\cli-list`); err == nil || changed {
		t.Fatalf("accepted overlong PATH, changed = %v, error = %v", changed, err)
	}
	if actual, _, err := key.GetStringValue("Path"); err != nil || actual != path {
		t.Fatal("old PATH value lost")
	}
	if err := key.SetStringValue("Path", `C:\System\bin`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if changed, err := appendCLIUserPathValue(ctx, key, `C:\cli-list`); !errors.Is(err, context.Canceled) || changed {
		t.Fatalf("accepted canceled PATH update, changed = %v, error = %v", changed, err)
	}
}

func TestCLIUserPathPreservesValueWhenRegistryWriteIsDenied(t *testing.T) {
	key, path := newCLIUserPathTestKey(t)
	const original = `C:\System\bin`
	if err := key.SetStringValue("Path", original); err != nil {
		t.Fatal(err)
	}
	readOnly, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	if changed, err := appendCLIUserPathValue(context.Background(), readOnly, `C:\cli-list`); !errors.Is(err, windows.ERROR_ACCESS_DENIED) || changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	if actual, _, err := key.GetStringValue("Path"); err != nil || actual != original {
		t.Fatal("old PATH value lost")
	}
}

func TestCLIPathContainsUNCShareRoot(t *testing.T) {
	if !cliPathContainsDirectory(`"\\Host\Share\";C:\System\bin`, `\\host\share`) {
		t.Fatal("UNC share root with trailing separator was not recognized")
	}
}
