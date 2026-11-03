"""Run the macOS artifact collection step against synthetic build output."""

from pathlib import Path
import shutil
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parent.parent


class UpdateArtifactCollectionChecks(unittest.TestCase):
    def test_only_top_level_release_assets_are_uploaded(self):
        source = (ROOT / '.github/workflows/native-build.yml').read_text(encoding='utf-8-sig')
        step = source.split('- name: Build macOS app and DMG', 1)[1]
        step = step.split('- name: Upload platform packages', 1)[0]
        script = textwrap.dedent(step.split('run: |\n', 1)[1])
        git_bash = Path('C:/Program Files/Git/bin/bash.exe')
        bash = str(git_bash) if git_bash.is_file() else shutil.which('bash')
        if not bash:
            self.skipTest('bash is unavailable')
        for signed in (False, True):
            with self.subTest(signed=signed), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / 'scripts').mkdir()
                (root / 'scripts/build-macos.sh').write_text('exit 0\n', encoding='utf-8')
                output = root / 'build/native-release/macos-arm64/version-build'
                resources = output / 'Silverwing.app/Contents/Resources'
                resources.mkdir(parents=True)
                (resources / 'update-installation.json').write_text('{}', encoding='utf-8')
                dmg = 'silverwing-1.2.3-macos-arm64.dmg'
                (output / dmg).write_bytes(b'fixture dmg')
                expected = {dmg}
                if signed:
                    for name in ('update-darwin-arm64-macos-app.json',
                                 'silverwing-1.2.3-darwin-arm64-macos-app.tar.gz'):
                        (output / name).write_bytes(b'fixture signed asset')
                        expected.add(name)
                result = subprocess.run([bash, '-e', '-c', script], cwd=root,
                                        capture_output=True, text=True, timeout=15)
                self.assertEqual(result.returncode, 0, result.stderr)
                actual = {p.name for p in (root / 'dist/macos-arm64').iterdir()}
                self.assertEqual(actual, expected)


if __name__ == '__main__':
    unittest.main()
