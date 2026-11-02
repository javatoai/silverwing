// Command terminal-runtime stages the pinned MyGO terminal library for both
// native packaging scripts and local builds. The installed app never downloads it.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const ghosttySource = "https://github.com/ghostty-org/ghostty/tree/befcdfd2c3a1cb24d9ec886e93c95b2b5daa7028"

var ghosttyLicense = pinnedFile{
	Name:   "LICENSE",
	URL:    "https://raw.githubusercontent.com/ghostty-org/ghostty/befcdfd2c3a1cb24d9ec886e93c95b2b5daa7028/LICENSE",
	SHA256: "386211873e5b7a02f663ae4d7adf96285999f91608f8f9f31fecfd0f4095e6f1",
}

type pinnedFile struct{ Name, URL, SHA256 string }
type terminalManifest struct {
	Libraries []struct {
		Name, Source string
		Files        map[string]pinnedFile
	}
}

func main() {
	manifest := flag.String("manifest", "third_party/mygo/plugins/terminal/mygo-plugin.json", "MyGO terminal plugin manifest")
	target := flag.String("target", runtime.GOOS+"-"+runtime.GOARCH, "target OS-architecture")
	resources := flag.String("resources", "", "application resources root")
	cache := flag.String("cache", "build/native-cache", "verified download cache")
	notices := flag.String("notices", "", "temporary notice directory (default resources/licenses/ghostty-vt)")
	flag.Parse()
	if *resources == "" {
		fmt.Fprintln(os.Stderr, "terminal-runtime: -resources is required")
		os.Exit(1)
	}
	if *notices == "" {
		*notices = filepath.Join(*resources, "licenses", "ghostty-vt")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := stageTerminalRuntime(ctx, *manifest, *target, *resources, *cache, *notices); err != nil {
		fmt.Fprintln(os.Stderr, "terminal-runtime:", err)
		os.Exit(1)
	}
}

func readTerminalManifest(data []byte, target string) (pinnedFile, error) {
	var manifest terminalManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return pinnedFile{}, err
	}
	for _, library := range manifest.Libraries {
		if library.Name != "libghostty-vt" {
			continue
		}
		if library.Source != ghosttySource {
			return pinnedFile{}, fmt.Errorf("Ghostty source changed; update the pinned license alongside its runtime")
		}
		file, ok := library.Files[target]
		if !ok {
			return pinnedFile{}, fmt.Errorf("no terminal runtime for %s", target)
		}
		if file.Name == "" || strings.ContainsAny(file.Name, `/\`) || file.Name == "." || file.Name == ".." {
			return pinnedFile{}, fmt.Errorf("invalid library file name")
		}
		if sum, err := hex.DecodeString(file.SHA256); err != nil || len(sum) != sha256.Size {
			return pinnedFile{}, fmt.Errorf("invalid library SHA-256")
		}
		if !strings.HasPrefix(file.URL, "https://") {
			return pinnedFile{}, fmt.Errorf("runtime download must use HTTPS")
		}
		return file, nil
	}
	return pinnedFile{}, fmt.Errorf("terminal manifest has no libghostty-vt")
}

func stageTerminalRuntime(ctx context.Context, manifestPath, target, resources, cache, notices string) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	file, err := readTerminalManifest(data, target)
	if err != nil {
		return err
	}
	library, err := pinnedAsset(ctx, cache, target+"-"+file.SHA256+"-"+file.Name, file)
	if err != nil {
		return err
	}
	license, err := pinnedAsset(ctx, cache, "ghostty-"+ghosttyLicense.SHA256+"-LICENSE", ghosttyLicense)
	if err != nil {
		return err
	}
	if err := writeVerified(filepath.Join(resources, file.Name), library, file.SHA256); err != nil {
		return err
	}
	if err := writeVerified(filepath.Join(notices, "LICENSE"), license, ghosttyLicense.SHA256); err != nil {
		return err
	}
	source := fmt.Sprintf("libghostty-vt (%s)\nSource: %s\nAsset: %s\nSHA-256: %s\nLicense: %s\nLicense SHA-256: %s\n", target, ghosttySource, file.URL, file.SHA256, ghosttyLicense.URL, ghosttyLicense.SHA256)
	return os.WriteFile(filepath.Join(notices, "SOURCE.txt"), []byte(source), 0o644)
}

func pinnedAsset(ctx context.Context, cache, name string, file pinnedFile) ([]byte, error) {
	path := filepath.Join(cache, name)
	data, err := os.ReadFile(path)
	if err == nil {
		if !matchesSHA(data, file.SHA256) {
			return nil, fmt.Errorf("cached asset checksum mismatch: %s", path)
		}
		return data, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, file.URL, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", file.Name, response.StatusCode)
	}
	const maxAssetBytes = 64 << 20
	data, err = io.ReadAll(io.LimitReader(response.Body, maxAssetBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAssetBytes || !matchesSHA(data, file.SHA256) {
		return nil, fmt.Errorf("downloaded asset size/checksum mismatch: %s", file.Name)
	}
	if err := writeVerified(path, data, file.SHA256); err != nil {
		return nil, err
	}
	return data, nil
}

func matchesSHA(data []byte, want string) bool {
	sum := sha256.Sum256(data)
	return strings.EqualFold(hex.EncodeToString(sum[:]), want)
}

func writeVerified(path string, data []byte, expected string) error {
	if !matchesSHA(data, expected) {
		return fmt.Errorf("refusing unverified asset: %s", path)
	}
	if existing, err := os.ReadFile(path); err == nil {
		if matchesSHA(existing, expected) {
			return os.Chmod(path, 0o644)
		}
		return fmt.Errorf("existing runtime checksum mismatch: %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".terminal-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(data)
	if writeErr == nil {
		writeErr = tmp.Chmod(0o644)
	}
	closeErr := tmp.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp.Name(), path)
}
