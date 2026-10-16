param(
    [switch]$PortableOnly,
    [string]$PayloadDirectory = '',
    [string]$NSISPath = '',
    [string]$WixBinPath = '',
    [switch]$KeepBuildTemp
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'nsis-payload-manifest.ps1')
. (Join-Path $PSScriptRoot 'windows-runtime.ps1')
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
if ($env:OS -ne 'Windows_NT') {
    throw 'This script packages the Windows x64 native application and must run on Windows.'
}
if (-not $PayloadDirectory -and -not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Go is required. Install the version declared in go.mod (Go 1.27.1).'
}

$domainModels = Get-Content (Join-Path $repoRoot 'internal/domain/models.go') -Raw
$match = [regex]::Match($domainModels, 'CurrentProductVersion\s*=\s*"([0-9]+\.[0-9]+\.[0-9]+)"')
if (-not $match.Success) { throw 'Could not read CurrentProductVersion from internal/domain/models.go.' }
$version = $match.Groups[1].Value
$utc = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
$stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ')
$buildRoot = Join-Path $repoRoot 'build/native-release'
$outputDirectory = Join-Path $buildRoot "windows-amd64/$version-$stamp"
if (Test-Path -LiteralPath $outputDirectory) { throw "Refusing to overwrite existing output: $outputDirectory" }
$workRoot = Join-Path $repoRoot ("build/native-packaging/windows-" + [guid]::NewGuid().ToString('N'))
$cacheRoot = Join-Path $repoRoot 'build/native-cache'
$stage = Join-Path $workRoot 'Silverwing'
$resourceRoot = Join-Path $stage 'resources'
$noticeRoot = Join-Path $workRoot 'notices'
$null = New-Item -ItemType Directory -Force -Path $stage, $resourceRoot, $cacheRoot, $noticeRoot

function Assert-WithinRepo([string]$Path) {
    $full = [IO.Path]::GetFullPath($Path)
    $prefix = [IO.Path]::GetFullPath((Join-Path $repoRoot 'build')) + [IO.Path]::DirectorySeparatorChar
    if (-not $full.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Generated path escaped the isolated build directory: $full"
    }
    return $full
}

function Get-PinnedAsset([string]$FileName, [string]$Url, [string]$Sha256, [string]$PreferredPath = '') {
    $cachePath = Join-Path $cacheRoot $FileName
    $candidate = if ($PreferredPath -and (Test-Path -LiteralPath $PreferredPath -PathType Leaf)) { $PreferredPath } else { $cachePath }
    if (Test-Path -LiteralPath $candidate -PathType Leaf) {
        $actual = (Get-FileHash -LiteralPath $candidate -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $Sha256.ToLowerInvariant()) { throw "Pinned asset checksum mismatch for $candidate; expected $Sha256, got $actual." }
        if ($candidate -ne $cachePath -and -not (Test-Path -LiteralPath $cachePath)) {
            Copy-Item -LiteralPath $candidate -Destination $cachePath
        }
        return $candidate
    }

    if (Test-Path -LiteralPath $cachePath) { throw "Pinned asset cache path exists but is not a file: $cachePath" }
    $partial = Join-Path $cacheRoot ($FileName + '.' + [guid]::NewGuid().ToString('N') + '.partial')
    try {
        Write-Host "Fetching pinned build asset: $FileName"
        Invoke-WebRequest -Uri $Url -OutFile $partial -UseBasicParsing
        $actual = (Get-FileHash -LiteralPath $partial -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $Sha256.ToLowerInvariant()) { throw "Pinned asset checksum mismatch for $FileName; expected $Sha256, got $actual." }
        Move-Item -LiteralPath $partial -Destination $cachePath
        return $cachePath
    }
    finally {
        if (Test-Path -LiteralPath $partial) {
            $safePartial = Assert-WithinRepo $partial
            Remove-Item -LiteralPath $safePartial -Force
        }
    }
}

function Invoke-Go([string[]]$Arguments) {
    & go @Arguments
    if ($LASTEXITCODE -ne 0) { throw "go $($Arguments -join ' ') failed with exit code $LASTEXITCODE." }
}

function Add-PerUserRegistryKeyPaths([string]$HarvestFile) {
    $document = [System.Xml.XmlDocument]::new()
    $document.PreserveWhitespace = $true
    $document.Load($HarvestFile)
    $namespace = 'http://schemas.microsoft.com/wix/2006/wi'
    $namespaces = [System.Xml.XmlNamespaceManager]::new($document.NameTable)
    $namespaces.AddNamespace('wix', $namespace)
    $components = $document.SelectNodes('/wix:Wix/wix:Fragment//wix:Component', $namespaces)
    if ($components.Count -lt 1) { throw 'WiX heat produced no harvested components.' }
    foreach ($component in $components) {
        $componentId = $component.GetAttribute('Id')
        $files = $component.SelectNodes('wix:File[@KeyPath="yes"]', $namespaces)
        if ($files.Count -lt 1) { throw "Harvested per-user component has no file key path: $componentId" }
        foreach ($file in $files) { $file.RemoveAttribute('KeyPath') }
        $registry = $document.CreateElement('RegistryValue', $namespace)
        $registry.SetAttribute('Id', "reg$componentId")
        $registry.SetAttribute('Root', 'HKCU')
        $registry.SetAttribute('Key', 'Software\SilverwingGo\Installer\Components')
        $registry.SetAttribute('Name', $componentId)
        $registry.SetAttribute('Type', 'integer')
        $registry.SetAttribute('Value', '1')
        $registry.SetAttribute('KeyPath', 'yes')
        $null = $component.AppendChild($registry)
    }
    $directoryReference = $document.SelectSingleNode('/wix:Wix/wix:Fragment/wix:DirectoryRef[@Id="INSTALLFOLDER"]', $namespaces)
    if ($null -eq $directoryReference) { throw 'WiX heat output does not reference INSTALLFOLDER.' }
    $directories = $directoryReference.SelectNodes('.//wix:Directory', $namespaces)
    $cleanup = $document.CreateElement('Component', $namespace)
    $cleanup.SetAttribute('Id', 'HarvestFolderCleanup')
    $cleanup.SetAttribute('Guid', '98DE3F65-A4BD-4DB1-B50C-FB3B7E759870')
    foreach ($directory in $directories) {
        $folder = $document.CreateElement('RemoveFolder', $namespace)
        $folder.SetAttribute('Id', "RemoveFolder_$($directory.GetAttribute('Id'))")
        $folder.SetAttribute('Directory', $directory.GetAttribute('Id'))
        $folder.SetAttribute('On', 'uninstall')
        $null = $cleanup.AppendChild($folder)
    }
    $folderRegistry = $document.CreateElement('RegistryValue', $namespace)
    $folderRegistry.SetAttribute('Id', 'regHarvestFolderCleanup')
    $folderRegistry.SetAttribute('Root', 'HKCU')
    $folderRegistry.SetAttribute('Key', 'Software\SilverwingGo\Installer')
    $folderRegistry.SetAttribute('Name', 'harvestedFolders')
    $folderRegistry.SetAttribute('Type', 'integer')
    $folderRegistry.SetAttribute('Value', '1')
    $folderRegistry.SetAttribute('KeyPath', 'yes')
    $null = $cleanup.AppendChild($folderRegistry)
    $null = $directoryReference.PrependChild($cleanup)
    $componentGroup = $document.SelectSingleNode('/wix:Wix/wix:Fragment/wix:ComponentGroup[@Id="SilverwingFiles"]', $namespaces)
    if ($null -eq $componentGroup) { throw 'WiX heat output does not contain ComponentGroup SilverwingFiles.' }
    $cleanupReference = $document.CreateElement('ComponentRef', $namespace)
    $cleanupReference.SetAttribute('Id', 'HarvestFolderCleanup')
    $null = $componentGroup.AppendChild($cleanupReference)
    $document.Save($HarvestFile)
}

Push-Location -LiteralPath $repoRoot
try {
    $script:commit = 'unknown'
    & git -C $repoRoot rev-parse --short=12 HEAD 2>$null | ForEach-Object { if ($_ -match '^[0-9a-f]{7,40}$') { $script:commit = $_ } }
    $commit = $script:commit

    if ($PayloadDirectory) {
        $payloadSource = (Resolve-Path -LiteralPath $PayloadDirectory).Path
        if (-not (Test-Path -LiteralPath $payloadSource -PathType Container)) { throw "Payload path is not a directory: $payloadSource" }
        if (Test-Path -LiteralPath (Join-Path $payloadSource 'silverwing-cli.exe')) { throw 'Reusable payload contains the removed Silverwing CLI; provide a GUI-only payload' }
        foreach ($required in @(
            'silverwing.exe',
            'resources/native/pdfium.dll',
            'resources/native-mermaid/windows-x64/mmdr.exe'
        )) {
            if (-not (Test-Path -LiteralPath (Join-Path $payloadSource $required) -PathType Leaf)) { throw "Reusable payload is incomplete: missing $required" }
        }
        $flatNotices = Join-Path $payloadSource 'resources/THIRD-PARTY-NOTICES.txt'
        if (Test-Path -LiteralPath $flatNotices -PathType Leaf) {
            if ((Get-Item -LiteralPath $flatNotices).Length -eq 0) { throw 'Reusable payload third-party notices are empty.' }
        }
        else {
            foreach ($required in @('THIRD-PARTY-NOTICES.txt', 'pdfium/LICENSE', 'pdfium/licenses/pdfium.txt', 'mmdr/LICENSE', 'go/THIRD-PARTY-NOTICES.txt')) {
                if (-not (Test-Path -LiteralPath (Join-Path $payloadSource "resources/licenses/$required") -PathType Leaf)) { throw "Reusable payload is missing third-party notices: $required" }
            }
        }
        $pdfiumHash = (Get-FileHash -LiteralPath (Join-Path $payloadSource 'resources/native/pdfium.dll') -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($pdfiumHash -ne '1f2aee4286b8cf0413ab4562b7121aa454f9cfd9afd6ca8609bcef80927b3f80') { throw "Reusable payload PDFium checksum mismatch: $pdfiumHash" }
        foreach ($item in Get-ChildItem -LiteralPath $payloadSource -Force) {
            $destination = Join-Path $stage $item.Name
            if ($item.PSIsContainer -and (Test-Path -LiteralPath $destination -PathType Container)) {
                Get-ChildItem -LiteralPath $item.FullName -Force | ForEach-Object {
                    if ($item.Name -eq 'resources' -and $_.PSIsContainer -and $_.Name -eq 'licenses') {
                        # Read old payload notices into temporary collection only.
                        Get-ChildItem -LiteralPath $_.FullName -Force | ForEach-Object { Copy-Item -LiteralPath $_.FullName -Destination $noticeRoot -Recurse }
                    }
                    else {
                        Copy-Item -LiteralPath $_.FullName -Destination $destination -Recurse
                    }
                }
            }
            else {
                Copy-Item -LiteralPath $item.FullName -Destination $stage -Recurse
            }
        }
    }
    else {
        $commonLdflags = "-s -w -X silverwing-go/internal/buildinfo.Version=$version -X silverwing-go/internal/buildinfo.Commit=$commit -X silverwing-go/internal/buildinfo.BuildTime=$utc"

    $oldGOOS, $oldGOARCH, $oldCGO = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    try {
        Invoke-Go @('build', '-trimpath', '-tags', 'mygo_noinspector', '-ldflags', "$commonLdflags -H=windowsgui", '-o', (Join-Path $stage 'silverwing.exe'), './cmd/silverwing')
    }
    finally {
        $env:GOOS, $env:GOARCH, $env:CGO_ENABLED = $oldGOOS, $oldGOARCH, $oldCGO
    }

    $mmdrZip = Get-PinnedAsset 'mmdr-x86_64-pc-windows-msvc.zip' `
        'https://github.com/1jehuang/mermaid-rs-renderer/releases/download/v0.3.1/mmdr-x86_64-pc-windows-msvc.zip' `
        '11df20c6a2b22c6dad95a8f0176c5530d4dc9de36ef289653ee9fea13027b14c'
    $mmdrLicense = Get-PinnedAsset 'mmdr-v0.3.1-LICENSE' `
        'https://raw.githubusercontent.com/1jehuang/mermaid-rs-renderer/v0.3.1/LICENSE' `
        '57ed7943c34463678a150769d4a4f6c95d2a190fe2c1977f74bc883492c94b86'
    $mermaidDir = Join-Path $resourceRoot 'native-mermaid/windows-x64'
    $null = New-Item -ItemType Directory -Force -Path $mermaidDir
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [IO.Compression.ZipFile]::OpenRead($mmdrZip)
    try {
        $entry = $archive.Entries | Where-Object { $_.FullName -eq 'mmdr.exe' } | Select-Object -First 1
        if ($null -eq $entry -or $entry.Length -lt 1 -or $entry.Length -gt 24MB) { throw 'Pinned mmdr archive does not contain a bounded mmdr.exe.' }
        $input = $entry.Open()
        try {
            $target = Join-Path $mermaidDir 'mmdr.exe'
            $output = [IO.File]::Open($target, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
            try { $input.CopyTo($output) } finally { $output.Dispose() }
        }
        finally { $input.Dispose() }
    }
    finally { $archive.Dispose() }
    Set-Content -LiteralPath (Join-Path $mermaidDir 'version.txt') -Value "0.3.1" -Encoding ascii
    $mmdrNotices = Join-Path $noticeRoot 'mmdr'
    $null = New-Item -ItemType Directory -Force -Path $mmdrNotices
    Copy-Item -LiteralPath $mmdrLicense -Destination (Join-Path $mmdrNotices 'LICENSE')
    Set-Content -LiteralPath (Join-Path $mmdrNotices 'SOURCE.txt') -Value "mermaid-rs-renderer v0.3.1`nhttps://github.com/1jehuang/mermaid-rs-renderer/releases/tag/v0.3.1`nArchive SHA-256: 11df20c6a2b22c6dad95a8f0176c5530d4dc9de36ef289653ee9fea13027b14c`nLicense SHA-256: 57ed7943c34463678a150769d4a4f6c95d2a190fe2c1977f74bc883492c94b86" -Encoding utf8

    $pdfiumArchiveInDevHome = Join-Path $repoRoot '.dev-home/native-media/pdfium-157.0.8086.0/pdfium-win-x64.tgz'
    $pdfiumArchive = Get-PinnedAsset 'pdfium-win-x64.tgz' `
        'https://github.com/bblanchon/pdfium-binaries/releases/download/chromium/8086/pdfium-win-x64.tgz' `
        '1fd8af952832dbb0eb16d9249f68fe09e5f5ebf7c3dd9f6066ea2720cc28487d' $pdfiumArchiveInDevHome
    $pdfiumExtract = Join-Path $workRoot 'pdfium'
    $null = New-Item -ItemType Directory -Path $pdfiumExtract
    & tar.exe -xzf $pdfiumArchive -C $pdfiumExtract -- 'bin/pdfium.dll' 'LICENSE' 'licenses'
    if ($LASTEXITCODE -ne 0) { throw 'Could not extract the pinned PDFium archive.' }
    $pdfiumDLL = Join-Path $pdfiumExtract 'bin/pdfium.dll'
    $pdfiumHash = (Get-FileHash -LiteralPath $pdfiumDLL -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($pdfiumHash -ne '1f2aee4286b8cf0413ab4562b7121aa454f9cfd9afd6ca8609bcef80927b3f80') { throw "PDFium DLL checksum mismatch: $pdfiumHash" }
    $nativeDir = Join-Path $resourceRoot 'native'
    $pdfiumNotices = Join-Path $noticeRoot 'pdfium'
    $null = New-Item -ItemType Directory -Force -Path $nativeDir, $pdfiumNotices
    Copy-Item -LiteralPath $pdfiumDLL -Destination (Join-Path $nativeDir 'pdfium.dll')
    Copy-Item -LiteralPath (Join-Path $pdfiumExtract 'LICENSE') -Destination (Join-Path $pdfiumNotices 'LICENSE')
    Copy-Item -LiteralPath (Join-Path $pdfiumExtract 'licenses') -Destination (Join-Path $pdfiumNotices 'licenses') -Recurse
    Set-Content -LiteralPath (Join-Path $pdfiumNotices 'SOURCE.txt') -Value "PDFium 157.0.8086.0 (chromium/8086)`nhttps://github.com/bblanchon/pdfium-binaries/releases/tag/chromium%2F8086`nArchive: pdfium-win-x64.tgz`nArchive SHA-256: 1fd8af952832dbb0eb16d9249f68fe09e5f5ebf7c3dd9f6066ea2720cc28487d`nDLL SHA-256: 1f2aee4286b8cf0413ab4562b7121aa454f9cfd9afd6ca8609bcef80927b3f80`nThe distribution LICENSE and all dependency license files are reproduced below." -Encoding utf8

    Invoke-Go @('run', './scripts/licenses', '-out', (Join-Path $noticeRoot 'go'), '-tags', 'mygo_noinspector', './cmd/silverwing')
    $notice = @'
Silverwing native package third-party notices

This file includes the complete original license and notice contents for the Go modules linked into the Silverwing GUI, mmdr v0.3.1, PDFium 157.0.8086.0 and its dependencies, and the app-local Microsoft runtime.
Each section identifies its original file; original formats, including the Microsoft RTF license source, are retained.
The app bundle contains no WebView runtime or JVM. Windows system graphics/runtime components are supplied by Windows.
'@
    Set-Content -LiteralPath (Join-Path $noticeRoot 'THIRD-PARTY-NOTICES.txt') -Value $notice -Encoding utf8
    }

    # Ship the product license alongside the GUI in every package.
    Copy-Item -LiteralPath (Join-Path $repoRoot 'LICENSE') -Destination (Join-Path $stage 'LICENSE') -Force

    # Reused GUI payloads may predate the renderer's app-local runtime. Enrich
    # both packaging paths before ZIP, NSIS manifest and MSI harvesting.
    $null = Prepare-SilverwingWindowsRuntime -PayloadDirectory $stage -CacheDirectory $cacheRoot -WorkDirectory $workRoot

    # Only the combined notice enters ZIP, NSIS and MSI payloads. Append raw
    # bytes so original license text/RTF is not changed by text decoding.
    $noticePath = Join-Path $resourceRoot 'THIRD-PARTY-NOTICES.txt'
    $noticeStream = [IO.File]::Open($noticePath, [IO.FileMode]::Append, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        foreach ($file in Get-ChildItem -LiteralPath $noticeRoot -Recurse -File | Sort-Object FullName) {
            $name = [IO.Path]::GetRelativePath($noticeRoot, $file.FullName).Replace('\', '/')
            $header = [Text.Encoding]::UTF8.GetBytes("`n`n===== $name =====`n")
            $noticeStream.Write($header, 0, $header.Length)
            $noticeInput = [IO.File]::OpenRead($file.FullName)
            try { $noticeInput.CopyTo($noticeStream) } finally { $noticeInput.Dispose() }
        }
    }
    finally { $noticeStream.Dispose() }

    if (-not $PortableOnly) {
        $nsisExecutable = ''
        if ($NSISPath) {
            if (-not (Test-Path -LiteralPath $NSISPath -PathType Leaf)) { throw "NSIS compiler not found: $NSISPath" }
            $nsisExecutable = (Resolve-Path -LiteralPath $NSISPath).Path
        }
        else {
            $nsis = Get-Command makensis.exe -ErrorAction SilentlyContinue
            if ($null -eq $nsis) { $nsis = Get-Command makensis -ErrorAction SilentlyContinue }
            if ($null -ne $nsis) { $nsisExecutable = $nsis.Source }
        }
        if (-not $nsisExecutable) { throw 'NSIS 3.13 is required for the Setup EXE. Install it or pass -NSISPath to a portable copy.' }
        $nsisVersion = (& $nsisExecutable /VERSION 2>&1 | Out-String).Trim()
        if ($LASTEXITCODE -ne 0 -or $nsisVersion -notmatch '(?i)\bv?3\.13\b') { throw "Expected NSIS 3.13, got: $nsisVersion" }

        $wixBin = $WixBinPath
        if (-not $wixBin) { $wixBin = Join-Path ${env:ProgramFiles(x86)} 'WiX Toolset v3.14\bin' }
        if (-not (Test-Path -LiteralPath $wixBin -PathType Container)) { throw "WiX Toolset 3.14.1 bin directory not found: $wixBin" }
        $wixBin = (Resolve-Path -LiteralPath $wixBin).Path
        foreach ($tool in @('heat.exe', 'candle.exe', 'light.exe')) {
            if (-not (Test-Path -LiteralPath (Join-Path $wixBin $tool))) { throw "WiX Toolset 3.14.1 is required for MSI creation: $tool not found. Use -PortableOnly to build the portable package." }
            $toolVersion = [Diagnostics.FileVersionInfo]::GetVersionInfo((Join-Path $wixBin $tool)).FileVersion
            if ($toolVersion -notmatch '^3\.14\.1(?:\.|$)') { throw "Expected WiX Toolset 3.14.1 for $tool, got: $toolVersion" }
        }

        $installerExe = Join-Path $workRoot "silverwing-$version-windows-x64-setup.exe"
        $uninstallManifest = Join-Path $workRoot 'uninstall-payload.nsh'
        New-NSISUninstallManifest -Stage $stage -Destination $uninstallManifest
        & $nsisExecutable /INPUTCHARSET UTF8 "/DPRODUCT_VERSION=$version" "/DSTAGING_DIR=$stage" "/DOUTPUT_FILE=$installerExe" "/DUNINSTALL_MANIFEST=$uninstallManifest" (Join-Path $PSScriptRoot 'windows-installer.nsi')
        if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $installerExe)) { throw 'NSIS failed to build the Setup EXE.' }

        $wixWork = Join-Path $workRoot 'wix'
        $null = New-Item -ItemType Directory -Path $wixWork
        $harvest = Join-Path $wixWork 'SilverwingFiles.wxs'
        & (Join-Path $wixBin 'heat.exe') dir $stage -nologo -dr INSTALLFOLDER -cg SilverwingFiles -gg -scom -sreg -sfrag -srd -var var.SourceDir -out $harvest
        if ($LASTEXITCODE -ne 0) { throw 'WiX heat failed to harvest the staged payload.' }
        Add-PerUserRegistryKeyPaths $harvest
        & (Join-Path $wixBin 'candle.exe') -nologo -arch x64 "-dProductVersion=$version" "-dSourceDir=$stage" -out (Join-Path $wixWork 'windows-installer.wixobj') (Join-Path $PSScriptRoot 'windows-installer.wxs')
        if ($LASTEXITCODE -ne 0) { throw 'WiX candle failed to compile windows-installer.wxs.' }
        & (Join-Path $wixBin 'candle.exe') -nologo -arch x64 "-dProductVersion=$version" "-dSourceDir=$stage" -out (Join-Path $wixWork 'SilverwingFiles.wixobj') $harvest
        if ($LASTEXITCODE -ne 0) { throw 'WiX candle failed to compile the harvested files.' }
        $installerMsi = Join-Path $workRoot "silverwing-$version-windows-x64.msi"
        & (Join-Path $wixBin 'light.exe') -nologo -out $installerMsi (Join-Path $wixWork 'windows-installer.wixobj') (Join-Path $wixWork 'SilverwingFiles.wixobj')
        if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $installerMsi)) { throw 'WiX light failed to link the MSI.' }
    }

    $null = New-Item -ItemType Directory -Force -Path $outputDirectory
    Copy-Item -LiteralPath $stage -Destination (Join-Path $outputDirectory 'silverwing') -Recurse
    $zipPath = Join-Path $outputDirectory "silverwing-$version-windows-x64-portable.zip"
    Compress-Archive -Path (Join-Path $outputDirectory 'silverwing') -DestinationPath $zipPath -CompressionLevel Optimal
    if (-not $PortableOnly) {
        Copy-Item -LiteralPath $installerExe -Destination $outputDirectory
        Copy-Item -LiteralPath $installerMsi -Destination $outputDirectory
    }
    Write-Host "Native Go package version $version ($commit) created at $outputDirectory"
    Get-ChildItem -LiteralPath $outputDirectory -File | Select-Object Name, Length | Format-Table -AutoSize
}
finally {
    try {
        if (-not $KeepBuildTemp) {
            $safeWork = Assert-WithinRepo $workRoot
            if (Test-Path -LiteralPath $safeWork) { Remove-Item -LiteralPath $safeWork -Recurse -Force }
        }
    }
    finally {
        Pop-Location
    }
}
