package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type codesignFixture struct{ calls, fail int }

func (r *codesignFixture) Run(context.Context, string, []string) (int, error) {
	r.calls++
	if r.calls == r.fail {
		return 1, nil
	}
	return 0, nil
}

func TestMacWholeAppReplacementRollbackAndSignatureRefusal(t *testing.T) {
	for _, fail := range []int{0, 1, 2} {
		p := portablePlan(t)
		old := p.Installation.Root
		p.Installation.Root = old + ".app"
		if err := os.Rename(old, p.Installation.Root); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(p.Installation.Root, "user.txt"), []byte("old app extra"), 0o600)
		runner := &codesignFixture{fail: fail}
		adapter := Mac{Runner: runner}
		err := adapter.Apply(context.Background(), p)
		if (err != nil) != (fail != 0) {
			t.Fatalf("fail=%d: %v", fail, err)
		}
		if err := adapter.Rollback(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		if content, _ := os.ReadFile(filepath.Join(p.Installation.Root, "app.exe")); string(content) != "1.0.0" {
			t.Fatal("original app was not restored")
		}
		if content, _ := os.ReadFile(filepath.Join(p.Installation.Root, "user.txt")); string(content) != "old app extra" {
			t.Fatal("old app contents lost")
		}
	}
}
