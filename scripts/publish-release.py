#!/usr/bin/env python3
"""Publish the four native packages only after both platform jobs succeed."""

import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys


def gh(*args):
    return subprocess.run(
        ['gh', *args], check=True, capture_output=True, text=True
    ).stdout


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def publish(asset_directory):
    repo = os.environ['GITHUB_REPOSITORY']
    ref = os.environ['GITHUB_REF']
    sha = os.environ['GITHUB_SHA']
    source = Path('internal/domain/models.go').read_text(encoding='utf-8')
    match = re.search(r'CurrentProductVersion\s*=\s*"(\d+\.\d+\.\d+)"', source)
    require(match, 'Could not read CurrentProductVersion.')
    version = match.group(1)
    tag = 'product-v' + version
    require(repo == 'javatoai/silverwing', 'Release repository does not match Silverwing.')
    require(os.environ['GITHUB_EVENT_NAME'] == 'push' and ref == 'refs/tags/' + tag,
            'Only a pushed tag matching CurrentProductVersion can publish a release.')
    require(re.fullmatch(r'[0-9a-f]{40}', sha), 'Invalid source commit.')

    directory = Path(asset_directory).resolve()
    names = sorted([
        f'silverwing-product-{version}-windows-x64-portable.zip',
        f'silverwing-product-{version}-windows-x64-setup.exe',
        f'silverwing-product-{version}-windows-x64.msi',
        f'silverwing-product-{version}-macos-arm64.dmg',
    ])
    require(sorted(path.name for path in directory.iterdir()) == names,
            'Expected exactly the four versioned Windows/macOS release packages.')
    expected = {}
    for name in names:
        path = directory / name
        require(path.is_file() and not path.is_symlink() and path.stat().st_size > 0,
                f'Package is missing, empty or not a regular file: {name}')
        with path.open('rb') as package:
            digest = hashlib.file_digest(package, 'sha256').hexdigest()
        expected[name] = (path.stat().st_size, digest)

    endpoint = f'repos/{repo}/releases'
    require(json.loads(gh('api', f'repos/{repo}/commits/{tag}'))['sha'] == sha,
            'Remote tag no longer points to this build commit.')
    # The tag endpoint only returns published releases. The authenticated
    # paginated list also includes drafts; any request failure stops here.
    pages = json.loads(gh('api', '--paginate', '--slurp', endpoint + '?per_page=100'))
    require(not any(item['tag_name'] == tag for page in pages for item in page),
            f'Release {tag} already exists; refusing to overwrite it or its assets.')

    checksums = directory / 'SHA256SUMS.txt'
    checksums.write_text(''.join(f'{expected[name][1]}  {name}\n' for name in names), encoding='ascii')
    expected[checksums.name] = (checksums.stat().st_size, hashlib.sha256(checksums.read_bytes()).hexdigest())
    notes = (
        f'silverwing-product {tag} · native Go / MyGO\n\n'
        '- Windows x64: portable ZIP, Setup EXE, MSI.\n'
        '- macOS Apple silicon (arm64): DMG, ad-hoc signed; not notarized.\n'
        '- SHA256SUMS.txt covers the four packages. No account or user configuration is bundled.\n\n'
        f'Commit: `{sha}`\n')
    assets = [str(directory / name) for name in sorted(expected)]
    payload = directory.parent / 'release-draft.json'
    payload.write_text(json.dumps({'tag_name': tag, 'target_commitish': sha,
                                  'name': f'silverwing-product {tag}', 'body': notes,
                                  'draft': True, 'prerelease': False,
                                  'generate_release_notes': True}), encoding='utf-8')
    created = json.loads(gh('api', '--method', 'POST', endpoint, '--input', str(payload)))
    release_id = created['id']
    require(type(release_id) is int and release_id > 0 and created['draft'] and created['tag_name'] == tag,
            'Create response does not identify the expected draft release.')
    endpoint += f'/{release_id}'
    gh('release', 'upload', tag, *assets, '--repo', repo)

    draft = json.loads(gh('api', endpoint))
    # target_commitish is only a creation hint when a tag already exists;
    # the resolved remote tag SHA above and below is the source identity.
    require(draft['id'] == release_id and draft['draft'] and draft['tag_name'] == tag,
            'Uploaded release does not match the expected draft ID and tag.')
    require(len(draft['assets']) == len(expected), 'Draft has an unexpected asset count.')
    for asset in draft['assets']:
        name = asset['name']
        require(name in expected, f'Unexpected draft asset: {name}')
        size, digest = expected[name]
        require(asset['state'] == 'uploaded' and asset['size'] == size
                and asset['digest'] == 'sha256:' + digest, f'Draft asset verification failed: {name}')
    require({asset['name'] for asset in draft['assets']} == set(expected), 'Draft assets are incomplete.')
    require(json.loads(gh('api', f'repos/{repo}/commits/{tag}'))['sha'] == sha,
            'Remote tag changed during upload; leaving the release as a draft.')
    publish_payload = directory.parent / 'release-publish.json'
    publish_payload.write_text(json.dumps({'draft': False, 'make_latest': 'false'}), encoding='utf-8')
    gh('api', '--method', 'PATCH', endpoint, '--input', str(publish_payload))
    result = json.loads(gh('api', endpoint))
    require(result['id'] == release_id and not result['draft'] and result['tag_name'] == tag,
            'Release publication was not confirmed.')
    print(json.dumps({'tag': tag, 'commit': sha, 'url': result['html_url'],
                      'assets': sorted(expected)}, indent=2))


if __name__ == '__main__':
    try:
        require(len(sys.argv) == 2, 'Usage: publish-release.py <asset-directory>')
        publish(sys.argv[1])
    except (RuntimeError, OSError, KeyError, subprocess.CalledProcessError) as error:
        print(f'Release stopped: {error}', file=sys.stderr)
        if isinstance(error, subprocess.CalledProcessError) and error.stderr:
            print(error.stderr.strip(), file=sys.stderr)
        sys.exit(1)
