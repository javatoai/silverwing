#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "This script creates the macOS ARM64 app bundle and DMG and must run on macOS." >&2
  exit 1
fi
if [[ "$(uname -m)" != "arm64" ]]; then
  echo "The current product release target is Apple silicon (macOS arm64); Intel macOS is not packaged." >&2
  exit 1
fi
command -v go >/dev/null || { echo "Go 1.27.1 is required." >&2; exit 1; }
command -v curl >/dev/null || { echo "curl is required to fetch pinned build-time assets." >&2; exit 1; }
command -v hdiutil >/dev/null || { echo "hdiutil is required to create the DMG." >&2; exit 1; }

version="$(sed -n 's/.*CurrentProductVersion[[:space:]]*= *"\([0-9.]*\)".*/\1/p' "$repo_root/internal/domain/models.go" | head -n 1)"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "Could not read CurrentProductVersion." >&2; exit 1; }
build_time="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
stamp="$(date -u '+%Y%m%dT%H%M%SZ')"
out_dir="$repo_root/build/native-release/macos-arm64/$version-$stamp"
[[ ! -e "$out_dir" ]] || { echo "Refusing to overwrite existing output: $out_dir" >&2; exit 1; }
mkdir -p "$repo_root/build/native-packaging" "$repo_root/build/native-cache"
work="$(mktemp -d "$repo_root/build/native-packaging/macos.XXXXXX")"
cleanup() {
  case "$work" in
    "$repo_root"/build/native-packaging/*) rm -rf -- "$work" ;;
    *) echo "Refusing to remove unexpected temporary path: $work" >&2 ;;
  esac
}
trap cleanup EXIT

payload="$work/silverwing-qa.app"
macos_dir="$payload/Contents/MacOS"
resource_root="$payload/Contents/Resources"
notice_root="$work/notices"
mkdir -p "$macos_dir" "$resource_root" "$notice_root"
cp "$repo_root/internal/desktop/assets/app-icon.icns" "$resource_root/app-icon.icns"

fetch_pinned() {
  local filename="$1" url="$2" expected="$3" preferred="${4:-}"
  local cached="$repo_root/build/native-cache/$filename"
  local candidate="$cached"
  if [[ -n "$preferred" && -f "$preferred" ]]; then candidate="$preferred"; fi
  if [[ -f "$candidate" ]]; then
    local actual
    actual="$(shasum -a 256 "$candidate" | awk '{print $1}')"
    [[ "$actual" == "$expected" ]] || { echo "SHA-256 mismatch for $candidate (expected $expected, got $actual)." >&2; return 1; }
    if [[ "$candidate" != "$cached" && ! -e "$cached" ]]; then cp -p "$candidate" "$cached"; fi
    printf '%s\n' "$candidate"
    return 0
  fi
  [[ ! -e "$cached" ]] || { echo "Pinned cache path exists but is not a file: $cached" >&2; return 1; }
  local partial="$cached.$$.partial"
  curl --fail --location --retry 2 --output "$partial" "$url"
  local actual
  actual="$(shasum -a 256 "$partial" | awk '{print $1}')"
  if [[ "$actual" != "$expected" ]]; then
    echo "SHA-256 mismatch for $filename (expected $expected, got $actual)." >&2
    rm -f -- "$partial"
    return 1
  fi
  mv -- "$partial" "$cached"
  printf '%s\n' "$cached"
}

commit="unknown"
if git -C "$repo_root" rev-parse --short=12 HEAD >/dev/null 2>&1; then
  commit="$(git -C "$repo_root" rev-parse --short=12 HEAD)"
fi
ldflags="-s -w -X silverwing-go/internal/buildinfo.Version=$version -X silverwing-go/internal/buildinfo.Commit=$commit -X silverwing-go/internal/buildinfo.BuildTime=$build_time"
(
  cd "$repo_root"
  GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -tags mygo_noinspector -ldflags "$ldflags" -o "$macos_dir/silverwing-qa" ./cmd/silverwing
)
chmod 755 "$macos_dir/silverwing-qa"

mmdr_archive="$(fetch_pinned \
  'mmdr-aarch64-apple-darwin.tar.gz' \
  'https://github.com/1jehuang/mermaid-rs-renderer/releases/download/v0.3.1/mmdr-aarch64-apple-darwin.tar.gz' \
  '562d0250cb8588adefe398a23e4bbdf67f242849ea8d888c38268bcc3edf3223')"
mmdr_license="$(fetch_pinned \
  'mmdr-v0.3.1-LICENSE' \
  'https://raw.githubusercontent.com/1jehuang/mermaid-rs-renderer/v0.3.1/LICENSE' \
  '57ed7943c34463678a150769d4a4f6c95d2a190fe2c1977f74bc883492c94b86')"
mermaid_dir="$resource_root/native-mermaid/macos-arm64"
mkdir -p "$mermaid_dir" "$notice_root/mmdr"
mmdr_member="$(tar -tzf "$mmdr_archive" | awk '($0 == "mmdr") || ($0 ~ /\/mmdr$/) { print; exit }')"
[[ -n "$mmdr_member" ]] || { echo "Pinned mmdr archive does not contain an mmdr executable." >&2; exit 1; }
tar -xOzf "$mmdr_archive" "$mmdr_member" > "$mermaid_dir/mmdr"
[[ -s "$mermaid_dir/mmdr" && "$(wc -c < "$mermaid_dir/mmdr")" -le 25165824 ]] || { echo "mmdr binary is empty or exceeds the packaged limit." >&2; exit 1; }
chmod 755 "$mermaid_dir/mmdr"
printf '0.3.1\n' > "$mermaid_dir/version.txt"
cp "$mmdr_license" "$notice_root/mmdr/LICENSE"
cat > "$notice_root/mmdr/SOURCE.txt" <<'EOF'
mermaid-rs-renderer v0.3.1
https://github.com/1jehuang/mermaid-rs-renderer/releases/tag/v0.3.1
macOS arm64 archive: mmdr-aarch64-apple-darwin.tar.gz
Archive SHA-256: 562d0250cb8588adefe398a23e4bbdf67f242849ea8d888c38268bcc3edf3223
License SHA-256: 57ed7943c34463678a150769d4a4f6c95d2a190fe2c1977f74bc883492c94b86
EOF

pdfium_archive="$(fetch_pinned \
  'pdfium-mac-arm64.tgz' \
  'https://github.com/bblanchon/pdfium-binaries/releases/download/chromium/8086/pdfium-mac-arm64.tgz' \
  'e98679e052c07edbb5a627980902abb823d4b3f35744d877bd21668bd9fc13ab')"
pdfium_extract="$work/pdfium"
mkdir -p "$pdfium_extract" "$resource_root/native" "$notice_root/pdfium"
pdfium_member="$(tar -tzf "$pdfium_archive" | awk '($0 ~ /(^|\/)libpdfium\.dylib$/) { print; exit }')"
[[ -n "$pdfium_member" ]] || { echo "Pinned PDFium archive does not contain libpdfium.dylib." >&2; exit 1; }
tar -xzf "$pdfium_archive" -C "$pdfium_extract" "$pdfium_member" LICENSE licenses
cp "$pdfium_extract/$pdfium_member" "$resource_root/native/libpdfium.dylib"
cp "$pdfium_extract/LICENSE" "$notice_root/pdfium/LICENSE"
cp -R "$pdfium_extract/licenses" "$notice_root/pdfium/licenses"
pdfium_file_sha="$(shasum -a 256 "$resource_root/native/libpdfium.dylib" | awk '{print $1}')"
cat > "$notice_root/pdfium/SOURCE.txt" <<EOF
PDFium 157.0.8086.0 (chromium/8086)
https://github.com/bblanchon/pdfium-binaries/releases/tag/chromium%2F8086
macOS arm64 archive: pdfium-mac-arm64.tgz
Archive SHA-256: e98679e052c07edbb5a627980902abb823d4b3f35744d877bd21668bd9fc13ab
Extracted libpdfium.dylib SHA-256: $pdfium_file_sha
The distribution LICENSE and all dependency license files are reproduced below.
EOF

(
  cd "$repo_root"
  go run ./scripts/licenses -out "$notice_root/go" -tags mygo_noinspector ./cmd/silverwing
)
cat > "$notice_root/THIRD-PARTY-NOTICES.txt" <<'EOF'
Silverwing native package third-party notices

This file includes the complete original license and notice contents for the Go modules linked into the Silverwing GUI, mmdr v0.3.1, PDFium 157.0.8086.0 and its dependencies.
Each section identifies its original file.
The app bundle contains no WebView runtime or JVM. macOS system graphics/runtime components are supplied by macOS.
EOF

notice="$resource_root/THIRD-PARTY-NOTICES.txt"
printf 'Silverwing native package third-party notices\n' > "$notice"
find "$notice_root" -type f -print0 | while IFS= read -r -d '' file; do
  name="${file#"$notice_root"/}"
  printf '\n\n===== %s =====\n' "$name" >> "$notice"
  cat "$file" >> "$notice"
done

cp "$repo_root/LICENSE" "$resource_root/LICENSE"

cat > "$payload/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleExecutable</key><string>silverwing-qa</string>
  <key>CFBundleIdentifier</key><string>io.github.javatoai.silverwing.qa</string>
  <key>CFBundleName</key><string>silverwing-qa</string>
  <key>CFBundleDisplayName</key><string>silverwing-qa</string>
  <key>CFBundleIconFile</key><string>app-icon.icns</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$version</string>
  <key>CFBundleVersion</key><string>$version</string>
  <key>LSApplicationCategoryType</key><string>public.app-category.developer-tools</string>
  <key>NSHighResolutionCapable</key><true/>
</dict></plist>
EOF
printf 'APPL????' > "$payload/Contents/PkgInfo"
plutil -lint "$payload/Contents/Info.plist"
codesign --force --deep --sign - "$payload"
codesign --verify --deep --strict "$payload"

mkdir -p "$out_dir"
cp -R "$payload" "$out_dir/silverwing-qa.app"
dmg="$out_dir/silverwing-qa-$version-macos-arm64.dmg"
hdiutil create -format UDZO -volname "silverwing-qa $version" -srcfolder "$out_dir/silverwing-qa.app" "$dmg"
printf 'Native Go package version %s (%s) created at %s\n' "$version" "$commit" "$out_dir"
ls -lh "$out_dir"
