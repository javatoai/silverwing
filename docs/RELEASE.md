# Native Go build and packaging

## Versioning and packages

The Go product uses its own release sequence, starting at `0.0.1`.
`domain.CurrentProductVersion` is the source for the UI version, linker metadata
and package names. Configuration and task schema versions are independent.

The desktop uses Go and the locally patched MyGO native UI. Maintain those
patches as described in [MYGO-PATCHES.md](../third_party/MYGO-PATCHES.md).
Builds produce Windows x64 portable ZIP, Setup EXE and MSI packages, or a macOS
Apple silicon DMG. The macOS app is ad-hoc signed; it is not Developer ID signed
or notarized. Linux, Intel Mac and Windows ARM packages are not provided.

Packaged apps include the pinned Mermaid renderer and PDFium library with
their notices. Startup uses local resources; users do not need Go, Node,
Bun or a JVM to run the app.

## Build on Windows

Use Go 1.27.1 as declared by `go.mod`. The portable build compiles the GUI,
fetches or reuses checksum-pinned assets, checks the dependency licenses,
and creates a portable application directory and ZIP:

```powershell
.\scripts\build-windows.ps1 -PortableOnly
```

The full packaging script additionally creates a per-user Setup EXE and MSI;
it requires NSIS 3.13 and WiX Toolset 3.14.1. The script accepts portable tool
paths so a local build does not need to install either tool system-wide:

```powershell
.\scripts\build-windows.ps1 `
  -PayloadDirectory build/native-release/windows-amd64/<version-time>/silverwing `
  -NSISPath build/native-tools/nsis-3.13/nsis-3.13/Bin/makensis.exe `
  -WixBinPath build/native-tools/wix3.14.1 `
  -KeepBuildTemp
```

`-PayloadDirectory` packages an existing portable application directory without
recompiling the GUI. `-KeepBuildTemp` leaves compiler inputs and
intermediates in the ignored `build/native-packaging/` tree for inspection.
When building from source instead, omit `-PayloadDirectory`; the script
compiles the GUI and reconstructs the pinned runtime resources. Reusable payloads
must include the GUI, matching native resources and third-party notices.

Each run writes to a new directory under `build/native-release/windows-amd64/`
named with product version and UTC build time. Existing output directories are
not replaced. Build intermediates and verified download caches are confined to
the ignored `build/native-packaging/` and `build/native-cache/` directories.

## Build on macOS

The current DMG target is Apple silicon (macOS arm64).
Run the script on an Apple silicon Mac with Go 1.27.1, Xcode command line tools,
`codesign`, `plutil`, and `hdiutil`:

```bash
bash ./scripts/build-macos.sh
```

It creates `Silverwing.app` and a compressed DMG under a unique
`build/native-release/macos-arm64/<version>-<UTC-time>/` directory. The app is
ad-hoc signed for local integrity; it is not Developer ID signed or notarized,
so a downloaded DMG can still receive a Gatekeeper warning. Intel macOS and
Universal packages are not produced by this script.

## Resource layout and runtime lookup

The resource tree uses one root regardless of platform. The GUI sets MyGO's
`PathResources` to `buildinfo.ResourceRoot(os.Executable())` on Windows. On macOS
this helper resolves the executable in `Contents/MacOS` to
`Contents/Resources`, which matches the app bundle layout.

```text
Windows portable folder/
├── silverwing.exe
└── resources/
    ├── native/pdfium.dll
    ├── native-mermaid/windows-x64/mmdr.exe
    └── licenses/...

Silverwing.app/Contents/
├── MacOS/silverwing
└── Resources/
    ├── native/libpdfium.dylib
    ├── native-mermaid/macos-arm64/mmdr
    └── licenses/...
```

The desktop calls its Go application layer directly and retains integration with
external CLIs. The media loader checks `resources/native/`
beside a Windows executable and `Contents/Resources/native/` for a
macOS bundle executable. Mermaid lookup should read the binary at
`<resource-root>/native-mermaid/<platform-key>/<mmdr>` and pass those bytes to
`editor.InstallMermaidExecutable`; that method installs into the local app
cache without network access. Cache extraction is not the source of the
packaged binary.

The build injects `buildinfo.Version`, `Commit`, and `BuildTime` with Go linker
flags. `Version` comes from `domain.CurrentProductVersion`.

## Pinned native resources

The scripts verify each archive SHA-256 before extracting it. They also verify
the standalone mmdr license, retain the PDFium distribution license and all
PDFium dependency license files, and run the Go license collector against the
actual GUI dependency graph. A missing Go-module license makes
packaging fail. Notices are placed under `resources/licenses/`.

| Resource | Target | Source | SHA-256 |
|---|---|---|---|
| mmdr v0.3.1 | Windows x64 | [upstream release archive](https://github.com/1jehuang/mermaid-rs-renderer/releases/download/v0.3.1/mmdr-x86_64-pc-windows-msvc.zip) | `11df20c6a2b22c6dad95a8f0176c5530d4dc9de36ef289653ee9fea13027b14c` |
| mmdr v0.3.1 | macOS arm64 | [upstream release archive](https://github.com/1jehuang/mermaid-rs-renderer/releases/download/v0.3.1/mmdr-aarch64-apple-darwin.tar.gz) | `562d0250cb8588adefe398a23e4bbdf67f242849ea8d888c38268bcc3edf3223` |
| mmdr license | all | [v0.3.1 LICENSE](https://raw.githubusercontent.com/1jehuang/mermaid-rs-renderer/v0.3.1/LICENSE) | `57ed7943c34463678a150769d4a4f6c95d2a190fe2c1977f74bc883492c94b86` |
| PDFium 157.0.8086.0 | Windows x64 | [release asset](https://github.com/bblanchon/pdfium-binaries/releases/download/chromium/8086/pdfium-win-x64.tgz) | `1fd8af952832dbb0eb16d9249f68fe09e5f5ebf7c3dd9f6066ea2720cc28487d` |
| PDFium 157.0.8086.0 | macOS arm64 | [release asset](https://github.com/bblanchon/pdfium-binaries/releases/download/chromium/8086/pdfium-mac-arm64.tgz) | `e98679e052c07edbb5a627980902abb823d4b3f35744d877bd21668bd9fc13ab` |

The Windows installer toolchain is build-time only and is never copied into
the application package. Portable archives used for the local installer build
are version checked by `build-windows.ps1`:

| Tool | Fixed source | SHA-256 |
|---|---|---|
| NSIS 3.13 ZIP | [NSIS SourceForge release](https://sourceforge.net/projects/nsis/files/NSIS%203/3.13/nsis-3.13.zip); [MyGo's checksum-verified mirror](https://github.com/egoist/mygo/releases/download/nsis-3.13/nsis-3.13.zip) | `ba63dffc4410ee89193e1cb5a41989991bd77c61068da17e3156d136b7b0b3d8` |
| WiX Toolset 3.14.1 binaries ZIP | [official WiX v3.14.1 release](https://github.com/wixtoolset/wix3/releases/tag/wix3141rtm) | `6ac824e1642d6f7277d0ed7ea09411a508f6116ba6fae0aa5f2c7daa2ff43d31` |

The local script checks installer tool versions before building either installer.

Go dependency notices are copied as individual original files under
`resources/licenses/go/` and listed with hashes in
`resources/licenses/go/THIRD-PARTY-NOTICES.txt`, including MyGO's original license.
Silverwing's [MIT license](../LICENSE) is copied to the Windows package root
and to `Contents/Resources/LICENSE` on macOS. Third-party dependencies retain
their own licenses and copyright notices.

## GitHub Actions

[Native builds and releases](../.github/workflows/native-build.yml) runs on
branch pushes, pull requests, manual dispatch and `v*` tag pushes. Deleted-ref
events are skipped.

Ordinary runs build the Windows portable ZIP and macOS DMG. A release-tag run
also builds the Windows Setup EXE and MSI. Platform artifacts are retained
for 14 days.

A release tag must exactly match `v` followed by `domain.CurrentProductVersion`.
After both platform builds succeed, `scripts/publish-release.py` checks the
complete asset set, uploads a draft and publishes the GitHub Release. Only this
release job receives repository write permission, and publishing is limited
to `javatoai/silverwing`.

Local packaging writes to the ignored `build/` directory. It does not install
the app or publish a release. A successful build does not establish complete
feature, installation or platform runtime validation.
