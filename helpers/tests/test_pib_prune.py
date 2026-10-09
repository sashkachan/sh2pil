"""Tests for `pib prune`: which sessions an age selects, and what it refuses to remove.

The age is the only criterion the command has, so these tests pin the boundary, the two
kinds of session that stop their own delete (a running Pi chat, and an OpenCode row whose
store removes its own), and the promise that nothing is deleted without --yes.

The helper is loaded from its source path, so the tests run against the file chezmoi
manages rather than an installed copy.
"""
import contextlib
import importlib.machinery
import importlib.util
import io
import json
import os
import pathlib
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

SCRIPT = pathlib.Path(__file__).resolve().parents[1] / 'pib'
# Loading the helper from its source path writes a bytecode cache beside it, which would land
# in the chezmoi source tree and be picked up there as a managed file.  Compile nothing.
sys.dont_write_bytecode = True
loader = importlib.machinery.SourceFileLoader('pib', str(SCRIPT))
spec = importlib.util.spec_from_loader(loader.name, loader)
pib = importlib.util.module_from_spec(spec)
# The module is registered before it runs, because a dataclass at module level looks itself
# up in sys.modules while the module is still loading.
sys.modules[loader.name] = pib
loader.exec_module(pib)


class AgeGrammarTest(unittest.TestCase):
    """The field the picker and this command both parse."""

    def test_the_written_forms_are_read(self):
        cases = {
            '1d': 86400,
            '3h': 10800,
            '10m': 600,
            '1d3h10m': 86400 + 10800 + 600,
            '1d 3h': 97200,
            '2w': 1209600,
            '90s': 90,
            '1W': 604800,
        }
        for field, want in cases.items():
            with self.subTest(field=field):
                self.assertEqual(pib.parse_age(field), want)

    def test_a_number_with_no_unit_is_refused(self):
        # `7` must never be read as a week or as seven seconds: the guess would be wrong in
        # one direction or the other, and this command deletes.
        for field in ('', '   ', '7', 'd', '1x', '1d3', '-1d', '0d'):
            with self.subTest(field=field):
                with self.assertRaises(ValueError):
                    pib.parse_age(field)


class PruneTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.home = pathlib.Path(self.tmp.name)
        self.store = self.home / 'pi-store'
        self.store.mkdir()
        # The other stores are pointed at paths that do not exist, so a read of several
        # stores does not depend on what this machine happens to have installed.
        self.patch('claude_projects_dir', self.home / 'no-claude-store')
        self.patch('codex_sessions_dir', self.home / 'no-codex-store')

    def patch(self, name, value):
        patcher = patch.object(pib, name, return_value=value)
        patcher.start()
        self.addCleanup(patcher.stop)

    def transcript(self, identifier, age_seconds):
        """Write one Pi transcript whose last write was that many seconds ago."""
        project = self.store / '--project--'
        project.mkdir(exist_ok=True)
        path = project / f'2026-01-01_{identifier}.jsonl'
        path.write_text(json.dumps({'type': 'session', 'cwd': '/project'}) + '\n')
        stamp = time.time() - age_seconds
        os.utime(path, (stamp, stamp))
        return path

    def run_prune(self, *arguments):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = pib.main(['prune', '--harness', 'pi', '--sessions-dir', str(self.store),
                             *arguments])
        return code, out.getvalue(), err.getvalue()

    def test_a_dry_run_reports_the_old_sessions_and_deletes_nothing(self):
        old = self.transcript('old', 2 * 86400)
        fresh = self.transcript('fresh', 60)
        code, out, err = self.run_prune('--older-than', '1d', '--json')
        self.assertEqual(code, 0)
        payload = json.loads(out)
        self.assertEqual([row['id'] for row in payload['sessions']], ['old'])
        self.assertFalse(payload['deleted'])
        self.assertEqual(payload['deleted_sessions'], 0)
        self.assertTrue(old.exists())
        self.assertTrue(fresh.exists())
        self.assertIn('pass --yes to delete', err)

    def test_yes_deletes_the_old_sessions_and_keeps_the_rest(self):
        old = self.transcript('old', 2 * 86400)
        fresh = self.transcript('fresh', 60)
        code, out, err = self.run_prune('--older-than', '1d', '--json', '--yes')
        self.assertEqual(code, 0)
        payload = json.loads(out)
        self.assertTrue(payload['deleted'])
        self.assertEqual(payload['deleted_sessions'], 1)
        self.assertFalse(old.exists())
        self.assertTrue(fresh.exists())

    def test_the_boundary_is_the_age_itself(self):
        # Just inside the age is kept and just outside it goes, so "older than 1d" means a
        # day and not "about a day".
        just_in = self.transcript('just-in', 86400 - 60)
        just_out = self.transcript('just-out', 86400 + 60)
        _code, out, _err = self.run_prune('--older-than', '1d', '--json', '--yes')
        self.assertEqual([row['id'] for row in json.loads(out)['sessions']], ['just-out'])
        self.assertTrue(just_in.exists())
        self.assertFalse(just_out.exists())

    def test_a_running_session_is_left_alone_unless_forced(self):
        old = self.transcript('old', 2 * 86400)
        self.patch('live_ids', {'old'})
        _code, out, err = self.run_prune('--older-than', '1d', '--json', '--yes')
        payload = json.loads(out)
        self.assertEqual(payload['deleted_sessions'], 0)
        self.assertEqual([row['id'] for row in payload['running']], ['old'])
        self.assertTrue(old.exists())
        self.assertIn('--force', err)

    def test_force_deletes_a_running_session(self):
        old = self.transcript('old', 2 * 86400)
        self.patch('live_ids', {'old'})
        _code, out, _err = self.run_prune('--older-than', '1d', '--json', '--yes', '--force')
        self.assertEqual(json.loads(out)['deleted_sessions'], 1)
        self.assertFalse(old.exists())

    def test_a_store_that_removes_its_own_rows_is_left_alone(self):
        # OpenCode keeps its sessions in its own database, so its own command line is the
        # only thing that removes one.  A prune says so instead of failing row by row.
        row = pib.Session(path=self.home / 'opencode.jsonl', cwd='/project', name='',
                          size=0, mtime=time.time() - 10 * 86400, harness='opencode',
                          identifier='oc')
        self.patch('read_store', ([row], ''))
        code, out, err = self.run_prune('--older-than', '1d', '--json', '--yes')
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(out)['deleted_sessions'], 0)
        self.assertIn('left 1 OpenCode session', err)

    def test_a_store_this_machine_lacks_is_reported_and_not_fatal(self):
        code, out, err = self.run_prune('--older-than', '1d', '--harness', 'pi,claude',
                                        '--json')
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(out)['sessions'], [])
        self.assertIn('skipping Claude Code', err)

    def test_a_bad_age_is_refused_before_anything_is_read(self):
        path = self.transcript('old', 2 * 86400)
        code, out, err = self.run_prune('--older-than', '7', '--yes')
        self.assertEqual(code, 1)
        self.assertEqual(out, '')
        self.assertIn('names no unit', err)
        self.assertTrue(path.exists())


class PruneZmxTest(unittest.TestCase):
    """The zmx half: a session the picker runs inside, or one a terminal holds, is in use."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.home = pathlib.Path(self.tmp.name)
        self.store = self.home / 'pi-store'
        self.store.mkdir()
        self.patch('claude_projects_dir', self.home / 'no-claude-store')
        self.patch('codex_sessions_dir', self.home / 'no-codex-store')

    def patch(self, name, value):
        patcher = patch.object(pib, name, return_value=value)
        patcher.start()
        self.addCleanup(patcher.stop)

    def run_prune(self, *arguments):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = pib.main(['prune', '--harness', 'pi', '--sessions-dir', str(self.store),
                             *arguments])
        return code, out.getvalue(), err.getvalue()

    def entry(self, name, age_seconds):
        return {'name': name, 'chat': '', 'pi': '', 'project': 'p', 'cwd': '/p',
                'file': '', 'created': time.time() - age_seconds}

    def test_the_age_selects_the_old_sessions_and_ends_only_with_yes(self):
        aged = [self.entry('pi-old', 3 * 86400), self.entry('pi-older', 9 * 86400)]
        killed = []
        self.patch('zmx_aged', (aged, ''))
        with patch.object(pib, 'zmx_kill',
                          side_effect=lambda name: killed.append(name) or True):
            _code, out, _err = self.run_prune('--older-than', '1d', '--zmx', '--json')
            payload = json.loads(out)
            self.assertEqual([row['name'] for row in payload['zmx']], ['pi-old', 'pi-older'])
            self.assertEqual(killed, [])

            _code, out, _err = self.run_prune('--older-than', '1d', '--zmx', '--json', '--yes')
            self.assertEqual(killed, ['pi-old', 'pi-older'])
            self.assertEqual(json.loads(out)['deleted_zmx'], 2)

    def test_zmx_sessions_are_not_read_unless_asked_for(self):
        # A prune of transcripts must not end a chat because the flag was forgotten, so the
        # selection only includes zmx sessions when --zmx is given.
        fake = self.entry('pi-old', 3 * 86400)
        self.patch('zmx_aged', ([fake], ''))
        killed = []
        with patch.object(pib, 'zmx_kill',
                          side_effect=lambda name: killed.append(name) or True):
            _code, out, _err = self.run_prune('--older-than', '1d', '--json', '--yes')
        self.assertEqual(json.loads(out)['zmx'], [])
        self.assertEqual(killed, [])

    def test_a_zmx_read_that_fails_is_reported_and_not_fatal(self):
        self.patch('zmx_aged', ([], 'cannot read the zmx sessions: zmx is gone'))
        code, out, err = self.run_prune('--older-than', '1d', '--zmx', '--json')
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(out)['zmx'], [])
        self.assertIn('cannot read the zmx sessions', err)


if __name__ == '__main__':
    unittest.main()
