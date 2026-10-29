# Read the same literal metadata used by Go; do not run Go or add a build dependency.
function Get-SilverwingProductProfile {
    param([Parameter(Mandatory)][string]$RepositoryRoot)

    $source = Get-Content -LiteralPath (Join-Path $RepositoryRoot 'internal/productprofile/current.go') -Raw -Encoding UTF8
    $values = [ordered]@{}
    $pattern = '(?m)^\s*([A-Za-z][A-Za-z0-9]*)\s*=\s*("(?:[^"\\]|\\.)*"|true|false)\s*$'
    foreach ($item in [regex]::Matches($source, $pattern)) {
        $name = $item.Groups[1].Value
        if ($values.Contains($name)) { throw "Duplicate product profile constant: $name" }
        $values[$name] = ConvertFrom-Json -InputObject $item.Groups[2].Value
    }
    foreach ($name in @('Name', 'ExecutableName', 'PackageDirectoryName', 'WindowsInstallName', 'WindowsRegistryNamespace', 'WindowsInstallerMutex', 'WindowsUpgradeCode', 'WindowsStartMenuGUID', 'WindowsDesktopGUID', 'WindowsInstallFolderGUID', 'WindowsProgramsFolderGUID', 'WindowsPayloadFolderGUID')) {
        if (-not $values.Contains($name) -or $values[$name] -isnot [string] -or -not $values[$name]) {
            throw "Missing or invalid product profile constant: $name"
        }
    }
    if ($values.ExecutableName -notmatch '^[a-z][a-z0-9-]*$') { throw 'Product executable name is not safe for package paths.' }
    return [pscustomobject]$values
}
