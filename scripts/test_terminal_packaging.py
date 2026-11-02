"""Exercise the real reusable-payload terminal validation without Go/installers/UI."""

import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest


SOURCE_ROOT = Path(__file__).resolve().parent.parent
POWERSHELL = shutil.which("pwsh") or shutil.which("powershell")


@unittest.skipUnless(POWERSHELL, "PowerShell required for Windows payload consumer")
class TerminalPayloadChecks(unittest.TestCase):
    def validate(self, *, flat=True, licensed=True, corrupt=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            resources = root / "resources"
            notices = root / "notices"
            resources.mkdir()
            notices.mkdir()
            runtime = b"isolated terminal runtime"
            (resources / "ghostty-vt.dll").write_bytes(b"corrupt" if corrupt else runtime)
            license_text = "MIT License\nCopyright Ghostty contributors\n" if licensed else "Other component notice\n"
            target = resources / "THIRD-PARTY-NOTICES.txt" if flat else notices / "LICENSE"
            target.write_text(license_text, encoding="utf-8")
            manifest = root / "third_party/mygo/plugins/terminal/mygo-plugin.json"
            manifest.parent.mkdir(parents=True)
            manifest.write_text(json.dumps({"libraries": [{"name": "libghostty-vt", "files": {"windows-amd64": {
                "name": "ghostty-vt.dll", "sha256": hashlib.sha256(runtime).hexdigest()
            }}}]}), encoding="utf-8")
            source = (SOURCE_ROOT / "scripts/build-windows.ps1").read_text(encoding="utf-8-sig")
            branch = re.search(r"(?ms)^    if \(\$PayloadDirectory\) \{\n        \$terminalManifest =.*?^    \}(?=\n    else \{)", source)
            self.assertIsNotNone(branch, "terminal payload validation branch missing")
            script = root / "validate.ps1"
            script.write_text(
                "$ErrorActionPreference = 'Stop'\n$repoRoot = $args[0]\n"
                "$resourceRoot = Join-Path $repoRoot 'resources'\n$noticeRoot = Join-Path $repoRoot 'notices'\n"
                "$PayloadDirectory = $repoRoot\ntry {\n" + branch.group() +
                "\n[pscustomobject]@{accepted=$true} | ConvertTo-Json -Compress\n} catch {\n"
                "[pscustomobject]@{accepted=$false;error=$_.Exception.Message} | ConvertTo-Json -Compress\n}\n",
                encoding="utf-8",
            )
            result = subprocess.run([POWERSHELL, "-NoProfile", "-File", str(script), str(root)], capture_output=True, text=True, encoding="utf-8", timeout=20)
            self.assertEqual(result.returncode, 0, result.stderr)
            return json.loads(result.stdout)

    def test_flattened_licensed_payload_is_reusable(self):
        self.assertTrue(self.validate()["accepted"])

    def test_legacy_notice_collection_is_reusable(self):
        self.assertTrue(self.validate(flat=False)["accepted"])

    def test_missing_ghostty_license_remains_rejected(self):
        result = self.validate(licensed=False)
        self.assertFalse(result["accepted"])
        self.assertIn("license", result["error"])

    def test_corrupt_runtime_remains_rejected(self):
        result = self.validate(corrupt=True)
        self.assertFalse(result["accepted"])
        self.assertIn("checksum", result["error"])


if __name__ == "__main__":
    unittest.main()
