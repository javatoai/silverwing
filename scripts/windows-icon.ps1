function New-SilverwingWindowsIconResource {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$OutputPath,
        [Parameter(Mandatory)][string]$IconPath
    )

    $ErrorActionPreference = 'Stop'
    if ([Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
        throw 'Windows icon resource generation requires Windows.'
    }

    $iconFile = (Resolve-Path -LiteralPath $IconPath).Path
    if (-not (Test-Path -LiteralPath $iconFile -PathType Leaf)) {
        throw "Icon path is not a file: $iconFile"
    }

    $null = Add-Type -AssemblyName System.Drawing
    $iconFileBytes = [IO.File]::ReadAllBytes($iconFile)
    $pngSignature = [byte[]]@(137, 80, 78, 71, 13, 10, 26, 10)
    if ($iconFileBytes.Length -lt $pngSignature.Length) {
        throw "Icon file is not a valid PNG: $iconFile"
    }
    for ($signatureIndex = 0; $signatureIndex -lt $pngSignature.Length; $signatureIndex++) {
        if ($iconFileBytes[$signatureIndex] -ne $pngSignature[$signatureIndex]) {
            throw "Icon file is not a valid PNG: $iconFile"
        }
    }

    $sourceStream = [IO.MemoryStream]::new($iconFileBytes, $false)
    $sourceImage = $null
    $iconResources = @()
    try {
        $sourceImage = [Drawing.Image]::FromStream($sourceStream, $true, $true)
        if (-not $sourceImage.RawFormat.Equals([Drawing.Imaging.ImageFormat]::Png)) {
            throw "Icon file is not a valid PNG: $iconFile"
        }
        if ($sourceImage.Width -ne $sourceImage.Height) {
            throw "Icon PNG must be square, got $($sourceImage.Width)x$($sourceImage.Height): $iconFile"
        }

        $iconSizes = @(16, 24, 32, 48, 64, 128, 256)
        $iconId = 1
        foreach ($iconSize in $iconSizes) {
            $scaledBitmap = $null
            $graphics = $null
            $pngStream = $null
            try {
                $scaledBitmap = [Drawing.Bitmap]::new($iconSize, $iconSize, [Drawing.Imaging.PixelFormat]::Format32bppArgb)
                $graphics = [Drawing.Graphics]::FromImage($scaledBitmap)
                $graphics.Clear([Drawing.Color]::Transparent)
                $graphics.InterpolationMode = [Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
                $graphics.CompositingQuality = [Drawing.Drawing2D.CompositingQuality]::HighQuality
                $graphics.SmoothingMode = [Drawing.Drawing2D.SmoothingMode]::HighQuality
                $graphics.PixelOffsetMode = [Drawing.Drawing2D.PixelOffsetMode]::HighQuality
                $graphics.DrawImage($sourceImage, 0, 0, $iconSize, $iconSize)
                $pngStream = [IO.MemoryStream]::new()
                $scaledBitmap.Save($pngStream, [Drawing.Imaging.ImageFormat]::Png)
                $iconResources += [pscustomobject]@{
                    Id = $iconId
                    Size = $iconSize
                    Bytes = $pngStream.ToArray()
                }
                $iconId++
            }
            finally {
                if ($null -ne $pngStream) { $pngStream.Dispose() }
                if ($null -ne $graphics) { $graphics.Dispose() }
                if ($null -ne $scaledBitmap) { $scaledBitmap.Dispose() }
            }
        }
    }
    finally {
        if ($null -ne $sourceImage) { $sourceImage.Dispose() }
        $sourceStream.Dispose()
    }

    $groupStream = [IO.MemoryStream]::new()
    $groupWriter = [IO.BinaryWriter]::new($groupStream)
    try {
        $groupWriter.Write([UInt16]0)
        $groupWriter.Write([UInt16]1)
        $groupWriter.Write([UInt16]$iconResources.Count)
        foreach ($iconResource in $iconResources) {
            $dimension = if ($iconResource.Size -eq 256) { 0 } else { [byte]$iconResource.Size }
            $groupWriter.Write([byte]$dimension)
            $groupWriter.Write([byte]$dimension)
            $groupWriter.Write([byte]0)
            $groupWriter.Write([byte]0)
            $groupWriter.Write([UInt16]1)
            $groupWriter.Write([UInt16]32)
            $groupWriter.Write([UInt32]$iconResource.Bytes.Length)
            $groupWriter.Write([UInt16]$iconResource.Id)
        }
        $groupBytes = $groupStream.ToArray()
    }
    finally {
        $groupWriter.Dispose()
        $groupStream.Dispose()
    }

    $resourcePayloads = @()
    foreach ($iconResource in $iconResources) {
        $resourcePayloads += [pscustomobject]@{
            Type = 3
            Id = $iconResource.Id
            Bytes = [byte[]]$iconResource.Bytes
        }
    }
    $resourcePayloads += [pscustomobject]@{
        Type = 14
        Id = 1
        Bytes = [byte[]]$groupBytes
    }

    $resourceCount = $resourcePayloads.Count
    $iconCount = $iconResources.Count
    $rootDirectorySize = 16 + 8 * 2
    $iconTypeDirectoryOffset = $rootDirectorySize
    $iconTypeDirectorySize = 16 + 8 * $iconCount
    $groupTypeDirectoryOffset = $iconTypeDirectoryOffset + $iconTypeDirectorySize
    $groupTypeDirectorySize = 16 + 8
    $languageDirectoryStart = $groupTypeDirectoryOffset + $groupTypeDirectorySize
    $iconLanguageOffsets = @()
    for ($resourceIndex = 0; $resourceIndex -lt $iconCount; $resourceIndex++) {
        $iconLanguageOffsets += $languageDirectoryStart + 24 * $resourceIndex
    }
    $groupLanguageOffset = $languageDirectoryStart + 24 * $iconCount
    $entriesAt = $languageDirectoryStart + 24 * $resourceCount
    $dataAt = $entriesAt + 16 * $resourceCount
    $entryOffsets = @()
    for ($resourceIndex = 0; $resourceIndex -lt $resourceCount; $resourceIndex++) {
        $entryOffsets += $entriesAt + 16 * $resourceIndex
    }

    $dataStream = [IO.MemoryStream]::new()
    $dataWriter = [IO.BinaryWriter]::new($dataStream)
    $dataLocations = @()
    $rawStream = $null
    $rawWriter = $null
    $coffStream = $null
    $coffWriter = $null
    try {
        foreach ($resourcePayload in $resourcePayloads) {
            while (($dataStream.Length % 8) -ne 0) {
                $dataWriter.Write([byte]0)
            }
            $payloadBytes = [byte[]]$resourcePayload.Bytes
            $dataLocations += [pscustomobject]@{
                Offset = $dataAt + [int]$dataStream.Length
                Length = $payloadBytes.Length
            }
            $dataWriter.Write($payloadBytes, 0, $payloadBytes.Length)
        }
        $dataWriter.Flush()

        $rawStream = [IO.MemoryStream]::new()
        $rawWriter = [IO.BinaryWriter]::new($rawStream)
        $rawStream.SetLength([int64]$dataAt)

        $rawStream.Position = 0
        $rawWriter.Write([UInt32]0)
        $rawWriter.Write([UInt32]0)
        $rawWriter.Write([UInt16]0)
        $rawWriter.Write([UInt16]0)
        $rawWriter.Write([UInt16]0)
        $rawWriter.Write([UInt16]2)
        $rawWriter.Write([UInt32]3)
        $rawWriter.Write([UInt32](2147483648 + $iconTypeDirectoryOffset))
        $rawWriter.Write([UInt32]14)
        $rawWriter.Write([UInt32](2147483648 + $groupTypeDirectoryOffset))

        $rawStream.Position = $iconTypeDirectoryOffset
        $rawWriter.Write([UInt32]0)
        $rawWriter.Write([UInt32]0)
        $rawWriter.Write([UInt16]0)
        $rawWriter.Write([UInt16]0)
        $rawWriter.Write([UInt16]0)
        $rawWriter.Write([UInt16]$iconCount)
        for ($resourceIndex = 0; $resourceIndex -lt $iconCount; $resourceIndex++) {
            $rawWriter.Write([UInt32]($resourceIndex + 1))
            $rawWriter.Write([UInt32](2147483648 + $iconLanguageOffsets[$resourceIndex]))
        }

        $rawStream.Position = $groupTypeDirectoryOffset
        $rawWriter.Write([UInt32]0)
        $rawWriter.Write([UInt32]0)
        $rawWriter.Write([UInt16]0)
        $rawWriter.Write([UInt16]0)
        $rawWriter.Write([UInt16]0)
        $rawWriter.Write([UInt16]1)
        $rawWriter.Write([UInt32]1)
        $rawWriter.Write([UInt32](2147483648 + $groupLanguageOffset))

        for ($resourceIndex = 0; $resourceIndex -lt $iconCount; $resourceIndex++) {
            $rawStream.Position = $iconLanguageOffsets[$resourceIndex]
            $rawWriter.Write([UInt32]0)
            $rawWriter.Write([UInt32]0)
            $rawWriter.Write([UInt16]0)
            $rawWriter.Write([UInt16]0)
            $rawWriter.Write([UInt16]0)
            $rawWriter.Write([UInt16]1)
            $rawWriter.Write([UInt32]0)
            $rawWriter.Write([UInt32]$entryOffsets[$resourceIndex])
        }

        $rawStream.Position = $groupLanguageOffset
        $rawWriter.Write([UInt32]0)
        $rawWriter.Write([UInt32]0)
        $rawWriter.Write([UInt16]0)
        $rawWriter.Write([UInt16]0)
        $rawWriter.Write([UInt16]0)
        $rawWriter.Write([UInt16]1)
        $rawWriter.Write([UInt32]0)
        $rawWriter.Write([UInt32]$entryOffsets[$resourceCount - 1])

        for ($resourceIndex = 0; $resourceIndex -lt $resourceCount; $resourceIndex++) {
            $rawStream.Position = $entryOffsets[$resourceIndex]
            $rawWriter.Write([UInt32]$dataLocations[$resourceIndex].Offset)
            $rawWriter.Write([UInt32]$dataLocations[$resourceIndex].Length)
            $rawWriter.Write([UInt32]0)
            $rawWriter.Write([UInt32]0)
        }

        $rawStream.Position = $dataAt
        $dataStream.Position = 0
        $dataStream.CopyTo($rawStream)
        while (($rawStream.Length % 4) -ne 0) {
            $rawWriter.Write([byte]0)
        }
        $rawWriter.Flush()
        $rawBytes = $rawStream.ToArray()

        $rawFileOffset = 60
        $relocationFileOffset = $rawFileOffset + $rawBytes.Length
        $symbolTableFileOffset = $relocationFileOffset + 10 * $resourceCount
        $coffStream = [IO.MemoryStream]::new()
        $coffWriter = [IO.BinaryWriter]::new($coffStream)
        $coffWriter.Write([UInt16]0x8664)
        $coffWriter.Write([UInt16]1)
        $coffWriter.Write([UInt32]0)
        $coffWriter.Write([UInt32]$symbolTableFileOffset)
        $coffWriter.Write([UInt32]1)
        $coffWriter.Write([UInt16]0)
        $coffWriter.Write([UInt16]0)
        $coffWriter.Write([byte[]]@(46, 114, 115, 114, 99, 0, 0, 0), 0, 8)
        $coffWriter.Write([UInt32]0)
        $coffWriter.Write([UInt32]0)
        $coffWriter.Write([UInt32]$rawBytes.Length)
        $coffWriter.Write([UInt32]$rawFileOffset)
        $coffWriter.Write([UInt32]$relocationFileOffset)
        $coffWriter.Write([UInt32]0)
        $coffWriter.Write([UInt16]$resourceCount)
        $coffWriter.Write([UInt16]0)
        $coffWriter.Write([UInt32]0x40000040)
        $coffWriter.Write($rawBytes, 0, $rawBytes.Length)
        foreach ($entryOffset in $entryOffsets) {
            $coffWriter.Write([UInt32]$entryOffset)
            $coffWriter.Write([UInt32]0)
            $coffWriter.Write([UInt16]0x0003)
        }
        $coffWriter.Write([byte[]]@(46, 114, 115, 114, 99, 0, 0, 0), 0, 8)
        $coffWriter.Write([UInt32]0)
        $coffWriter.Write([Int16]1)
        $coffWriter.Write([UInt16]0)
        $coffWriter.Write([byte]3)
        $coffWriter.Write([byte]0)
        $coffWriter.Write([UInt32]4)
        $coffWriter.Flush()
        $coffBytes = $coffStream.ToArray()
    }
    finally {
        $dataWriter.Dispose()
        $dataStream.Dispose()
        if ($null -ne $rawWriter) { $rawWriter.Dispose() }
        if ($null -ne $rawStream) { $rawStream.Dispose() }
        if ($null -ne $coffWriter) { $coffWriter.Dispose() }
        if ($null -ne $coffStream) { $coffStream.Dispose() }
    }

    $outputFullPath = [IO.Path]::GetFullPath($OutputPath)
    $outputDirectory = [IO.Path]::GetDirectoryName($outputFullPath)
    if (-not (Test-Path -LiteralPath $outputDirectory -PathType Container)) {
        throw "Output directory does not exist: $outputDirectory"
    }
    if (Test-Path -LiteralPath $outputFullPath) {
        throw "Refusing to overwrite existing icon resource: $outputFullPath"
    }

    $outputStream = $null
    $ownsOutput = $false
    try {
        $outputStream = [IO.File]::Open($outputFullPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
        $ownsOutput = $true
        $outputStream.Write($coffBytes, 0, $coffBytes.Length)
        $outputStream.Flush($true)
        $outputStream.Dispose()
        $outputStream = $null
    }
    catch {
        $writeError = $_
        if ($null -ne $outputStream) {
            try { $outputStream.Dispose() } catch {}
            $outputStream = $null
        }
        if ($ownsOutput -and [IO.File]::Exists($outputFullPath)) {
            [IO.File]::Delete($outputFullPath)
        }
        throw $writeError
    }
}

function Assert-SilverwingWindowsIcon {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$ExecutablePath
    )

    $ErrorActionPreference = 'Stop'
    if ([Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
        throw 'Windows icon validation requires Windows.'
    }
    $executable = (Resolve-Path -LiteralPath $ExecutablePath).Path
    if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) {
        throw "Executable path is not a file: $executable"
    }

    if ($null -eq ('Silverwing.WindowsIcon.NativeMethods' -as [type])) {
        $nativeMethodsSource = @'
using System;
using System.Runtime.InteropServices;

namespace Silverwing.WindowsIcon
{
    public static class NativeMethods
    {
        [DllImport("kernel32.dll", EntryPoint = "LoadLibraryExW", CharSet = CharSet.Unicode, SetLastError = true)]
        public static extern IntPtr LoadLibraryExW(string fileName, IntPtr fileHandle, uint flags);

        [DllImport("kernel32.dll", EntryPoint = "FindResourceW", CharSet = CharSet.Unicode, SetLastError = true)]
        public static extern IntPtr FindResourceW(IntPtr moduleHandle, IntPtr resourceName, IntPtr resourceType);

        [DllImport("kernel32.dll", EntryPoint = "FreeLibrary", SetLastError = true)]
        [return: MarshalAs(UnmanagedType.Bool)]
        public static extern bool FreeLibrary(IntPtr moduleHandle);

        [DllImport("user32.dll", EntryPoint = "LoadImageW", CharSet = CharSet.Unicode, SetLastError = true)]
        public static extern IntPtr LoadImageW(IntPtr moduleHandle, IntPtr imageName, uint imageType, int width, int height, uint loadFlags);

        [DllImport("user32.dll", EntryPoint = "DestroyIcon", SetLastError = true)]
        [return: MarshalAs(UnmanagedType.Bool)]
        public static extern bool DestroyIcon(IntPtr iconHandle);
    }
}
'@
        $null = Add-Type -TypeDefinition $nativeMethodsSource
    }

    $nativeMethods = 'Silverwing.WindowsIcon.NativeMethods' -as [type]
    $moduleHandle = $nativeMethods::LoadLibraryExW($executable, [IntPtr]::Zero, [UInt32]2)
    if ($moduleHandle -eq [IntPtr]::Zero) {
        $win32Code = [Runtime.InteropServices.Marshal]::GetLastWin32Error()
        $win32Message = [ComponentModel.Win32Exception]::new($win32Code).Message
        throw "LoadLibraryExW failed with Win32 error $win32Code ($win32Message)."
    }

    $iconHandle = [IntPtr]::Zero
    try {
        $groupResource = $nativeMethods::FindResourceW($moduleHandle, [IntPtr]1, [IntPtr]14)
        if ($groupResource -eq [IntPtr]::Zero) {
            $win32Code = [Runtime.InteropServices.Marshal]::GetLastWin32Error()
            $win32Message = [ComponentModel.Win32Exception]::new($win32Code).Message
            throw "RT_GROUP_ICON resource ID 1 was not found; Win32 error $win32Code ($win32Message). Rebuild from source without -PayloadDirectory."
        }

        $iconHandle = $nativeMethods::LoadImageW($moduleHandle, [IntPtr]1, [UInt32]1, 32, 32, [UInt32]0)
        if ($iconHandle -eq [IntPtr]::Zero) {
            $win32Code = [Runtime.InteropServices.Marshal]::GetLastWin32Error()
            $win32Message = [ComponentModel.Win32Exception]::new($win32Code).Message
            throw "LoadImageW failed for RT_GROUP_ICON resource ID 1 with Win32 error $win32Code ($win32Message)."
        }
    }
    finally {
        if ($iconHandle -ne [IntPtr]::Zero) {
            $null = $nativeMethods::DestroyIcon($iconHandle)
        }
        $null = $nativeMethods::FreeLibrary($moduleHandle)
    }
}
