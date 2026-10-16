# mmdr v0.3.1 imports VCRUNTIME140.dll. Deploy that release DLL app-local;
# do not execute the redistributable or copy a machine's system runtime.
function Prepare-SilverwingWindowsRuntime {
    param(
        [Parameter(Mandatory)][string]$PayloadDirectory,
        [Parameter(Mandatory)][string]$CacheDirectory,
        [Parameter(Mandatory)][string]$WorkDirectory
    )
    $ErrorActionPreference = 'Stop'
    if ($env:OS -ne 'Windows_NT') { throw 'Windows runtime preparation requires Windows.' }
    $payload = (Resolve-Path -LiteralPath $PayloadDirectory).Path
    $rendererDirectory = Join-Path $payload 'resources/native-mermaid/windows-x64'
    if (-not (Test-Path -LiteralPath (Join-Path $rendererDirectory 'mmdr.exe') -PathType Leaf)) { throw 'mmdr.exe is missing from the payload.' }
    $runtimePath = Join-Path $rendererDirectory 'vcruntime140.dll'
    $licenseDirectory = Join-Path $WorkDirectory 'notices/vcruntime'
    $licensePath = Join-Path $licenseDirectory 'LICENSE.rtf'
    $sourcePath = Join-Path $licenseDirectory 'SOURCE.txt'
    $runtimeHash = 'd5e4d9a3e835fa679450145d6a7d94e36573a509317111904d9b3712c30d9066'
    $licenseHash = '8099dc3cf9502c335da829e5c755948a12e3e6de490eb492a99deb673d883d8b'
    $archiveHash = 'cc0ff0eb1dc3f5188ae6300faef32bf5beeba4bdd6e8e445a9184072096b713b'
    $archiveURL = 'https://download.visualstudio.microsoft.com/download/pr/bd1c8d9d-ba95-4eee-bc6e-df1fcc876373/CC0FF0EB1DC3F5188AE6300FAEF32BF5BEEBA4BDD6E8E445A9184072096B713B/VC_redist.x64.exe'
    foreach ($file in @(@{Path=$runtimePath; Hash=$runtimeHash}, @{Path=$licensePath; Hash=$licenseHash})) {
        if ((Test-Path -LiteralPath $file.Path) -and (Get-FileHash -LiteralPath $file.Path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $file.Hash) {
            throw "Existing Microsoft runtime payload checksum mismatch: $($file.Path)"
        }
    }
    if (-not (Test-Path -LiteralPath $runtimePath) -or -not (Test-Path -LiteralPath $licensePath)) {
        $null = New-Item -ItemType Directory -Force -Path $CacheDirectory, $WorkDirectory
        $archivePath = Join-Path $CacheDirectory 'vc_redist.x64-14.44.35211.0.exe'
        if (-not (Test-Path -LiteralPath $archivePath)) {
            $partialPath = $archivePath + '.' + [guid]::NewGuid().ToString('N') + '.partial'
            Invoke-WebRequest -Uri $archiveURL -OutFile $partialPath -UseBasicParsing
            if ((Get-FileHash -LiteralPath $partialPath -Algorithm SHA256).Hash.ToLowerInvariant() -ne $archiveHash) { throw 'Microsoft redistributable download checksum mismatch.' }
            Move-Item -LiteralPath $partialPath -Destination $archivePath
        }
        if ((Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant() -ne $archiveHash) { throw 'Microsoft redistributable cache checksum mismatch.' }
        $signature = Get-AuthenticodeSignature -LiteralPath $archivePath
        if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch 'O=Microsoft Corporation') { throw 'Microsoft redistributable signature is invalid.' }

        # These CAB offsets belong only to the SHA-pinned official 14.44.35211.0
        # Burn bundle. expand.exe reads CAB files; no installer/MSI is launched.
        $extractRoot = Join-Path $WorkDirectory ('vcruntime-' + [guid]::NewGuid().ToString('N'))
        $metadataDirectory = Join-Path $extractRoot 'metadata'
        $runtimeDirectory = Join-Path $extractRoot 'runtime'
        $null = New-Item -ItemType Directory -Path $extractRoot, $metadataDirectory, $runtimeDirectory
        $bytes = [IO.File]::ReadAllBytes($archivePath)
        foreach ($cab in @(@{Name='metadata'; Offset=479744; Size=195988}, @{Name='payload'; Offset=686152; Size=24939223})) {
            if ([BitConverter]::ToUInt32($bytes, $cab.Offset) -ne 0x4643534D -or [BitConverter]::ToUInt32($bytes, $cab.Offset + 8) -ne $cab.Size) { throw 'Pinned Microsoft redistributable CAB layout changed.' }
            $data = [byte[]]::new($cab.Size)
            [Buffer]::BlockCopy($bytes, $cab.Offset, $data, 0, $cab.Size)
            [IO.File]::WriteAllBytes((Join-Path $extractRoot ($cab.Name + '.cab')), $data)
        }
        $expand = Join-Path $env:WINDIR 'System32/expand.exe'
        & $expand '-F:u4' (Join-Path $extractRoot 'metadata.cab') $metadataDirectory | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Microsoft runtime license extraction failed.' }
        & $expand '-F:a12' (Join-Path $extractRoot 'payload.cab') $runtimeDirectory | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Microsoft runtime CAB extraction failed.' }
        & $expand '-F:vcruntime140.dll_amd64' (Join-Path $runtimeDirectory 'a12') $runtimeDirectory | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Microsoft runtime DLL extraction failed.' }
        $extractedRuntime = Join-Path $runtimeDirectory 'vcruntime140.dll_amd64'
        $extractedLicense = Join-Path $metadataDirectory 'u4'
        if ((Get-FileHash -LiteralPath $extractedRuntime -Algorithm SHA256).Hash.ToLowerInvariant() -ne $runtimeHash -or
            (Get-FileHash -LiteralPath $extractedLicense -Algorithm SHA256).Hash.ToLowerInvariant() -ne $licenseHash) { throw 'Extracted Microsoft runtime/license checksum mismatch.' }
        $null = New-Item -ItemType Directory -Force -Path $licenseDirectory
        if (-not (Test-Path -LiteralPath $runtimePath)) { Copy-Item -LiteralPath $extractedRuntime -Destination $runtimePath }
        if (-not (Test-Path -LiteralPath $licensePath)) { Copy-Item -LiteralPath $extractedLicense -Destination $licensePath }
    }
    $runtimeSignature = Get-AuthenticodeSignature -LiteralPath $runtimePath
    if ($runtimeSignature.Status -ne 'Valid' -or $runtimeSignature.SignerCertificate.Subject -notmatch 'O=Microsoft Corporation') { throw 'App-local Microsoft runtime signature is invalid.' }
    $source = [string]::Join("`n", @(
        'Microsoft Visual C++ VCRUNTIME140.dll 14.44.35211.0 (x64, release)',
        $archiveURL,
        "Source bundle SHA-256: $archiveHash",
        "DLL SHA-256: $runtimeHash",
        "Original English LICENSE.rtf SHA-256: $licenseHash",
        'DLL and LICENSE.rtf are retained unmodified from the official redistributable.',
        'Only the runtime directly required by mmdr v0.3.1 is deployed beside mmdr.exe.',
        'Windows 10/11 supplies the Universal CRT and other imported Windows DLLs.',
        'App-local deployment: https://learn.microsoft.com/en-us/cpp/windows/choosing-a-deployment-method',
        'Redistribution terms: https://learn.microsoft.com/en-us/cpp/windows/redistributing-visual-cpp-files',
        'Visual Studio 2022 REDIST list: https://learn.microsoft.com/en-us/visualstudio/releases/2022/redistribution',
        'Redistribution is subject to the Visual Studio license terms; update this pinned app-local DLL when servicing the package.'
    )) + "`n"
    if (Test-Path -LiteralPath $sourcePath) {
        if ([IO.File]::ReadAllText($sourcePath) -ne $source) { throw 'Existing Microsoft runtime source notice does not match the pinned deployment.' }
    } else {
        [IO.File]::WriteAllText($sourcePath, $source, [Text.UTF8Encoding]::new($false))
    }
    return [pscustomobject]@{ RuntimePath=$runtimePath; RuntimeSHA256=$runtimeHash; LicensePath=$licensePath; SourcePath=$sourcePath }
}
