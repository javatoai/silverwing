function New-NSISUninstallManifest([string]$Stage, [string]$Destination) {
    $root = (Resolve-Path -LiteralPath $Stage).Path
    if ((Get-Item -LiteralPath $root -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Payload root must not be a reparse point.' }
    if (Test-Path -LiteralPath $Destination) { throw 'Refusing to overwrite an uninstall manifest.' }
    $entries = @(Get-ChildItem -LiteralPath $root -Recurse -Force)
    $files = [Collections.Generic.List[string]]::new()
    $directories = [Collections.Generic.List[string]]::new()
    foreach ($entry in $entries) {
        if ($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Payload contains a reparse point: $($entry.FullName)" }
        $relative = [IO.Path]::GetRelativePath($root, $entry.FullName)
        if ([IO.Path]::IsPathRooted($relative) -or $relative -match '(^|[\\/])\.\.([\\/]|$)' -or $relative -match '["\r\n]') { throw "Unsafe payload path: $relative" }
        $escaped = $relative.Replace('$', '$$')
        if ($entry.PSIsContainer) { $directories.Add($escaped) } else { $files.Add($escaped) }
    }
    if ($files.Count -eq 0 -or -not (Test-Path -LiteralPath (Join-Path $root 'silverwing-qa.exe') -PathType Leaf)) { throw 'Native payload is incomplete.' }
    $lines = [Collections.Generic.List[string]]::new()
    $lines.Add('; Generated from this package payload. Never enumerate or recursively remove an installation at runtime.')
    foreach ($relative in ($files | Sort-Object)) {
        $lines.Add('ClearErrors')
        $lines.Add('Delete "$INSTDIR\' + $relative + '"')
        $lines.Add('IfErrors 0 +2')
        $lines.Add('StrCpy $UninstallFailed 1')
    }
    foreach ($relative in ($directories | Sort-Object { ($_ -split '\\').Count } -Descending)) {
        $lines.Add('RMDir "$INSTDIR\' + $relative + '"')
    }
    [IO.File]::WriteAllLines($Destination, $lines, [Text.UTF8Encoding]::new($true))
}
