// update-bundle adds official layout metadata and signed MyGO update artifacts
// to the existing native packaging pipeline; it never reads user configuration.
package main

import (
	binaryinfo "debug/buildinfo"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"silverwing-go/internal/appupdate"
	"silverwing-go/internal/appupdate/native"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/productprofile"
)

func main() {
	root := flag.String("layout", "", "official application directory")
	marker := flag.String("marker", "resources/update-installation.json", "layout path relative to application")
	executable := flag.String("exe", productprofile.ExecutableName+".exe", "relative executable")
	helper := flag.String("helper", "resources/"+productprofile.ExecutableName+"-updater.exe", "relative updater")
	platform := flag.String("platform", "windows/amd64", "runtime platform")
	version := flag.String("version", domain.CurrentProductVersion, "product version")
	kind := flag.String("bundle", "", "installation kind; empty writes layout")
	payload := flag.String("payload", "", "installer or complete application directory")
	output := flag.String("out", "", "artifact directory")
	publicKey := flag.String("public-key", os.Getenv("MYGO_UPDATER_PUBLIC_KEY"), "pinned public key")
	verify := flag.String("verify", "", "verify a complete set of signed release bundles")
	flag.Parse()
	if *verify != "" {
		must(verifyBundles(*verify, *version, *publicKey))
		return
	}
	identity := appupdate.Identity{Repository: productprofile.ReleaseRepository, TagPrefix: productprofile.ReleaseTagPrefix, Executable: productprofile.ExecutableName, Platform: *platform, Version: *version}
	if *root == "" {
		log.Fatal("missing application directory")
	}
	if *kind == "" {
		files, err := appupdate.Inventory(*root, *marker)
		must(err)
		layout := appupdate.Layout{Schema: 1, Identity: identity, Executable: *executable, Helper: *helper, Files: files}
		path, err := appupdate.Resolve(*root, *marker)
		must(err)
		must(os.MkdirAll(filepath.Dir(path), 0o755))
		must(appupdate.WriteJSON(path, layout))
		return
	}
	privateKey := os.Getenv("MYGO_UPDATER_PRIVATE_KEY")
	if privateKey == "" || *publicKey == "" {
		log.Fatal("signed update artifacts require CI signing key and pinned public key")
	}
	installationKind := appupdate.Kind(*kind)
	if installationKind != appupdate.Setup && installationKind != appupdate.MSI && installationKind != appupdate.Portable && installationKind != appupdate.MacApp {
		log.Fatal("invalid update kind")
	}
	var layout appupdate.Layout
	markerPath, err := appupdate.Resolve(*root, *marker)
	must(err)
	must(appupdate.ReadJSON(markerPath, &layout))
	if layout.Identity != identity {
		log.Fatal("application layout does not match release identity")
	}
	for _, relative := range []string{layout.Executable, layout.Helper} {
		path, e := appupdate.Resolve(*root, relative)
		must(e)
		must(checkPinnedBuild(path, *publicKey))
	}
	if installationKind != appupdate.MacApp {
		must(appupdate.VerifyLayout(*root, layout))
	}
	layout.Files, err = appupdate.Inventory(*root, "")
	must(err)
	must(os.MkdirAll(*output, 0o755))
	work, err := os.MkdirTemp(*output, ".update-build-")
	must(err)
	defer func() {
		if appupdate.Within(*output, work) {
			os.RemoveAll(work)
		}
	}()
	metadata := appupdate.Metadata{Schema: 1, Identity: identity, Kind: installationKind, Layout: &layout}
	entries := []string{appupdate.MetadataName}
	if installationKind == appupdate.Setup || installationKind == appupdate.MSI {
		name := "installer.exe"
		if installationKind == appupdate.MSI {
			name = "installer.msi"
		}
		metadata.Payload = name
		must(appupdate.CopyFile(*payload, filepath.Join(work, name), 0o600))
		metadata.InstallerSHA256, err = appupdate.Digest(filepath.Join(work, name))
		must(err)
		if installationKind == appupdate.MSI {
			metadata.ProductCode, metadata.UpgradeCode, err = native.MSIFileIdentity(*payload, *version)
			must(err)
		}
		entries = append(entries, name)
	} else {
		metadata.Payload = "payload"
		for _, file := range layout.Files {
			source, e := appupdate.Resolve(*root, file.Path)
			must(e)
			destination, e := appupdate.Resolve(filepath.Join(work, "payload"), file.Path)
			must(e)
			must(appupdate.CopyFile(source, destination, os.FileMode(file.Mode)))
		}
		entries = append(entries, "payload")
	}
	must(appupdate.WriteJSON(filepath.Join(work, appupdate.MetadataName), metadata))
	target := appupdate.Target(*platform, installationKind)
	name := identity.Executable + "-" + identity.Version + "-" + target + ".tar.gz"
	size, signature, actualKey, err := mygo.WriteUpdateArchive(filepath.Join(*output, name), work, entries, privateKey)
	must(err)
	if actualKey != *publicKey {
		log.Fatal("signing key does not match the program's pinned public key")
	}
	manifest := mygo.UpdateManifest{Version: *version, Date: time.Now().UTC().Format(time.RFC3339), URL: "https://github.com/" + identity.Repository + "/releases/download/" + url.PathEscape(identity.TagPrefix+identity.Version) + "/" + name, Size: size, Signature: signature}
	must(appupdate.WriteJSON(filepath.Join(*output, "update-"+target+".json"), manifest))
	data, _ := json.Marshal(map[string]any{"target": target, "size": size})
	fmt.Println(string(data))
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func checkPinnedBuild(path, publicKey string) error {
	info, err := binaryinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot verify updater key in %s: %w", path, err)
	}
	assignment := "silverwing-go/internal/buildinfo.UpdatePublicKey=" + publicKey
	for _, setting := range info.Settings {
		if setting.Key != "-ldflags" {
			continue
		}
		for _, argument := range strings.Fields(setting.Value) {
			if argument == assignment {
				return nil
			}
		}
	}
	return fmt.Errorf("%s does not pin the release signing key; rebuild the payload", path)
}

func verifyBundles(directory, version, publicKey string) error {
	for _, kind := range []appupdate.Kind{appupdate.Setup, appupdate.MSI, appupdate.Portable, appupdate.MacApp} {
		platform := "windows/amd64"
		if kind == appupdate.MacApp {
			platform = "darwin/arm64"
		}
		target := appupdate.Target(platform, kind)
		name := productprofile.ExecutableName + "-" + version + "-" + target + ".tar.gz"
		var manifest mygo.UpdateManifest
		if err := appupdate.ReadJSON(filepath.Join(directory, "update-"+target+".json"), &manifest); err != nil {
			return err
		}
		expected := "https://github.com/" + productprofile.ReleaseRepository + "/releases/download/" + productprofile.ReleaseTagPrefix + version + "/" + name
		if manifest.Version != version || manifest.URL != expected {
			return fmt.Errorf("update manifest does not match release %s", target)
		}
		if err := mygo.VerifyUpdateArchive(filepath.Join(directory, name), publicKey, manifest); err != nil {
			return err
		}
	}
	return nil
}
