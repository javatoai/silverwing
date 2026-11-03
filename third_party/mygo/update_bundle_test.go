//go:build !js

package mygo

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type preparedUpdateSource struct{ manifest, archive []byte }

func (s preparedUpdateSource) Manifest(context.Context, bool) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.manifest)), nil
}
func (s preparedUpdateSource) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.archive)), nil
}

func TestPrepareUpdateStagesVerifiedBytesWithoutInstalling(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "installed.txt")
	if err := os.WriteFile(old, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(root, "payload")
	os.Mkdir(payload, 0o700)
	os.WriteFile(filepath.Join(payload, "app"), []byte("new application"), 0o700)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "update.tar.gz")
	size, signature, public, err := WriteUpdateArchive(archive, payload, []string{"app"}, base64.StdEncoding.EncodeToString(private))
	if err != nil {
		t.Fatal(err)
	}
	manifest := UpdateManifest{Version: "1.2.3", URL: "test://bundle", Size: size, Signature: signature}
	data, _ := json.Marshal(manifest)
	blob, _ := os.ReadFile(archive)
	source := preparedUpdateSource{data, blob}
	prepared, err := PrepareUpdate(context.Background(), source, public, filepath.Join(root, "stage"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(filepath.Join(prepared.Contents, "app")); string(content) != "new application" {
		t.Fatal("payload not prepared")
	}
	if content, _ := os.ReadFile(old); string(content) != "unchanged" {
		t.Fatal("preparing modified the installation")
	}
	if err := ExtractVerifiedUpdateArchive(prepared.Archive, public, manifest, filepath.Join(root, "verified")); err != nil {
		t.Fatal(err)
	}
	blob[len(blob)/2] ^= 0x40
	if _, err := PrepareUpdate(context.Background(), preparedUpdateSource{data, blob}, public, filepath.Join(root, "bad"), nil); err == nil {
		t.Fatal("bad signature accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PrepareUpdate(ctx, source, public, filepath.Join(root, "cancelled"), nil); err == nil {
		t.Fatal("cancelled prepare succeeded")
	}
	if _, err := os.Stat(filepath.Join(root, "cancelled")); !os.IsNotExist(err) {
		t.Fatal("cancelled prepare wrote staging files")
	}
}
