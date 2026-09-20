$ErrorActionPreference = 'Stop'

$projectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $projectRoot
try {
    # Installers are produced only after the complete test gate passes.
    $gradleArguments = @('clean', 'test', ':desktop:compileKotlin', ':desktop:createDistributable', ':desktop:packageExe', ':desktop:packageMsi', '--no-daemon')
    if ($env:SILVERWING_RELEASE_SKIP_UNSTABLE_GIT_TESTS -eq 'true') {
        $gradleArguments += '-PskipHostedGitIntegrationTests'
    }
    & .\gradlew.bat @gradleArguments
    if ($LASTEXITCODE -ne 0) {
        throw "Gradle build failed with exit code $LASTEXITCODE"
    }

    $version = (Select-String -Path 'build.gradle.kts' -Pattern 'version\s*=\s*"([^"]+)"').Matches[0].Groups[1].Value
    $portableSrc = 'desktop\build\compose\binaries\main\app\silverwing'
    $zipDir = 'desktop\build\compose\binaries\main\zip'
    New-Item -ItemType Directory -Force -Path $zipDir | Out-Null
    $zipPath = Join-Path $zipDir "silverwing-$version-portable.zip"
    if (Test-Path $zipPath) {
        Remove-Item $zipPath -Force
    }
    Compress-Archive -Path $portableSrc -DestinationPath $zipPath -CompressionLevel Optimal

    Write-Host ""
    Write-Host "Portable (green) package:"
    Write-Host "  Folder: $portableSrc"
    Write-Host "  Zip:    $zipPath"
    Write-Host "  Launch: $portableSrc\silverwing.exe"
}
finally {
    Pop-Location
}
