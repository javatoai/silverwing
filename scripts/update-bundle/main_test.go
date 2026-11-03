package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
	"silverwing-go/internal/appupdate"
	"silverwing-go/internal/productprofile"
)

func TestSigningRejectsReusablePayloadWithDifferentPinnedKey(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "updater.exe")
	command := exec.Command("go", "build", "-ldflags", "-X silverwing-go/internal/buildinfo.UpdatePublicKey=fixture-public-key", "-o", binary, "./cmd/silverwing-updater")
	command.Dir = filepath.Join("..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, output)
	}
	if err := checkPinnedBuild(binary, "fixture-public-key"); err != nil {
		t.Fatal(err)
	}
	if err := checkPinnedBuild(binary, "other-public-key"); err == nil {
		t.Fatal("reused payload with wrong embedded key accepted")
	}
}

func TestVerifySignedReleaseSetRejectsChangedArchiveAndWrongVersion(t *testing.T) {
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	root := t.TempDir()
	source := t.TempDir()
	os.WriteFile(filepath.Join(source, "fixture"), []byte("signed release"), 0o600)
	public := ""
	for _, kind := range []appupdate.Kind{appupdate.Setup, appupdate.MSI, appupdate.Portable, appupdate.MacApp} {
		platform := "windows/amd64"
		if kind == appupdate.MacApp {
			platform = "darwin/arm64"
		}
		target := appupdate.Target(platform, kind)
		name := productprofile.ExecutableName + "-1.0.1-" + target + ".tar.gz"
		size, sig, key, err := mygo.WriteUpdateArchive(filepath.Join(root, name), source, []string{"fixture"}, base64.StdEncoding.EncodeToString(private))
		if err != nil {
			t.Fatal(err)
		}
		public = key
		manifest := mygo.UpdateManifest{Version: "1.0.1", Size: size, Signature: sig, URL: "https://github.com/" + productprofile.ReleaseRepository + "/releases/download/" + productprofile.ReleaseTagPrefix + "1.0.1/" + name}
		if err := appupdate.WriteJSON(filepath.Join(root, "update-"+target+".json"), manifest); err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyBundles(root, "1.0.1", public); err != nil {
		t.Fatal(err)
	}
	if err := verifyBundles(root, "1.0.2", public); err == nil {
		t.Fatal("different release accepted")
	}
	archive := filepath.Join(root, productprofile.ExecutableName+"-1.0.1-windows-amd64-msi.tar.gz")
	data, _ := os.ReadFile(archive)
	data[len(data)/2] ^= 1
	os.WriteFile(archive, data, 0o600)
	if err := verifyBundles(root, "1.0.1", public); err == nil {
		t.Fatal("changed signed archive accepted")
	}
}
