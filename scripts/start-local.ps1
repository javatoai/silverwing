param(
    [Parameter(Mandatory)][string]$BuildReceipt,
    [Parameter(Mandatory)][string]$ReceiptDirectory,
    [string]$PreviousLaunch
)
$ErrorActionPreference = 'Stop'
$build = Get-Content -LiteralPath $BuildReceipt -Raw | ConvertFrom-Json
if (-not $build.sourcesStable -or (Get-FileHash -LiteralPath $build.binary).Hash -ne $build.binarySha256) { throw 'A stable desktop build is required' }
$receiptRoot = [IO.Path]::GetFullPath($ReceiptDirectory)
[IO.Directory]::CreateDirectory($receiptRoot) | Out-Null
$launchFile = Join-Path $receiptRoot 'launch.json'
if (Test-Path -LiteralPath $launchFile) { throw 'Preserve the existing launch receipt' }
$userDirectory = [Environment]::GetFolderPath('UserProfile')
if (-not $userDirectory -or $env:USERPROFILE -ne $userDirectory) { throw 'Interactive startup requires the current user profile' }
$applicationHome = Join-Path $userDirectory 'silverwing-qa'
if (Test-Path -LiteralPath (Join-Path $applicationHome 'migrations\task-root.json')) { throw 'Resolve the pending task-root migration before interactive startup' }
if ($PreviousLaunch) {
    $previous = Get-Content -LiteralPath $PreviousLaunch -Raw | ConvertFrom-Json
    $oldProcess = Get-Process -Id $previous.pid -ErrorAction SilentlyContinue
    if ($oldProcess) {
        if ($oldProcess.Path -ne $previous.binary -or $oldProcess.StartTime.ToUniversalTime().Ticks -ne ([DateTime]$previous.startedAt).ToUniversalTime().Ticks) { throw 'Previous desktop identity changed' }
        if (-not $oldProcess.CloseMainWindow() -or -not $oldProcess.WaitForExit(15000)) { throw 'Previous desktop is still open; no force termination performed' }
    }
}
# Interactive use follows the executable's normal user-home default. Synthetic
# --home profiles belong only to the separate automated verification scripts.
# Omit navigation and size overrides so the normal saved session is restored.
$arguments = @()
$projectRoot = Split-Path -Parent $PSScriptRoot
$process = Start-Process -FilePath $build.binary -WorkingDirectory $projectRoot -WindowStyle Normal -RedirectStandardOutput (Join-Path $receiptRoot 'stdout.log') -RedirectStandardError (Join-Path $receiptRoot 'stderr.log') -PassThru
$idle = $process.WaitForInputIdle(5000)
$process.Refresh()
if ($process.HasExited -or -not $idle -or -not $process.Responding -or $process.MainWindowHandle -eq 0) { throw 'Desktop window did not become ready' }
[ordered]@{pid=$process.Id;startedAt=$process.StartTime.ToUniversalTime().ToString('o');binary=$build.binary;binarySha256=$build.binarySha256;home=$applicationHome;arguments=$arguments;userProfile=$userDirectory;previousLaunch=$PreviousLaunch;inputIdle=$idle;responding=$process.Responding;windowTitle=$process.MainWindowTitle;windowHandle=$process.MainWindowHandle.ToInt64()} | ConvertTo-Json | Set-Content -LiteralPath $launchFile -Encoding utf8
Get-Content -LiteralPath $launchFile
