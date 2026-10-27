"""Focused release guard checks. All gh calls are fakes; no network writes."""

import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('publish_release', Path(__file__).with_name('publish-release.py'))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class PublishReleaseChecks(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.assets = self.root / 'dist'
        self.assets.mkdir()
        model = self.root / 'internal/domain/models.go'
        model.parent.mkdir(parents=True)
        model.write_text('const CurrentProductVersion = "2.1.8"\n')
        for suffix in ['windows-x64-portable.zip', 'windows-x64-setup.exe', 'windows-x64.msi', 'macos-arm64.dmg']:
            (self.assets / ('silverwing-qa-2.1.8-' + suffix)).write_bytes(('synthetic ' + suffix).encode())
        self.sha = 'a' * 40
        self.env = {'GITHUB_REPOSITORY': 'javatoai/silverwing', 'GITHUB_REF': 'refs/tags/qa-v2.1.8',
                    'GITHUB_SHA': self.sha, 'GITHUB_EVENT_NAME': 'push'}
        self.calls = []
        self.created = False
        self.published = False
        self.exists = False
        self.read_error = 0
        self.read_id_error = 0
        self.create_id = 123
        self.create_tag = 'qa-v2.1.8'
        self.read_id = 123
        self.read_tag = 'qa-v2.1.8'
        self.existing_published = False
        self.bad_digest = False
        self.moved_tag = False
        self.old_cwd = Path.cwd()
        os.chdir(self.root)
        self.addCleanup(os.chdir, self.old_cwd)

    def fake_gh(self, *args):
        self.calls.append(args)
        if args[0] == 'api' and '--method' in args:
            method = args[args.index('--method') + 1]
            endpoint = args[args.index('--method') + 2]
            payload = json.loads(Path(args[args.index('--input') + 1]).read_text())
            if method == 'POST':
                self.assertEqual(endpoint, 'repos/javatoai/silverwing/releases')
                self.assertEqual(payload['tag_name'], 'qa-v2.1.8')
                self.assertEqual(payload['target_commitish'], self.sha)
                self.assertTrue(payload['draft'])
                self.created = True
                return json.dumps({'id': self.create_id, 'draft': True, 'tag_name': self.create_tag})
            self.assertEqual(method, 'PATCH')
            self.assertEqual(endpoint, 'repos/javatoai/silverwing/releases/123')
            self.assertEqual(payload, {'draft': False, 'make_latest': 'false'})
            self.published = True
            return '{}'
        if args[0] == 'api' and '/commits/' in args[1]:
            return json.dumps({'sha': 'b' * 40 if self.moved_tag and self.created else self.sha})
        if args[0] == 'api' and '--paginate' in args:
            self.assertIn('--slurp', args)
            if self.read_error:
                raise subprocess.CalledProcessError(1, ['gh', *args], stderr=f'gh: error (HTTP {self.read_error})')
            existing = [{'id': 999, 'tag_name': 'qa-v2.1.8', 'draft': not self.existing_published}] if self.exists else []
            return json.dumps([[{'id': 1, 'tag_name': 'v0.0.1', 'draft': False}], existing])
        if args[0] == 'api' and '/releases/tags/' in args[1]:
            # The real tag endpoint cannot read an unpublished draft.
            raise subprocess.CalledProcessError(1, ['gh', *args], stderr='gh: Not Found (HTTP 404)')
        if args[0] == 'api':
            self.assertEqual(args[1], 'repos/javatoai/silverwing/releases/123')
            if self.read_id_error:
                raise subprocess.CalledProcessError(1, ['gh', *args], stderr=f'gh: error (HTTP {self.read_id_error})')
            assets = [{'name': path.name, 'size': path.stat().st_size, 'state': 'uploaded',
                       'digest': 'sha256:' + hashlib.sha256(path.read_bytes()).hexdigest()}
                      for path in self.assets.iterdir()]
            if self.bad_digest:
                assets[0]['digest'] = 'sha256:' + '0' * 64
            return json.dumps({'id': self.read_id, 'draft': not self.published, 'tag_name': self.read_tag,
                               'target_commitish': 'master', 'assets': assets,
                               'html_url': 'https://example.invalid/releases/v2.1.8'})
        if args[:2] == ('release', 'upload'):
            self.assertTrue(self.created)
            self.assertNotIn('--clobber', args)
            return ''
        raise AssertionError(args)

    def run_publish(self):
        with patch.dict(os.environ, self.env, clear=True), patch.object(release, 'gh', self.fake_gh):
            with contextlib.redirect_stdout(io.StringIO()):
                release.publish(self.assets)

    def test_complete_release_verifies_four_packages_and_checksum_before_publish(self):
        self.run_publish()
        self.assertTrue(self.created and self.published)
        self.assertEqual(len(list(self.assets.iterdir())), 5)
        self.assertEqual(len((self.assets / 'SHA256SUMS.txt').read_text().splitlines()), 4)
        self.assertEqual(sum('POST' in call for call in self.calls), 1)
        self.assertEqual(sum('PATCH' in call for call in self.calls), 1)
        self.assertEqual(sum(call[:2] == ('release', 'upload') for call in self.calls), 1)
        self.assertEqual(sum(call == ('api', 'repos/javatoai/silverwing/releases/123') for call in self.calls), 2)
        self.assertFalse(any('/releases/tags/' in str(call) for call in self.calls))

    def test_pr_branch_manual_wrong_tag_and_wrong_repository_never_write(self):
        for key, value in [('GITHUB_EVENT_NAME', 'pull_request'), ('GITHUB_EVENT_NAME', 'workflow_dispatch'),
                           ('GITHUB_REF', 'refs/heads/master'), ('GITHUB_REF', 'refs/tags/qa-v2.1.9'),
                           ('GITHUB_REPOSITORY', 'other/silverwing')]:
            with self.subTest(key=key, value=value):
                original = self.env[key]
                self.env[key] = value
                with self.assertRaises(RuntimeError):
                    self.run_publish()
                self.env[key] = original
        self.assertEqual(self.calls, [])

    def test_missing_empty_or_extra_package_never_writes(self):
        package = self.assets / 'silverwing-qa-2.1.8-windows-x64.msi'
        original = package.read_bytes()
        package.unlink()
        with self.assertRaises(RuntimeError):
            self.run_publish()
        package.write_bytes(b'')
        with self.assertRaises(RuntimeError):
            self.run_publish()
        package.write_bytes(original)
        (self.assets / 'unexpected.txt').write_text('synthetic')
        with self.assertRaises(RuntimeError):
            self.run_publish()
        self.assertEqual(self.calls, [])

    def test_existing_release_and_auth_failure_never_overwrite(self):
        self.exists = True
        for published in [False, True]:
            self.existing_published = published
            with self.subTest(published=published), self.assertRaisesRegex(RuntimeError, 'already exists'):
                self.run_publish()
        self.exists = False
        self.read_error = 401
        with self.assertRaises(subprocess.CalledProcessError):
            self.run_publish()
        self.assertFalse(self.created or self.published)

    def test_invalid_create_response_id_stops_before_upload(self):
        self.create_id = 0
        with self.assertRaisesRegex(RuntimeError, 'Create response'):
            self.run_publish()
        self.assertTrue(self.created)
        self.assertFalse(self.published)
        self.assertFalse(any(call[:2] == ('release', 'upload') for call in self.calls))

    def test_wrong_create_response_tag_stops_before_upload(self):
        self.create_tag = 'v9.9.9'
        with self.assertRaisesRegex(RuntimeError, 'Create response'):
            self.run_publish()
        self.assertFalse(self.published)

    def test_wrong_draft_id_cannot_publish(self):
        self.read_id = 999
        with self.assertRaisesRegex(RuntimeError, 'draft ID and tag'):
            self.run_publish()
        self.assertTrue(self.created)
        self.assertFalse(self.published)

    def test_wrong_draft_tag_cannot_publish(self):
        self.read_tag = 'v9.9.9'
        with self.assertRaisesRegex(RuntimeError, 'draft ID and tag'):
            self.run_publish()
        self.assertFalse(self.published)

    def test_draft_id_read_auth_failure_leaves_draft_unpublished(self):
        self.read_id_error = 401
        with self.assertRaises(subprocess.CalledProcessError):
            self.run_publish()
        self.assertTrue(self.created)
        self.assertFalse(self.published)

    def test_remote_tag_mismatch_never_creates_release(self):
        self.sha = 'b' * 40
        with self.assertRaisesRegex(RuntimeError, 'tag no longer points'):
            self.run_publish()
        self.assertFalse(self.created or self.published)

    def test_bad_upload_digest_leaves_only_a_draft(self):
        self.bad_digest = True
        with self.assertRaisesRegex(RuntimeError, 'asset verification failed'):
            self.run_publish()
        self.assertTrue(self.created)
        self.assertFalse(self.published)

    def test_tag_change_during_upload_leaves_only_a_draft(self):
        self.moved_tag = True
        with self.assertRaisesRegex(RuntimeError, 'changed during upload'):
            self.run_publish()
        self.assertTrue(self.created)
        self.assertFalse(self.published)


if __name__ == '__main__':
    unittest.main(verbosity=2)
