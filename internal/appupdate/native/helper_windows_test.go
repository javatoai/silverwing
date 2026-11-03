//go:build windows

package native

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"silverwing-go/internal/appupdate"
	"silverwing-go/internal/buildinfo"
)

func fixtureExecutable(t *testing.T, revision string) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	binary := filepath.Join(root, "fixture.exe")
	program := `package main
import("flag";"os";"time")
var revision string
func main(){ home:=flag.String("home","",""); _=home; ready:=flag.String("update-health","",""); token:=flag.String("update-token","",""); flag.String("update-result","",""); wait:=flag.Duration("wait",0,""); flag.Parse(); time.Sleep(*wait); if revision=="fail" {os.Exit(3)}; if *ready!="" {if os.WriteFile(*ready,[]byte(*token),0600)!=nil {os.Exit(4)}; time.Sleep(50*time.Millisecond)} }
`
	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-ldflags", "-X main.revision="+revision, "-o", binary, source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %s %v", output, err)
	}
	return binary
}

func helperProgramLayout(t *testing.T, root, executable, version string) appupdate.Layout {
	t.Helper()
	os.MkdirAll(filepath.Join(root, "resources"), 0o700)
	if err := appupdate.CopyFile(executable, filepath.Join(root, "fixture.exe"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "helper.exe"), []byte("fixture helper"), 0o700)
	identity := Identity()
	identity.Version = version
	files, err := appupdate.Inventory(root, "resources/update-installation.json")
	if err != nil {
		t.Fatal(err)
	}
	layout := appupdate.Layout{Schema: 1, Identity: identity, Executable: "fixture.exe", Helper: "helper.exe", Files: files}
	if err := appupdate.WriteJSON(filepath.Join(root, "resources/update-installation.json"), layout); err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestNativePortableHelperUpgradeAndFailedStartupRollback(t *testing.T) {
	oldBinary := fixtureExecutable(t, "old")
	goodBinary := fixtureExecutable(t, "new")
	badBinary := fixtureExecutable(t, "fail")
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	savedKey := buildinfo.UpdatePublicKey
	buildinfo.UpdatePublicKey = base64.StdEncoding.EncodeToString(public)
	defer func() { buildinfo.UpdatePublicKey = savedKey }()
	for _, scenario := range []struct {
		name, binary string
		success      bool
	}{{"upgrade", goodBinary, true}, {"rollback", badBinary, false}} {
		t.Run(scenario.name, func(t *testing.T) {
			home := t.TempDir()
			root := filepath.Join(t.TempDir(), "program")
			layout := helperProgramLayout(t, root, oldBinary, buildinfo.Version)
			os.WriteFile(filepath.Join(root, "user.md"), []byte("private user file"), 0o600)
			installation, err := Detect(filepath.Join(root, "fixture.exe"), home, buildinfo.UpdatePublicKey, Identity())
			if err != nil {
				t.Fatal(err)
			}
			if installation.Kind != appupdate.Portable {
				t.Skip("fixture must not be treated as a registered real installation")
			}
			os.MkdirAll(Cache(home), 0o700)
			job, err := os.MkdirTemp(Cache(home), "job-")
			if err != nil {
				t.Fatal(err)
			}
			os.Mkdir(filepath.Join(job, "new"), 0o700)
			archiveRoot := t.TempDir()
			payload := filepath.Join(archiveRoot, "payload")
			next := helperProgramLayout(t, payload, scenario.binary, "99.0.0")
			next.Files, _ = appupdate.Inventory(payload, "")
			metadata := appupdate.Metadata{Schema: 1, Identity: next.Identity, Kind: appupdate.Portable, Payload: "payload", Layout: &next}
			if err := appupdate.WriteJSON(filepath.Join(archiveRoot, appupdate.MetadataName), metadata); err != nil {
				t.Fatal(err)
			}
			archive := filepath.Join(job, "new", "update.tar.gz")
			size, signature, _, err := mygo.WriteUpdateArchive(archive, archiveRoot, []string{"payload", appupdate.MetadataName}, base64.StdEncoding.EncodeToString(private))
			if err != nil {
				t.Fatal(err)
			}
			parent := exec.Command(installation.Executable, "--wait", "250ms")
			hide(parent)
			if err := parent.Start(); err != nil {
				t.Fatal(err)
			}
			defer parent.Wait()
			layoutHash, _ := appupdate.Digest(installation.LayoutPath)
			exeHash, _ := appupdate.Digest(installation.Executable)
			plan := appupdate.Plan{Schema: 1, ID: "0123456789abcdef0123456789abcdef", Installation: installation, ParentPID: parent.Process.Pid, HealthToken: "0123456789abcdef0123456789abcdef", Backup: filepath.Join(job, "backup"), LayoutDigest: layoutHash, ExecutableDigest: exeHash, New: appupdate.Bundle{Archive: archive, Size: size, Signature: signature, Metadata: metadata}}
			if err := appupdate.WriteJSON(filepath.Join(job, "plan.json"), plan); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			err = RunHelper(ctx, filepath.Join(job, "plan.json"))
			if (err == nil) != scenario.success {
				t.Fatalf("helper success=%t err=%v", scenario.success, err)
			}
			var current appupdate.Layout
			if e := appupdate.ReadJSON(installation.LayoutPath, &current); e != nil {
				t.Fatal(e)
			}
			want := "99.0.0"
			if !scenario.success {
				want = layout.Identity.Version
			}
			if current.Identity.Version != want {
				t.Fatalf("installed version=%s want=%s", current.Identity.Version, want)
			}
			if content, _ := os.ReadFile(filepath.Join(root, "user.md")); string(content) != "private user file" {
				t.Fatal("helper touched user content")
			}
			outcome, e := ReadOutcome(home, filepath.Join(job, "result.json"))
			if e != nil || outcome.Success != scenario.success {
				t.Fatalf("outcome %+v %v", outcome, e)
			}
		})
	}
}
