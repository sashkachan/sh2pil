"""Tests for what `sh2pil-sessions list` answers when no store holds a row.

A picker reads one target at a time and shows that target's stores beside its projects and
its zmx sessions.  A machine that runs none of the stores, or that has one with nothing in
it yet, must answer with no rows instead of stopping the read, or the whole machine reads as
a failure.

The helper is loaded from its source path, so the tests run against the file in this
repository rather than an installed copy.
"""
import contextlib
import importlib.machinery
import importlib.util
import io
import json
import pathlib
import sys
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = pathlib.Path(__file__).resolve().parents[1] / 'sh2pil-sessions'
# Loading the helper from its source path writes a bytecode cache beside it, which would land in
# the working tree as an untracked directory.  Compile nothing.
sys.dont_write_bytecode = True
loader = importlib.machinery.SourceFileLoader('sh2pil_sessions', str(SCRIPT))
spec = importlib.util.spec_from_loader(loader.name, loader)
sessions = importlib.util.module_from_spec(spec)
# The module is registered before it runs, because a dataclass at module level looks itself
# up in sys.modules while the module is still loading.
sys.modules[loader.name] = sessions
loader.exec_module(sessions)


class EmptyReadTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.home = pathlib.Path(self.tmp.name)
        self.absent = self.home / 'no-pi-store'
        self.blank = self.home / 'pi-store-with-no-transcripts'
        self.blank.mkdir()
        # The Claude Code and Codex stores are pointed at paths that do not exist, so a read
        # of several stores does not depend on what this machine happens to have installed.
        self.patch('claude_projects_dir', self.home / 'no-claude-store')
        self.patch('codex_sessions_dir', self.home / 'no-codex-store')

    def patch(self, name, value):
        patcher = patch.object(sessions, name, return_value=value)
        patcher.start()
        self.addCleanup(patcher.stop)

    def list_args(self, harness, root, *arguments):
        return ['list', '--harness', harness, '--sessions-dir', str(root), *arguments]

    def run_list(self, *arguments):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = sessions.main(list(arguments))
        return code, out.getvalue(), err.getvalue()

    def transcript(self, identifier='aaa', cwd='/project'):
        project = self.blank / '--project--'
        project.mkdir(exist_ok=True)
        path = project / f'2026-01-01_{identifier}.jsonl'
        path.write_text(json.dumps({'type': 'session', 'cwd': cwd}) + '\n')
        return path

    def test_a_machine_without_the_store_answers_with_no_rows(self):
        # The case a picker meets on a host that runs none of the stores: the read succeeds
        # with an empty list, so the rest of that target is still read.
        code, out, err = self.run_list(*self.list_args('pi', self.absent, '--json'))
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(out), [])
        self.assertIn('sh2pil-sessions: no session store at', err)

    def test_a_store_with_no_transcripts_answers_with_no_rows(self):
        code, out, err = self.run_list(*self.list_args('pi', self.blank, '--json'))
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(out), [])
        self.assertIn('sh2pil-sessions: no transcripts in', err)

    def test_every_store_absent_still_answers_with_no_rows(self):
        code, out, err = self.run_list(
            *self.list_args('pi,claude,codex', self.absent, '--json'))
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(out), [])
        self.assertIn('no sessions found in Pi, Claude Code, Codex', err)

    def test_a_person_reading_one_store_still_gets_the_message(self):
        # Without `--json` the reader asked for the list itself, so an empty one is reported
        # as a failure with the reason, as it was before.
        code, out, err = self.run_list(*self.list_args('pi', self.absent))
        self.assertEqual(code, 1)
        self.assertEqual(out, '')
        self.assertIn('sh2pil-sessions: no session store at', err)

    def test_a_person_reading_several_stores_still_gets_the_message(self):
        code, out, err = self.run_list(*self.list_args('pi,claude,codex', self.absent))
        self.assertEqual(code, 1)
        self.assertEqual(out, '')
        self.assertIn('sh2pil-sessions: no sessions found in Pi, Claude Code, Codex', err)

    def test_a_store_with_rows_is_read_as_before(self):
        self.transcript()
        code, out, err = self.run_list(*self.list_args('pi', self.blank, '--json'))
        self.assertEqual(code, 0)
        rows = json.loads(out)
        self.assertEqual([row['harness'] for row in rows], ['pi'])
        self.assertEqual(rows[0]['id'], 'aaa')
        self.assertEqual(rows[0]['project'], 'project')


if __name__ == '__main__':
    unittest.main()
