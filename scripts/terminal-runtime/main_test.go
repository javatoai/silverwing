package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func testAssetHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func TestTerminalRuntimeStagesExactManifestLibraryAndWorksOffline(t *testing.T) {
	for _, target := range []string{"windows-amd64", "darwin-arm64"} {
		t.Run(target, func(t *testing.T) {
			library, license := []byte("isolated native fixture"), []byte("isolated license fixture")
			var requests atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/license" {
					w.Write(license)
				} else {
					w.Write(library)
				}
			}))
			defer server.Close()
			previousClient, previousLicense := http.DefaultClient, ghosttyLicense
			http.DefaultClient = server.Client()
			ghosttyLicense = pinnedFile{"LICENSE", server.URL + "/license", testAssetHash(license)}
			defer func() { http.DefaultClient = previousClient; ghosttyLicense = previousLicense }()
			name := "ghostty-vt.dll"
			if target == "darwin-arm64" {
				name = "libghostty-vt.dylib"
			}
			file := pinnedFile{name, server.URL + "/runtime", testAssetHash(library)}
			manifestData, _ := json.Marshal(map[string]any{"libraries": []any{map[string]any{"name": "libghostty-vt", "source": ghosttySource, "files": map[string]pinnedFile{target: file}}}})
			root := t.TempDir()
			manifest := filepath.Join(root, "manifest.json")
			os.WriteFile(manifest, manifestData, 0o644)
			resources, cache, notices := filepath.Join(root, "resources"), filepath.Join(root, "cache"), filepath.Join(root, "notices")
			if err := stageTerminalRuntime(context.Background(), manifest, target, resources, cache, notices); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(filepath.Join(resources, name)); err != nil || string(data) != string(library) {
				t.Fatal("runtime not in resources root", err)
			}
			if data, err := os.ReadFile(filepath.Join(notices, "LICENSE")); err != nil || string(data) != string(license) {
				t.Fatal("license absent", err)
			}
			server.Close()
			if err := stageTerminalRuntime(context.Background(), manifest, target, filepath.Join(root, "offline"), cache, filepath.Join(root, "offline-notices")); err != nil || requests.Load() != 2 {
				t.Fatal("verified cache must work offline", err, requests.Load())
			}
			os.WriteFile(filepath.Join(resources, name), []byte("untrusted existing payload"), 0o644)
			if err := stageTerminalRuntime(context.Background(), manifest, target, resources, cache, notices); err == nil {
				t.Fatal("corrupt reusable payload accepted")
			}
		})
	}
}

func TestTerminalRuntimeRejectsCorruptCacheAndDownload(t *testing.T) {
	root := t.TempDir()
	data := []byte("wanted")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("wrong binary")) }))
	defer server.Close()
	file := pinnedFile{"runtime", server.URL, testAssetHash(data)}
	if _, err := pinnedAsset(context.Background(), root, "download", file); err == nil {
		t.Fatal("bad download checksum accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "download")); !os.IsNotExist(err) {
		t.Fatal("bad download was cached")
	}
	os.WriteFile(filepath.Join(root, "corrupt"), []byte("wrong cache"), 0o644)
	if _, err := pinnedAsset(context.Background(), root, "corrupt", file); err == nil {
		t.Fatal("bad cache accepted")
	}
}

func TestTerminalRuntimeManifestRejectsInvalidTargetAndMetadata(t *testing.T) {
	valid := `{"libraries":[{"name":"libghostty-vt","source":"` + ghosttySource + `","files":{"windows-amd64":{"name":"ghostty-vt.dll","url":"https://example.invalid/runtime","sha256":"` + strings.Repeat("a", 64) + `"}}}]}`
	if _, err := readTerminalManifest([]byte(valid), "windows-amd64"); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{valid, strings.ReplaceAll(valid, "ghostty-vt.dll", "../bad.dll"), strings.ReplaceAll(valid, "https://example.invalid/runtime", "http://example.invalid/runtime"), strings.ReplaceAll(valid, strings.Repeat("a", 64), "bad"), strings.ReplaceAll(valid, ghosttySource, "other-source")} {
		target := "windows-amd64"
		if data == valid {
			target = "unknown"
		}
		if _, err := readTerminalManifest([]byte(data), target); err == nil {
			t.Fatal("invalid manifest accepted", data)
		}
	}
}

func TestTerminalRuntimeStagedFilesReadableByOtherUsers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics are verified on POSIX runners")
	}
	data := []byte("public runtime fixture")
	path := filepath.Join(t.TempDir(), "libghostty-vt.dylib")
	for _, existing := range []bool{false, true} {
		if existing {
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := writeVerified(path, data, testAssetHash(data)); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o644 {
			t.Fatal("packaged runtime must be readable across users", info, err)
		}
	}
}
