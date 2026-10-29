"""Identity and template regression checks; no application or package build runs."""

import hashlib
from pathlib import Path
import re
import tempfile
import unittest

from product_profile import PROFILE_PATH, read_profile


SOURCE_ROOT = Path(__file__).resolve().parent.parent
# Hashes of the pre-refactor templates with their existing installation identity.
# Rendered metadata must reproduce those exact UTF-8/LF bytes in each branch.
_INSTALLER_HASHES = {
    '': {
        'windows-installer.nsi': '6b2df2a9b671590b5214e8e034b6e8b75d8252ee9d0313105fa0956d31d2bbe0',
        'windows-installer.wxs': 'e54a44c49cd31800e8bbd6acc1491bd51de683bd857ccf260d081ef685089561',
    },
    'qa': {
        'windows-installer.nsi': '403eb8fcb06997badcd012ebb206ea15c4915296cf926101db616c1283ea7909',
        'windows-installer.wxs': 'b051c870b936e7a9665bf116dc3b2feafd173cb841ba4a08183ff859300264fb',
    },
    'product': {
        'windows-installer.nsi': '89dfaf337613aa36dfcbd377c21ade4eb48e7a31606db1921ea7590e957a0524',
        'windows-installer.wxs': '8a05b20b84fa5945a8e2262f89a13c04c36e466b66d28cc68c5d9ec451cd9de8',
    },
}


class ProductProfileChecks(unittest.TestCase):
    def test_packaging_channel_and_application_identity_are_fixed(self):
        profile = read_profile(SOURCE_ROOT)
        expected = {
            '': ('Silverwing', 'silverwing', 'Silverwing', 'v', True, 'io.github.javatoai.silverwing'),
            'qa': ('silverwing-qa', 'silverwing-qa', 'silverwing-qa', 'qa-v', False, 'io.github.javatoai.silverwing.qa'),
            'product': ('silverwing-product', 'silverwing-product', 'silverwing-product', 'product-v', False, 'io.github.javatoai.silverwing.product'),
        }[profile['Edition']]
        self.assertEqual(tuple(profile[name] for name in [
            'Name', 'ExecutableName', 'PackageDirectoryName', 'ReleaseTagPrefix',
            'ReleaseMakeLatest', 'MacBundleID']), expected)
        self.assertEqual(profile['ReleaseRepository'], 'javatoai/silverwing')
        self.assertEqual(profile['WindowsPayloadFolderGUID'], {
            '': '98DE3F65-A4BD-4DB1-B50C-FB3B7E759870',
            'qa': 'DC9374EF-9951-5B14-AB34-99BD19D35615',
            'product': 'FA8703C2-9D9F-5E67-8FEB-2C03F4D70431',
        }[profile['Edition']])

    def test_installer_templates_preserve_original_identity_byte_for_byte(self):
        profile = read_profile(SOURCE_ROOT)
        for name, expected_digest in _INSTALLER_HASHES[profile['Edition']].items():
            with self.subTest(template=name):
                source = (SOURCE_ROOT / 'scripts' / name).read_text(encoding='utf-8-sig')
                rendered = re.sub(r'\$\{PROFILE_(\w+)\}', lambda match: profile[match[1]], source)
                rendered = re.sub(r'\$\(var\.Profile(\w+)\)', lambda match: profile[match[1]], rendered)
                self.assertEqual(hashlib.sha256(rendered.encode('utf-8')).hexdigest(), expected_digest)

    def test_packaging_reader_does_not_require_ui_labels_to_be_literals(self):
        source = (SOURCE_ROOT / PROFILE_PATH).read_text(encoding='utf-8-sig')
        for name in ['TaskKindLabel', 'ActionEditionLabel', 'MigrationFormatLabel']:
            source = re.sub(r'^(\s*' + name + r'\s*=).*$', r'\1 CLIName', source, flags=re.MULTILINE)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile_path = root / PROFILE_PATH
            profile_path.parent.mkdir(parents=True)
            profile_path.write_text(source, encoding='utf-8')
            projected = read_profile(root)
            original = read_profile(SOURCE_ROOT)
            for name in ['ExecutableName', 'ReleaseTagPrefix', 'ReleaseMakeLatest', 'MacBundleID', 'WindowsInstallerMutex']:
                self.assertEqual(projected[name], original[name])

    def test_missing_duplicate_or_invalid_profile_stops_packaging(self):
        source = (SOURCE_ROOT / PROFILE_PATH).read_text(encoding='utf-8-sig')
        variants = {
            'missing executable': re.sub(r'^\s*ExecutableName\s*=.*$', '', source, flags=re.MULTILINE),
            'duplicate executable': source + '\nconst (\n ExecutableName = "another"\n)\n',
            'unsafe executable': re.sub(r'^(\s*ExecutableName\s*=).*$', r'\1 "../other"', source, flags=re.MULTILINE),
            'nonliteral executable': re.sub(r'^(\s*ExecutableName\s*=).*$', r'\1 Name', source, flags=re.MULTILINE),
            'invalid latest type': re.sub(r'^(\s*ReleaseMakeLatest\s*=).*$', r'\1 "false"', source, flags=re.MULTILINE),
        }
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile_path = root / PROFILE_PATH
            profile_path.parent.mkdir(parents=True)
            for name, invalid in variants.items():
                with self.subTest(case=name):
                    profile_path.write_text(invalid, encoding='utf-8')
                    with self.assertRaises(RuntimeError):
                        read_profile(root)


if __name__ == '__main__':
    unittest.main(verbosity=2)
