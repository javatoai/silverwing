// Command licenses copies the license and notice files for the Go modules used
// by the shipped binaries. It deliberately fails if a dependency module has no
// recognizable license file, so packages cannot silently omit a notice.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type moduleInfo struct {
	Path string
	Dir  string
}

type packageInfo struct {
	Module *moduleInfo
}

type licensedModule struct {
	Path string
	Dir  string
}

var licenseName = regexp.MustCompile(`(?i)^(license|copying|notice|copyright)([._-].*)?$`)

func main() {
	out := flag.String("out", "", "output directory for copied notices")
	tags := flag.String("tags", "mygo_noinspector", "Go build tags used by the shipped binaries")
	flag.Parse()
	if *out == "" || flag.NArg() == 0 {
		fatal(errors.New("usage: go run ./scripts/licenses -out <directory> [-tags tag1,tag2] <package> [<package>...]"))
	}
	if err := collect(*out, *tags, flag.Args()); err != nil {
		fatal(err)
	}
}

func collect(out, tags string, packages []string) error {
	args := []string{"list", "-deps", "-json"}
	if strings.TrimSpace(tags) != "" {
		args = append(args, "-tags="+strings.TrimSpace(tags))
	}
	args = append(args, packages...)
	cmd := exec.Command("go", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start go list: %w", err)
	}
	modules := map[string]licensedModule{}
	decoder := json.NewDecoder(bufio.NewReader(stdout))
	for {
		var pkg packageInfo
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			_ = cmd.Wait()
			return fmt.Errorf("decode go list output: %w", err)
		}
		if pkg.Module == nil || pkg.Module.Path == "" || pkg.Module.Dir == "" {
			continue
		}
		modules[pkg.Module.Path] = licensedModule{Path: pkg.Module.Path, Dir: pkg.Module.Dir}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("go list dependencies: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	paths := make([]string, 0, len(modules))
	for modulePath := range modules {
		paths = append(paths, modulePath)
	}
	sort.Strings(paths)
	var notice strings.Builder
	notice.WriteString("Third-party Go module licenses and notices\n")
	notice.WriteString("Generated from the packages shipped in this build.\n\n")
	for _, modulePath := range paths {
		module := modules[modulePath]
		if filepath.Clean(module.Dir) == filepath.Clean(repositoryRoot()) {
			// Packaging scripts ship the product LICENSE separately.
			// This report contains only third-party dependency notices.
			continue
		}
		files, err := moduleLicenseFiles(module.Dir)
		if err != nil {
			return fmt.Errorf("%s: %w", module.Path, err)
		}
		if len(files) == 0 {
			return fmt.Errorf("%s has no root LICENSE/COPYING/NOTICE/COPYRIGHT file", module.Path)
		}
		slug := safeName(module.Path)
		notice.WriteString(module.Path + "\n")
		for _, source := range files {
			name := slug + "__" + filepath.Base(source)
			destination := filepath.Join(out, name)
			if err := copyFile(source, destination); err != nil {
				return fmt.Errorf("copy %s: %w", source, err)
			}
			hash, err := fileSHA256(destination)
			if err != nil {
				return err
			}
			notice.WriteString("  " + filepath.ToSlash(name) + "  SHA-256 " + hash + "\n")
		}
		notice.WriteByte('\n')
	}
	return os.WriteFile(filepath.Join(out, "THIRD-PARTY-NOTICES.txt"), []byte(notice.String()), 0o644)
}

func repositoryRoot() string {
	root, err := os.Getwd()
	if err != nil {
		return ""
	}
	return root
}

func moduleLicenseFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, entry := range entries {
		if entry.IsDir() || !licenseName.MatchString(entry.Name()) {
			continue
		}
		result = append(result, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(result)
	return result, nil
}

func safeName(value string) string {
	var out strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			out.WriteRune(r)
		} else {
			out.WriteByte('_')
		}
	}
	return out.String()
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "license collector:", err)
	os.Exit(1)
}
