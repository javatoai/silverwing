package platform

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"silverwing-go/internal/appupdate"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAppUpdateSourceUsesMatchingSignedBundleAndRejectsWrongVersion(t *testing.T) {
	root := t.TempDir()
	payload := filepath.Join(root, "payload")
	os.Mkdir(payload, 0o700)
	os.WriteFile(filepath.Join(payload, "app.exe"), []byte("new"), 0o700)
	os.WriteFile(filepath.Join(payload, "helper.exe"), []byte("helper"), 0o700)
	identity := appupdate.Identity{Repository: "owner/repo", TagPrefix: "v", Executable: "app", Platform: "windows/amd64", Version: "1.0.1"}
	files, _ := appupdate.Inventory(payload, "")
	layout := appupdate.Layout{Schema: 1, Identity: identity, Executable: "app.exe", Helper: "helper.exe", Files: files}
	metadata := appupdate.Metadata{Schema: 1, Identity: identity, Kind: appupdate.Portable, Payload: "payload", Layout: &layout}
	if err := appupdate.WriteJSON(filepath.Join(root, appupdate.MetadataName), metadata); err != nil {
		t.Fatal(err)
	}
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	archive := filepath.Join(t.TempDir(), "archive.tar.gz")
	size, signature, public, err := mygo.WriteUpdateArchive(archive, root, []string{"payload", appupdate.MetadataName}, base64.StdEncoding.EncodeToString(private))
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := os.ReadFile(archive)
	manifest := mygo.UpdateManifest{Version: "1.0.1", URL: "https://github.com/owner/repo/releases/download/v1.0.1/app-1.0.1-windows-amd64-portable.tar.gz", Size: size, Signature: signature}
	requests := []string{}
	client := &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.String())
		body := blob
		if strings.HasSuffix(r.URL.Path, ".json") {
			body, _ = json.Marshal(manifest)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}
	old := identity
	old.Version = "1.0.0"
	i := appupdate.Installation{Identity: old, Kind: appupdate.Portable}
	clientSnapshots := 0
	source := appUpdateSource{publicKey: public, newClient: func() (*http.Client, error) { clientSnapshots++; return client, nil }}
	bundle, err := source.Prepare(context.Background(), i, "1.0.1", filepath.Join(t.TempDir(), "prepared"), nil)
	if err != nil || bundle.Metadata.Identity.Version != "1.0.1" || len(requests) != 2 || clientSnapshots != 1 {
		t.Fatalf("signed source %+v %v requests=%v", bundle, err, requests)
	}
	manifest.Version = "8.0.0"
	if _, err := source.Prepare(context.Background(), i, "1.0.1", filepath.Join(t.TempDir(), "wrong-version"), nil); err == nil {
		t.Fatal("mismatched manifest version accepted")
	}
	manifest.Version = "1.0.1"
	blob = blob[:len(blob)/2]
	if _, err := source.Prepare(context.Background(), i, "1.0.1", filepath.Join(t.TempDir(), "interrupted"), nil); err == nil {
		t.Fatal("interrupted download accepted")
	}
	if clientSnapshots != 3 {
		t.Fatal("download reused a stale proxy snapshot")
	}
}
