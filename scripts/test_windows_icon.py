"""Windows resource checks for the native package icon object."""

import base64
import ctypes
import os
from pathlib import Path
import shutil
import struct
import subprocess
import sys
import tempfile
import unittest


REPO_ROOT = Path(__file__).resolve().parent.parent
ICON_PATH = REPO_ROOT / 'internal' / 'desktop' / 'assets' / 'app-icon.png'
ICON_SIZES = (16, 24, 32, 48, 64, 128, 256)


@unittest.skipUnless(sys.platform == 'win32', 'Windows resource APIs are required')
class WindowsIconResourceChecks(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.helper_path = REPO_ROOT / 'scripts' / 'windows-icon.ps1'
        cls.powershell = shutil.which('pwsh') or 'pwsh'
        cls.fixture_temp = tempfile.TemporaryDirectory(prefix='银翼 icon fixture ')
        cls.addClassCleanup(cls.fixture_temp.cleanup)
        cls.fixture_root = Path(cls.fixture_temp.name)
        cls.fixture_source = cls.fixture_root / 'main.go'
        cls.fixture_source.write_text('package main\n\nfunc main() {}\n', encoding='utf-8')
        (cls.fixture_root / 'go.mod').write_text('module silverwing-icon-fixture\n\ngo 1.20\n', encoding='utf-8')
        cls.fixture_syso = cls.fixture_root / 'silverwing_windows_amd64.syso'
        create_result = cls.run_powershell(
            'New-SilverwingWindowsIconResource -OutputPath '
            + cls.powershell_literal(cls.fixture_syso)
            + ' -IconPath '
            + cls.powershell_literal(ICON_PATH)
        )
        if create_result.returncode != 0:
            raise RuntimeError(f'Could not generate the Windows icon object: {create_result.stdout}{create_result.stderr}')

        cls.fixture_executable = cls.fixture_root / 'silverwing fixture.exe'
        build_environment = os.environ.copy()
        build_environment.update({'GOOS': 'windows', 'GOARCH': 'amd64', 'CGO_ENABLED': '0', 'GOFLAGS': ''})
        build_result = subprocess.run(
            ['go', 'build', '-ldflags=-H=windowsgui', '-o', str(cls.fixture_executable), '.'],
            cwd=cls.fixture_root,
            env=build_environment,
            capture_output=True,
            text=True,
            encoding='utf-8',
            check=False,
        )
        if build_result.returncode != 0:
            raise RuntimeError(f'Could not build the Windows icon fixture: {build_result.stderr}')

        cls.kernel32 = ctypes.WinDLL('kernel32', use_last_error=True)
        cls.kernel32.LoadLibraryExW.argtypes = [ctypes.c_wchar_p, ctypes.c_void_p, ctypes.c_uint32]
        cls.kernel32.LoadLibraryExW.restype = ctypes.c_void_p
        cls.kernel32.FindResourceW.argtypes = [ctypes.c_void_p, ctypes.c_void_p, ctypes.c_void_p]
        cls.kernel32.FindResourceW.restype = ctypes.c_void_p
        cls.kernel32.LoadResource.argtypes = [ctypes.c_void_p, ctypes.c_void_p]
        cls.kernel32.LoadResource.restype = ctypes.c_void_p
        cls.kernel32.LockResource.argtypes = [ctypes.c_void_p]
        cls.kernel32.LockResource.restype = ctypes.c_void_p
        cls.kernel32.SizeofResource.argtypes = [ctypes.c_void_p, ctypes.c_void_p]
        cls.kernel32.SizeofResource.restype = ctypes.c_uint32
        cls.kernel32.FreeLibrary.argtypes = [ctypes.c_void_p]
        cls.kernel32.FreeLibrary.restype = ctypes.c_int

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='银翼 icon 测试 ')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    @staticmethod
    def powershell_literal(value):
        return "'" + str(value).replace("'", "''") + "'"

    @classmethod
    def run_powershell(cls, command):
        script = '\n'.join((
            "$ErrorActionPreference = 'Stop'",
            f". {cls.powershell_literal(cls.helper_path)}",
            command,
        ))
        encoded_command = base64.b64encode(script.encode('utf-16le')).decode('ascii')
        return subprocess.run(
            [cls.powershell, '-NoLogo', '-NoProfile', '-NonInteractive', '-EncodedCommand', encoded_command],
            capture_output=True,
            text=True,
            encoding='utf-8',
            timeout=180,
            check=False,
        )

    @classmethod
    def read_resource(cls, executable_path, resource_type, resource_id):
        module_handle = cls.kernel32.LoadLibraryExW(str(executable_path), None, 0x00000002)
        if not module_handle:
            raise ctypes.WinError(ctypes.get_last_error())
        try:
            resource_handle = cls.kernel32.FindResourceW(
                module_handle,
                ctypes.c_void_p(resource_id),
                ctypes.c_void_p(resource_type),
            )
            if not resource_handle:
                raise FileNotFoundError(f'Resource type {resource_type}, ID {resource_id} was not found.')
            loaded_resource = cls.kernel32.LoadResource(module_handle, resource_handle)
            if not loaded_resource:
                raise ctypes.WinError(ctypes.get_last_error())
            resource_size = cls.kernel32.SizeofResource(module_handle, resource_handle)
            resource_pointer = cls.kernel32.LockResource(loaded_resource)
            if not resource_pointer or resource_size == 0:
                raise ctypes.WinError(ctypes.get_last_error())
            return ctypes.string_at(resource_pointer, resource_size)
        finally:
            cls.kernel32.FreeLibrary(module_handle)

    def test_group_icon_contains_all_expected_png_sizes(self):
        group_bytes = self.read_resource(self.fixture_executable, 14, 1)
        reserved, image_type, image_count = struct.unpack_from('<HHH', group_bytes)
        self.assertEqual((reserved, image_type, image_count), (0, 1, len(ICON_SIZES)))
        self.assertEqual(len(group_bytes), 6 + image_count * 14)

        for entry_index, expected_size in enumerate(ICON_SIZES):
            entry_offset = 6 + entry_index * 14
            width, height, color_count, entry_reserved, planes, bits_per_pixel, data_length, icon_id = struct.unpack_from(
                '<BBBBHHIH', group_bytes, entry_offset
            )
            expected_dimension = 0 if expected_size == 256 else expected_size
            self.assertEqual((width, height, color_count, entry_reserved), (expected_dimension, expected_dimension, 0, 0))
            self.assertEqual((planes, bits_per_pixel, icon_id), (1, 32, entry_index + 1))

            icon_bytes = self.read_resource(self.fixture_executable, 3, icon_id)
            self.assertEqual(len(icon_bytes), data_length)
            self.assertEqual(icon_bytes[:8], b'\x89PNG\r\n\x1a\n')
            self.assertEqual(icon_bytes[12:16], b'IHDR')
            png_width, png_height = struct.unpack('>II', icon_bytes[16:24])
            self.assertEqual((png_width, png_height), (expected_size, expected_size))

    def test_go_build_info_and_fixture_process_are_valid(self):
        build_info = subprocess.run(
            ['go', 'version', '-m', str(self.fixture_executable)],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(build_info.returncode, 0, build_info.stdout + build_info.stderr)
        self.assertIn('go1.', build_info.stdout)

        process = subprocess.run([str(self.fixture_executable)], capture_output=True, timeout=30, check=False)
        self.assertEqual(process.returncode, 0, process.stdout + process.stderr)

    def test_payload_icon_assertion_reads_group_one_without_running_executable(self):
        result = self.run_powershell(
            'Assert-SilverwingWindowsIcon -ExecutablePath '
            + self.powershell_literal(self.fixture_executable)
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_repeat_generation_is_byte_identical(self):
        repeated_syso = self.root / 'repeat_windows_amd64.syso'
        result = self.run_powershell(
            'New-SilverwingWindowsIconResource -OutputPath '
            + self.powershell_literal(repeated_syso)
            + ' -IconPath '
            + self.powershell_literal(ICON_PATH)
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(repeated_syso.read_bytes(), self.fixture_syso.read_bytes())

    def test_invalid_and_missing_png_preserve_existing_output(self):
        invalid_icon = self.root / 'invalid icon.png'
        invalid_icon.write_bytes(b'not a png image')
        missing_icon = self.root / 'missing icon.png'
        sentinel = b'existing output must remain unchanged'

        for icon_path in (invalid_icon, missing_icon):
            output_path = self.root / ('output-' + icon_path.stem + '.syso')
            output_path.write_bytes(sentinel)
            result = self.run_powershell(
                'New-SilverwingWindowsIconResource -OutputPath '
                + self.powershell_literal(output_path)
                + ' -IconPath '
                + self.powershell_literal(icon_path)
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(output_path.read_bytes(), sentinel)

    def test_existing_output_is_never_overwritten(self):
        output_path = self.root / 'existing output.syso'
        sentinel = b'keep existing bytes'
        output_path.write_bytes(sentinel)

        result = self.run_powershell(
            'New-SilverwingWindowsIconResource -OutputPath '
            + self.powershell_literal(output_path)
            + ' -IconPath '
            + self.powershell_literal(ICON_PATH)
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(output_path.read_bytes(), sentinel)


if __name__ == '__main__':
    unittest.main(verbosity=2)
