//go:build windows

package native

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
	"silverwing-go/internal/appupdate"
	"silverwing-go/internal/appupdate/install"
)

func fixtureGUID() string {
	var data [16]byte
	rand.Read(data[:])
	return fmt.Sprintf("{%x-%x-%x-%x-%x}", data[:4], data[4:6], data[6:8], data[8:10], data[10:])
}

func nativeFixtureBundle(t *testing.T, directory, version string, kind appupdate.Kind, installer, product, upgrade string) appupdate.Bundle {
	t.Helper()
	source := filepath.Join(directory, version)
	os.MkdirAll(source, 0o700)
	app := filepath.Join(source, "fixture.txt")
	os.WriteFile(app, []byte(version), 0o600)
	hash, _ := appupdate.Digest(app)
	layout := appupdate.Layout{Schema: 1, Executable: "fixture.txt", Helper: "fixture.txt", Files: []appupdate.File{{Path: "fixture.txt", SHA256: hash, Size: int64(len(version)), Mode: 0o600}}}
	installerHash, err := appupdate.Digest(installer)
	if err != nil {
		t.Fatal(err)
	}
	return appupdate.Bundle{Directory: filepath.Dir(installer), Metadata: appupdate.Metadata{Kind: kind, Payload: filepath.Base(installer), InstallerSHA256: installerHash, ProductCode: product, UpgradeCode: upgrade, Layout: &layout}}
}

func compileTool(t *testing.T, tool string, args ...string) {
	t.Helper()
	cmd := exec.Command(tool, args...)
	hide(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture compiler: %v\n%s", err, output)
	}
}

func TestNativeRegisteredInstallerUpgradeRollbackAndRegistration(t *testing.T) {
	if os.Getenv("SILVERWING_NATIVE_INSTALLER_TESTS") != "1" {
		t.Skip("isolated silent installer tests are explicit opt-in")
	}
	tools := filepath.Join("..", "..", "..", "build", "native-tools")
	wix, _ := filepath.Abs(filepath.Join(tools, "wix3.14.1"))
	nsis, _ := filepath.Abs(filepath.Join(tools, "nsis-3.13", "nsis-3.13", "makensis.exe"))
	for _, kind := range []appupdate.Kind{appupdate.Setup, appupdate.MSI} {
		t.Run(string(kind), func(t *testing.T) {
			directory := t.TempDir()
			root := filepath.Join(directory, "installed")
			os.Mkdir(root, 0o700)
			keyName := `Software\SilverwingUpdaterFixtures\` + strings.Trim(fixtureGUID(), "{}")
			defer registry.DeleteKey(registry.CURRENT_USER, keyName)
			upgrade, oldCode, newCode := fixtureGUID(), fixtureGUID(), fixtureGUID()
			build := func(version, product string) appupdate.Bundle {
				work := filepath.Join(directory, "build-"+version)
				os.Mkdir(work, 0o700)
				data := filepath.Join(work, "fixture.txt")
				os.WriteFile(data, []byte(version), 0o600)
				if kind == appupdate.Setup {
					installer := filepath.Join(work, "fixture-setup.exe")
					script := filepath.Join(work, "fixture.nsi")
					body := fmt.Sprintf("Unicode true\nName \"SilverwingUpdaterFixture\"\nOutFile \"%s\"\nRequestExecutionLevel user\nSilentInstall silent\nInstallDir \"%s\"\nSection\nSetOutPath $INSTDIR\nFile /oname=fixture.txt \"%s\"\nWriteRegStr HKCU \"%s\" \"Version\" \"%s\"\nWriteUninstaller $INSTDIR\\Uninstall.exe\nSectionEnd\nSection \"Uninstall\"\nDelete $INSTDIR\\fixture.txt\nSectionEnd\n", installer, root, data, keyName, version)
					if version == "1.0.1" {
						body = strings.Replace(body, "WriteRegStr", fmt.Sprintf("File /oname=new-only.bin \"%s\"\nWriteRegStr", data), 1)
					}
					os.WriteFile(script, []byte(body), 0o600)
					compileTool(t, nsis, "/V1", script)
					bundle := nativeFixtureBundle(t, directory, version, kind, installer, product, upgrade)
					if version == "1.0.1" {
						hash, _ := appupdate.Digest(data)
						bundle.Metadata.Layout.Files = append(bundle.Metadata.Layout.Files, appupdate.File{Path: "new-only.bin", SHA256: hash, Size: int64(len(version)), Mode: 0o600})
					}
					return bundle
				}
				installer := filepath.Join(work, "fixture.msi")
				script := filepath.Join(work, "fixture.wxs")
				body := fmt.Sprintf(`<?xml version="1.0"?><Wix xmlns="http://schemas.microsoft.com/wix/2006/wi"><Product Id="%s" Name="SilverwingUpdaterFixture" Language="1033" Version="%s" Manufacturer="Silverwing Test" UpgradeCode="%s"><Package InstallerVersion="500" Compressed="yes" InstallScope="perUser"/><MajorUpgrade AllowSameVersionUpgrades="yes" DowngradeErrorMessage="Newer version exists"/><MediaTemplate EmbedCab="yes"/><SetProperty Id="ARPINSTALLLOCATION" Value="[INSTALLFOLDER]" After="CostFinalize" Sequence="execute"/><Directory Id="TARGETDIR" Name="SourceDir"><Directory Id="LocalAppDataFolder"><Directory Id="INSTALLFOLDER" Name="SilverwingUpdaterFixture"><Component Id="Payload" Guid="%s"><File Id="PayloadFile" Source="%s" Name="fixture.txt"/><RegistryValue Root="HKCU" Key="%s" Name="installed" Type="integer" Value="1" KeyPath="yes"/></Component></Directory></Directory></Directory><Feature Id="Main" Level="1"><ComponentRef Id="Payload"/></Feature></Product></Wix>`, product, version, upgrade, fixtureGUID(), data, keyName)
				os.WriteFile(script, []byte(body), 0o600)
				object := filepath.Join(work, "fixture.wixobj")
				compileTool(t, filepath.Join(wix, "candle.exe"), "-nologo", "-out", object, script)
				compileTool(t, filepath.Join(wix, "light.exe"), "-nologo", "-sval", "-out", installer, object)
				return nativeFixtureBundle(t, directory, version, kind, installer, product, upgrade)
			}
			old, next := build("1.0.0", oldCode), build("1.0.1", newCode)
			runner := Runner{}
			if kind == appupdate.MSI {
				defer runner.Run(context.Background(), "msiexec.exe", []string{"/x", newCode, "/qn", "/norestart"})
				defer runner.Run(context.Background(), "msiexec.exe", []string{"/x", oldCode, "/qn", "/norestart"})
			}
			adapter := install.Registered{Kind: kind, Runner: runner}
			initial := appupdate.Plan{Installation: appupdate.Installation{Root: root, Kind: kind, UpgradeCode: upgrade}, New: old}
			if err := adapter.Apply(context.Background(), initial); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(root, "user.md"), []byte("user data"), 0o600)
			plan := appupdate.Plan{Installation: initial.Installation, New: next, Recovery: &old}
			if err := adapter.Apply(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
			if kind == appupdate.MSI {
				version, err := productInfo(newCode, "VersionString")
				if err != nil || version != "1.0.1" {
					t.Fatalf("MSI registration: %q %v", version, err)
				}
				if _, err := productInfo(oldCode, "VersionString"); err == nil {
					t.Fatal("old MSI registration survived major upgrade")
				}
			}
			if err := adapter.Rollback(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
			if kind == appupdate.MSI {
				version, err := productInfo(oldCode, "VersionString")
				if err != nil || version != "1.0.0" {
					t.Fatalf("MSI rollback registration: %q %v", version, err)
				}
			}
			if content, _ := os.ReadFile(filepath.Join(root, "user.md")); string(content) != "user data" {
				t.Fatal("installer changed user data")
			}
			if content, _ := os.ReadFile(filepath.Join(root, "fixture.txt")); string(content) != "1.0.0" {
				t.Fatal("old installer did not restore the program")
			}
			if kind == appupdate.Setup {
				if _, err := os.Stat(filepath.Join(root, "new-only.bin")); !os.IsNotExist(err) {
					t.Fatal("new Setup file survived rollback")
				}
			}
			if kind == appupdate.MSI {
				code, err := runner.Run(context.Background(), "msiexec.exe", []string{"/x", oldCode, "/qn", "/norestart"})
				if err != nil || code != 0 {
					t.Fatalf("restored MSI uninstall: %d %v", code, err)
				}
				if _, err := productInfo(oldCode, "VersionString"); err == nil {
					t.Fatal("uninstalled MSI still registered")
				}
				if content, _ := os.ReadFile(filepath.Join(root, "user.md")); string(content) != "user data" {
					t.Fatal("MSI uninstall changed user data")
				}
			}
		})
	}
}
