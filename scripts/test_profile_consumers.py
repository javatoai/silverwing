"""Execute packaging/startup consumers with synthetic files and a stub process.

No Go build, real application data, environment mutation, or desktop launch is
needed. The profile source is copied into a temporary repository for each test.
"""

import base64
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

from product_profile import read_profile


SOURCE_ROOT = Path(__file__).resolve().parent.parent
_START_STUB = r"""
# Both callbacks close over this same object, including their mutable records.
# $script: would rebind to start-local.ps1 when it invokes the mock functions.
$fixtureState = [pscustomobject]@{
    fixtureRoot = @@FIXTURE@@
    expectedHome = Join-Path ([Environment]::GetFolderPath('UserProfile')) @@EXECUTABLE@@
    expectedMigration = $null
    migrationPending = @@PENDING@@
    migrationChecks = [Collections.Generic.List[string]]::new()
    launches = [Collections.Generic.List[object]]::new()
    temporaryQueries = [Collections.Generic.List[string]]::new()
    refusedQueries = [Collections.Generic.List[string]]::new()
    guardProbes = [Collections.Generic.List[object]]::new()
}
$fixtureState.expectedMigration = Join-Path $fixtureState.expectedHome 'migrations\task-root.json'
$testPathCallback = {
    [CmdletBinding()]
    param([string]$LiteralPath, [string]$PathType)
    if ([StringComparer]::OrdinalIgnoreCase.Equals($LiteralPath, $fixtureState.expectedMigration)) {
        $fixtureState.migrationChecks.Add($LiteralPath)
        return $fixtureState.migrationPending
    }
    $fixturePrefix = $fixtureState.fixtureRoot + [IO.Path]::DirectorySeparatorChar
    if ($LiteralPath.StartsWith($fixturePrefix, [StringComparison]::OrdinalIgnoreCase)) {
        $fixtureState.temporaryQueries.Add($LiteralPath)
        return Microsoft.PowerShell.Management\Test-Path @PSBoundParameters
    }
    $fixtureState.refusedQueries.Add($LiteralPath)
    throw "Consumer test refused a path lookup outside its temporary fixture: $LiteralPath"
}.GetNewClosure()
Set-Item -Path Function:Test-Path -Value $testPathCallback
$startProcessCallback = {
    [CmdletBinding()]
    param([string]$FilePath, [string]$WorkingDirectory, [string]$WindowStyle,
          [string]$RedirectStandardOutput, [string]$RedirectStandardError,
          [string[]]$ArgumentList = @(), [switch]$PassThru)
    $fixtureState.launches.Add([ordered]@{
        binary = $FilePath; directory = $WorkingDirectory; style = $WindowStyle
        stdout = $RedirectStandardOutput; stderr = $RedirectStandardError
        arguments = @($ArgumentList); passThru = $PassThru.IsPresent
    })
    $fakeProcess = [pscustomobject]@{
        Id = 24680; StartTime = [datetime]'2026-01-01T00:00:00Z'
        HasExited = $false; Responding = $true
        MainWindowHandle = [IntPtr]1; MainWindowTitle = 'Synthetic profile consumer'
    }
    $fakeProcess | Add-Member -MemberType ScriptMethod -Name WaitForInputIdle -Value { param($timeout) return $true }
    $fakeProcess | Add-Member -MemberType ScriptMethod -Name Refresh -Value { }
    return $fakeProcess
}.GetNewClosure()
Set-Item -Path Function:Start-Process -Value $startProcessCallback
function Get-Process { throw 'Consumer test must not query a real application process.' }
$failure = $null
try {
    & @@START@@ -BuildReceipt @@BUILD@@ -ReceiptDirectory @@RECEIPTS@@ | Out-Null
} catch {
    $failure = $_.Exception.Message
}
@@GUARD_PROBES@@
$launchFile = Join-Path @@RECEIPTS@@ 'launch.json'
$receipt = $null
if (Microsoft.PowerShell.Management\Test-Path -LiteralPath $launchFile) {
    $receipt = Get-Content -LiteralPath $launchFile -Raw | ConvertFrom-Json
}
[ordered]@{ failure = $failure; migrationChecks = @($fixtureState.migrationChecks.ToArray())
            launches = @($fixtureState.launches.ToArray()); receipt = $receipt
            temporaryQueries = @($fixtureState.temporaryQueries.ToArray())
            refusedQueries = @($fixtureState.refusedQueries.ToArray())
            guardProbes = @($fixtureState.guardProbes.ToArray()) } | ConvertTo-Json -Depth 8 -Compress
"""
_GUARD_PROBES = r"""
foreach ($probe in @(
    [pscustomobject]@{ name = 'temporary child'; path = @@TEMPORARY_PROBE@@ },
    [pscustomobject]@{ name = 'same-prefix sibling'; path = @@SIBLING_PROBE@@ },
    [pscustomobject]@{ name = 'unexpected home'; path = (Join-Path ([Environment]::GetFolderPath('UserProfile')) 'silverwing-profile-guard-forbidden\never-read.json') }
)) {
    $probeValue = $null
    $probeFailure = $null
    try { $probeValue = Test-Path -LiteralPath $probe.path }
    catch { $probeFailure = $_.Exception.Message }
    $fixtureState.guardProbes.Add([ordered]@{
        name = $probe.name; path = $probe.path; value = $probeValue; failure = $probeFailure
    })
}
"""


@unittest.skipUnless(sys.platform == 'win32', 'Windows startup and packaging consumers require PowerShell on Windows')
class WindowsProductProfileConsumers(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='silverwing profile 消费者 ')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repository = self.root / 'temporary repository'
        (self.repository / 'scripts').mkdir(parents=True)
        profile_path = self.repository / 'internal/productprofile/current.go'
        profile_path.parent.mkdir(parents=True)
        shutil.copyfile(SOURCE_ROOT / 'internal/productprofile/current.go', profile_path)
        for name in ['start-local.ps1', 'nsis-payload-manifest.ps1', 'product-profile.ps1']:
            shutil.copyfile(SOURCE_ROOT / 'scripts' / name, self.repository / 'scripts' / name)
        self.executable = {
            '': 'silverwing', 'qa': 'silverwing-qa', 'product': 'silverwing-product',
        }[read_profile(SOURCE_ROOT)['Edition']]
        self.powershell = shutil.which('pwsh') or shutil.which('powershell')
        if not self.powershell:
            raise RuntimeError('PowerShell is required to execute the profile consumers.')

    @staticmethod
    def literal(value):
        return "'" + str(value).replace("'", "''") + "'"

    def run_powershell(self, source):
        script = "$ErrorActionPreference = 'Stop'\n" + source
        encoded = base64.b64encode(script.encode('utf-16le')).decode('ascii')
        result = subprocess.run(
            [self.powershell, '-NoLogo', '-NoProfile', '-NonInteractive', '-EncodedCommand', encoded],
            cwd=self.repository, capture_output=True, text=True, encoding='utf-8',
            errors='replace', check=False,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result.stdout.strip()

    def set_fixture_executable(self, executable):
        path = self.repository / 'internal/productprofile/current.go'
        source = path.read_text(encoding='utf-8-sig')
        source, count = re.subn(r'^(\s*ExecutableName\s*=).*$',
                                lambda match: match[1] + ' ' + json.dumps(executable),
                                source, count=1, flags=re.MULTILINE)
        self.assertEqual(count, 1)
        path.write_text(source, encoding='utf-8')

    def write_payload(self, name, executable):
        stage = self.root / name
        (stage / 'resources' / 'space $ 中文').mkdir(parents=True)
        (stage / (executable + '.exe')).write_bytes(b'synthetic bytes; never execute')
        (stage / 'resources' / 'space $ 中文' / 'readme.txt').write_text('keep bytes', encoding='utf-8')
        return stage

    def generate_manifest(self, stage, destination):
        helper = self.repository / 'scripts/nsis-payload-manifest.ps1'
        return self.run_powershell(
            f'. {self.literal(helper)}\n'
            f'New-NSISUninstallManifest -Stage {self.literal(stage)} -Destination {self.literal(destination)}\n'
        )

    def test_nsis_consumer_generates_manifest_for_current_executable(self):
        stage = self.write_payload('native payload', self.executable)
        destination = self.root / 'uninstall-payload.nsh'
        self.generate_manifest(stage, destination)
        source = destination.read_text(encoding='utf-8-sig')
        self.assertIn('Delete "$INSTDIR\\' + self.executable + '.exe"', source)
        self.assertIn('Delete "$INSTDIR\\resources\\space $$ 中文\\readme.txt"', source)
        self.assertNotIn('RMDir /r', source)
        self.assertEqual((stage / (self.executable + '.exe')).read_bytes(), b'synthetic bytes; never execute')

    def test_nsis_consumer_follows_profile_source_and_rejects_foreign_executable(self):
        probe = 'profile-payload-probe'
        self.set_fixture_executable(probe)
        destination = self.root / 'probe-uninstall.nsh'
        self.generate_manifest(self.write_payload('profile probe payload', probe), destination)
        self.assertIn('Delete "$INSTDIR\\' + probe + '.exe"', destination.read_text(encoding='utf-8-sig'))
        foreign = self.write_payload('foreign payload', self.executable)
        rejected = self.root / 'foreign-uninstall.nsh'
        helper = self.repository / 'scripts/nsis-payload-manifest.ps1'
        result = self.run_powershell(
            f'. {self.literal(helper)}\n'
            '$failure = $null\n'
            f'try {{ New-NSISUninstallManifest -Stage {self.literal(foreign)} -Destination {self.literal(rejected)} }} '
            'catch { $failure = $_.Exception.Message }\n'
            '[ordered]@{failure=$failure} | ConvertTo-Json -Compress\n'
        )
        self.assertEqual(json.loads(result)['failure'], 'Native payload is incomplete.')
        self.assertFalse(rejected.exists())
        self.assertTrue((foreign / (self.executable + '.exe')).exists())

    def run_start_consumer(self, executable, pending=False, guard_probes=False):
        binary = self.root / 'synthetic receipt executable.exe'
        binary.write_bytes(b'synthetic bytes; not a valid Windows executable')
        build = self.root / 'build.json'
        build.write_text(json.dumps({'sourcesStable': True, 'binary': str(binary),
                                    'binarySha256': hashlib.sha256(binary.read_bytes()).hexdigest().upper()}), encoding='utf-8')
        receipts = self.root / 'launch receipts'
        probes = ''
        if guard_probes:
            temporary_probe = self.root / 'guard probe' / 'present.txt'
            temporary_probe.parent.mkdir()
            temporary_probe.write_text('temporary lookup only', encoding='utf-8')
            sibling_probe = self.root.with_name(self.root.name + '-sibling') / 'never-read.txt'
            probes = _GUARD_PROBES.replace('@@TEMPORARY_PROBE@@', self.literal(temporary_probe))
            probes = probes.replace('@@SIBLING_PROBE@@', self.literal(sibling_probe))
        replacements = {
            '@@FIXTURE@@': self.literal(self.root), '@@EXECUTABLE@@': self.literal(executable),
            '@@PENDING@@': '$true' if pending else '$false',
            '@@GUARD_PROBES@@': probes,
            '@@START@@': self.literal(self.repository / 'scripts/start-local.ps1'),
            '@@BUILD@@': self.literal(build), '@@RECEIPTS@@': self.literal(receipts),
        }
        script = _START_STUB
        for token, value in replacements.items():
            script = script.replace(token, value)
        return json.loads(self.run_powershell(script)), binary, receipts

    def assert_start_receipt(self, result, executable, binary, receipts):
        self.assertIsNone(result['failure'])
        self.assertEqual(len(result['launches']), 1)
        launch = result['launches'][0]
        self.assertEqual(launch['binary'], str(binary))
        self.assertEqual(launch['directory'], str(self.repository))
        self.assertEqual(launch['style'], 'Normal')
        self.assertEqual(launch['arguments'], [])
        self.assertTrue(launch['passThru'])
        self.assertEqual(Path(launch['stdout']).parent, receipts)
        self.assertEqual(Path(launch['stderr']).parent, receipts)
        receipt = result['receipt']
        self.assertEqual(Path(receipt['home']), Path(receipt['userProfile']) / executable)
        self.assertEqual(receipt['arguments'], [])
        self.assertEqual(receipt['binary'], str(binary))
        self.assertEqual(result['migrationChecks'], [str(Path(receipt['home']) / 'migrations' / 'task-root.json')])
        self.assertIn(str(receipts / 'launch.json'), result['temporaryQueries'])

    def test_start_local_consumer_keeps_default_home_and_launch_contract(self):
        result, binary, receipts = self.run_start_consumer(self.executable)
        self.assert_start_receipt(result, self.executable, binary, receipts)
        self.assertEqual(result['refusedQueries'], [])

    def test_start_local_consumer_follows_profile_source_without_launching_a_process(self):
        probe = 'profile-home-probe'
        self.set_fixture_executable(probe)
        result, binary, receipts = self.run_start_consumer(probe)
        self.assert_start_receipt(result, probe, binary, receipts)
        self.assertEqual(result['refusedQueries'], [])

    def test_start_local_consumer_blocks_pending_profile_migration(self):
        result, _, _ = self.run_start_consumer(self.executable, pending=True)
        self.assertEqual(result['failure'], 'Resolve the pending task-root migration before interactive startup')
        self.assertEqual(result['launches'], [])
        self.assertIsNone(result['receipt'])
        self.assertEqual(len(result['migrationChecks']), 1)
        self.assertEqual(Path(result['migrationChecks'][0]).parts[-3:], (self.executable, 'migrations', 'task-root.json'))
        self.assertEqual(result['refusedQueries'], [])

    def test_start_local_mock_guard_keeps_temporary_and_home_boundaries(self):
        result, binary, receipts = self.run_start_consumer(self.executable, guard_probes=True)
        self.assert_start_receipt(result, self.executable, binary, receipts)
        probes = {probe['name']: probe for probe in result['guardProbes']}
        self.assertEqual(set(probes), {'temporary child', 'same-prefix sibling', 'unexpected home'})
        temporary = probes['temporary child']
        self.assertTrue(temporary['value'])
        self.assertIsNone(temporary['failure'])
        self.assertIn(temporary['path'], result['temporaryQueries'])
        refused = []
        for name in ['same-prefix sibling', 'unexpected home']:
            probe = probes[name]
            self.assertIsNone(probe['value'])
            self.assertEqual(probe['failure'], 'Consumer test refused a path lookup outside its temporary fixture: ' + probe['path'])
            self.assertNotIn(probe['path'], result['temporaryQueries'])
            refused.append(probe['path'])
        self.assertEqual(result['refusedQueries'], refused)


if __name__ == '__main__':
    unittest.main(verbosity=2)
