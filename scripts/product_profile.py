"""Read the fixed Go product identity without importing or executing application code."""

import json
from pathlib import Path
import re


PROFILE_PATH = Path('internal/productprofile/current.go')
_LITERAL = re.compile(r'^\s*([A-Za-z][A-Za-z0-9]*)\s*=\s*("(?:[^"\\]|\\.)*"|true|false)\s*$')
_REQUIRED = {
    'ReleaseRepository', 'Name', 'ExecutableName', 'PackageDirectoryName',
    'ReleaseTagPrefix', 'ReleaseMakeLatest', 'MacBundleID',
    'WindowsInstallName', 'WindowsRegistryNamespace', 'WindowsInstallerMutex',
    'WindowsUpgradeCode', 'WindowsStartMenuGUID', 'WindowsDesktopGUID',
    'WindowsInstallFolderGUID', 'WindowsProgramsFolderGUID', 'WindowsPayloadFolderGUID',
}


def read_profile(repository_root):
    """Return string/bool literals; missing or duplicate metadata stops packaging."""
    source = (Path(repository_root) / PROFILE_PATH).read_text(encoding='utf-8-sig')
    values = {}
    for line in source.splitlines():
        match = _LITERAL.fullmatch(line)
        if match:
            name, literal = match.groups()
            if name in values:
                raise RuntimeError(f'Duplicate product profile constant: {name}')
            values[name] = json.loads(literal)
    if not _REQUIRED.issubset(values):
        raise RuntimeError('Product profile has missing packaging metadata constants.')
    for name in _REQUIRED:
        value = values[name]
        expected = bool if name == 'ReleaseMakeLatest' else str
        if type(value) is not expected:
            raise RuntimeError(f'Invalid product profile type for {name}.')
    if not re.fullmatch(r'[a-z][a-z0-9-]*', values['ExecutableName']):
        raise RuntimeError('Product executable name is not safe for package paths.')
    if not re.fullmatch(r'[a-z-]*v', values['ReleaseTagPrefix']):
        raise RuntimeError('Product release tag prefix is invalid.')
    return values
