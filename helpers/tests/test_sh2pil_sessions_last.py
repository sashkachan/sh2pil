"""Tests for `sh2pil-sessions last`, the reader behind the f3 and f4 kitty bindings.

The binding passes a window id, and the session that window runs is in one of three stores.
Pi publishes the session in its terminal title, so sh2pil-last is asked for a Pi window.  Claude
Code and Codex publish no such name, so those windows are matched through the directory their
transcript records, which is the only thing the window and the store have in common.

The helper is loaded from its source path, so the tests run against the file in this repository
rather than an installed copy.
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


class WindowHarnessTest(unittest.TestCase):
    """Which store a window runs, as kitty reports its processes."""

    def test_a_program_of_its_own_names_the_store(self):
        self.assertEqual(sessions.window_harness([['/bin/zsh'], ['/opt/homebrew/bin/codex']]),
                         'codex')
        self.assertEqual(sessions.window_harness([['/bin/zsh'], ['/Users/me/.local/bin/claude',
                                                           '--resume', 'abc']]), 'claude')

    def test_a_script_an_interpreter_runs_names_the_store_too(self):
        # Claude Code may be started through npx, which is how a window shows `node` as its
        # program with the package path as the argument.
        self.assertEqual(sessions.window_harness([['npx', '@anthropic-ai/claude-code']]), 'claude')
        self.assertEqual(sessions.window_harness([['node', '/opt/lib/node_modules/claude-code/'
                                              'cli.js']]), 'claude')

    def test_an_argument_that_names_a_store_does_not_decide_it(self):
        # Only the program is read: a pi window that opens a Claude Code transcript is still
        # a pi window.
        self.assertEqual(sessions.window_harness([['pi', '--session',
                                              '/Users/me/.claude/projects/x/abc.jsonl']]), 'pi')

    def test_a_window_that_names_no_store_is_read_as_pi(self):
        self.assertEqual(sessions.window_harness([['/bin/zsh']]), 'pi')
        self.assertEqual(sessions.window_harness([]), 'pi')


class StoreSessionTest(unittest.TestCase):
    """The session one store holds for a directory."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.home = pathlib.Path(self.tmp.name)
        self.claude = self.home / 'claude'
        self.codex = self.home / 'codex'
        self.patch('claude_projects_dir', self.claude)
        self.patch('codex_sessions_dir', self.codex)

    def patch(self, name, value):
        patcher = patch.object(sessions, name, return_value=value)
        patcher.start()
        self.addCleanup(patcher.stop)

    def claude_transcript(self, project, identifier, cwd, mtime):
        directory = self.claude / project
        directory.mkdir(parents=True, exist_ok=True)
        path = directory / f'{identifier}.jsonl'
        entry = {'type': 'user', 'cwd': cwd,
                 'message': {'role': 'user', 'content': [{'type': 'text', 'text': 'hi'}]}}
        path.write_text(json.dumps(entry) + '\n')
        os.utime(path, (mtime, mtime))
        return path

    def codex_rollout(self, cwd, mtime, name='rollout-2026-01-01T00-00-00-019cf561-bdf5-79a0'):
        directory = self.codex / '2026' / '01' / '02'
        directory.mkdir(parents=True, exist_ok=True)
        path = directory / f'{name}.jsonl'
        path.write_text(json.dumps({'type': 'session_meta',
                                    'payload': {'id': 'aaa', 'cwd': cwd}}) + '\n')
        os.utime(path, (mtime, mtime))
        return path

    def test_the_newest_transcript_of_the_window_directory_wins(self):
        stale = self.claude_transcript('-Users-me-project', 'older', '/Users/me/project', 100)
        fresh = self.claude_transcript('-Users-me-project', 'newer', '/Users/me/project', 200)
        self.claude_transcript('-Users-me-other', 'other', '/Users/me/other', 300)
        session = sessions.newest_store_session('claude', '/Users/me/project')
        self.assertEqual(session.path, fresh)
        self.assertEqual(session.harness, 'claude')
        self.assertNotEqual(session.path, stale)

    def test_a_codex_rollout_is_matched_on_the_directory_it_records(self):
        self.codex_rollout('/Users/me/other', 300)
        wanted = self.codex_rollout('/Users/me/project', 200)
        session = sessions.newest_store_session('codex', '/Users/me/project')
        self.assertEqual(session.path, wanted)
        self.assertEqual(session.harness, 'codex')

    def test_a_directory_no_session_records_is_no_session(self):
        self.claude_transcript('-Users-me-project', 'older', '/Users/me/project', 100)
        self.assertIsNone(sessions.newest_store_session('claude', '/Users/me/elsewhere'))
        self.assertIsNone(sessions.newest_store_session('codex', '/Users/me/elsewhere'))
        self.assertIsNone(sessions.newest_store_session('claude', ''))


class LastSessionTest(unittest.TestCase):
    """What one `sh2pil-sessions last` call prints."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.home = pathlib.Path(self.tmp.name)
        self.claude = self.home / 'claude'
        self.claude.mkdir()
        patcher = patch.object(sessions, 'claude_projects_dir', return_value=self.claude)
        patcher.start()
        self.addCleanup(patcher.stop)
        # These windows run here: the remote read is `RemoteLastTest`'s subject, and a unit test
        # must not reach for the installed `sh2pil-open`.
        local = patch.object(sessions, 'remote_window_session', return_value=None)
        local.start()
        self.addCleanup(local.stop)

    def window(self, commands, cwd='/Users/me/project'):
        return patch.object(sessions, 'window_facts',
                            return_value={'id': '12', 'cwd': cwd, 'title': '',
                                          'commands': commands})

    def transcript(self):
        directory = self.claude / '-Users-me-project'
        directory.mkdir(parents=True, exist_ok=True)
        path = directory / 'abc.jsonl'
        entries = [{'type': 'user', 'cwd': '/Users/me/project',
                    'message': {'role': 'user', 'content': [{'type': 'text', 'text': 'ask'}]}},
                   {'type': 'assistant', 'cwd': '/Users/me/project',
                    'message': {'role': 'assistant',
                                'content': [{'type': 'text', 'text': 'answer'}]}}]
        path.write_text(''.join(json.dumps(entry) + '\n' for entry in entries))
        return path

    def run_last(self, *arguments):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = sessions.main(['last', *arguments])
        return code, out.getvalue(), err.getvalue()

    def args(self, full=False, print_session=False):
        return sessions.parse_args(['last', '--window', '12', '--nvim', '/usr/bin/true']
                              + (['--full'] if full else [])
                              + (['--print-session'] if print_session else []))

    def test_a_pi_window_is_handed_to_sh2pil_last_with_the_same_flags(self):
        with patch.object(sessions.subprocess, 'run') as run, self.window([['pi']]):
            run.return_value = sessions.subprocess.CompletedProcess(['sh2pil-last'], 0)
            code = sessions.last_session(self.args(full=True, print_session=True))
        self.assertEqual(code, 0)
        command = run.call_args.args[0]
        self.assertEqual(command[1], str(sessions.sh2pil_last()))
        self.assertEqual(command[2:], ['--window', '12', '--full', '--print-session',
                                       '--nvim', '/usr/bin/true'])

    def test_a_claude_window_prints_only_the_last_answer(self):
        self.transcript()
        with self.window([['/bin/zsh'], ['claude']]):
            code, out, _ = self.run_last('--window', '12')
        self.assertEqual(code, 0)
        self.assertIn('## assistant', out)
        self.assertIn('answer', out)
        self.assertNotIn('## user', out)
        with self.window([['claude']]):
            code, out, _ = self.run_last('--window', '12', '--full')
        self.assertIn('## user', out)
        self.assertIn('## assistant', out)

    def test_a_claude_window_can_print_the_path_instead(self):
        path = self.transcript()
        with self.window([['claude']]):
            code, out, err = self.run_last('--window', '12', '--print-session')
        self.assertEqual(code, 0)
        # The path goes to stderr, the way sh2pil-last reports the session it chose, so the
        # transcript it points at still reaches the reader.
        self.assertIn(f'session {path}', err)
        self.assertIn('## assistant', out)

    def test_a_window_with_no_session_says_so_instead_of_guessing(self):
        with self.window([['codex']], cwd='/Users/me/nowhere'):
            code, out, err = self.run_last('--window', '12')
        self.assertEqual(code, 1)
        self.assertEqual(out, '')
        self.assertIn('no Codex session for /Users/me/nowhere', err)

    def test_the_store_can_be_forced_for_a_window_that_names_none(self):
        path = self.transcript()
        with self.window([['/bin/zsh']]):
            code, _, err = self.run_last('--window', '12', '--harness', 'claude',
                                         '--print-session')
        self.assertEqual(code, 0)
        self.assertIn(f'session {path}', err)


class RemoteWindowSessionTest(unittest.TestCase):
    """What `sh2pil-sessions last` asks `sh2pil-open` about the window it was given."""

    def test_the_helper_names_the_host_and_the_session(self):
        payload = json.dumps({'host': 'user@192.0.2.15', 'harness': 'pi', 'session': 'a',
                              'file': '/home/me/a.jsonl', 'cwd': '/home/me/infra',
                              'name': 'Fix ingress docs'}).encode()
        completed = sessions.subprocess.CompletedProcess(['sh2pil-open'], 0, payload, b'')
        with patch.object(sessions.subprocess, 'run', return_value=completed) as run:
            answer = sessions.remote_window_session('12', 'pi')
        self.assertEqual(answer['host'], 'user@192.0.2.15')
        self.assertEqual(answer['file'], '/home/me/a.jsonl')
        command = run.call_args.args[0]
        self.assertEqual(command[1], str(sessions.sh2pil_open()))
        self.assertEqual(command[2:], ['window-session', '--window', '12',
                                       '--harness', 'pi', '--json'])
        # The child must not wait for a key: its stderr is this process's pipe, not a screen.
        self.assertEqual(run.call_args.kwargs['stdin'], sessions.subprocess.DEVNULL)

    def test_a_window_that_runs_here_is_no_host(self):
        completed = sessions.subprocess.CompletedProcess(['sh2pil-open'], 0, b'{"host": ""}', b'')
        with patch.object(sessions.subprocess, 'run', return_value=completed):
            self.assertIsNone(sessions.remote_window_session('12', 'pi'))

    def test_a_window_with_no_window_id_asks_nothing(self):
        with patch.object(sessions.subprocess, 'run') as run:
            self.assertIsNone(sessions.remote_window_session('', 'pi'))
        run.assert_not_called()

    def test_a_refusal_loses_the_helper_name_and_keeps_one_line(self):
        completed = sessions.subprocess.CompletedProcess(['sh2pil-open'], 1, b'',
                                                    b'sh2pil-open: no kitty is running\n')
        with patch.object(sessions.subprocess, 'run', return_value=completed):
            with self.assertRaises(sessions.RemoteRead) as caught:
                sessions.remote_window_session('12', 'pi')
        self.assertEqual(str(caught.exception), 'no kitty is running')

    def test_a_helper_that_cannot_be_run_is_no_host(self):
        with patch.object(sessions.subprocess, 'run', side_effect=OSError('no sh2pil-open')):
            self.assertIsNone(sessions.remote_window_session('12', 'pi'))

    def test_a_helper_too_old_for_the_verb_is_no_host(self):
        # argparse exits 2 for an unknown verb, and an older copy of the helper must not turn a
        # local window's f3 into an argument-parser message.
        completed = sessions.subprocess.CompletedProcess(['sh2pil-open'], 2, b'',
                                                    b'usage: sh2pil-open [-h]')
        with patch.object(sessions.subprocess, 'run', return_value=completed):
            self.assertIsNone(sessions.remote_window_session('12', 'pi'))


class RemoteLastTest(unittest.TestCase):
    """The same f3 and f4 views, for a chat that runs on a host the picker reads."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.home = pathlib.Path(self.tmp.name)
        self.claude = self.home / 'claude'
        self.claude.mkdir()
        patcher = patch.object(sessions, 'claude_projects_dir', return_value=self.claude)
        patcher.start()
        self.addCleanup(patcher.stop)
        facts = patch.object(sessions, 'window_facts',
                             return_value={'id': '12', 'cwd': '/Users/me/project', 'title': '',
                                           'commands': [['ssh', 'user@192.0.2.15',
                                                         'bash -lc pi']]})
        facts.start()
        self.addCleanup(facts.stop)

    def window(self, commands):
        return patch.object(sessions, 'window_facts',
                            return_value={'id': '12', 'cwd': '/Users/me/project',
                                          'title': '', 'commands': commands})

    def source(self, harness='pi'):
        return {'host': 'user@192.0.2.15', 'harness': harness, 'session': 'a',
                'file': '/home/me/.pi/agent/sessions/x/a.jsonl', 'cwd': '/home/me/infra',
                'name': 'Fix ingress docs'}

    def claude_transcript(self):
        directory = self.claude / '-Users-me-project'
        directory.mkdir(parents=True, exist_ok=True)
        path = directory / 'abc.jsonl'
        entries = [{'type': 'user', 'cwd': '/Users/me/project',
                    'message': {'role': 'user', 'content': [{'type': 'text', 'text': 'ask'}]}},
                   {'type': 'assistant', 'cwd': '/Users/me/project',
                    'message': {'role': 'assistant',
                                'content': [{'type': 'text', 'text': 'answer'}]}}]
        path.write_text(''.join(json.dumps(entry) + '\n' for entry in entries))
        return path

    def run_last(self, *arguments):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = sessions.main(['last', *arguments])
        return code, out.getvalue(), err.getvalue()

    def args(self, full=False, print_session=False):
        return sessions.parse_args(['last', '--window', '12', '--nvim', '/usr/bin/true']
                              + (['--full'] if full else [])
                              + (['--print-session'] if print_session else []))

    def test_a_pi_chat_on_a_host_is_rendered_by_sh2pil_last_from_a_local_copy(self):
        payload = b'{"type":"session","cwd":"/home/me/infra"}\n'
        seen = {}

        def run(command, check=False):
            seen['command'] = command
            seen['bytes'] = pathlib.Path(command[3]).read_bytes()
            seen['live'] = pathlib.Path(command[3]).exists()
            seen['suffix'] = pathlib.Path(command[3]).suffix
            return sessions.subprocess.CompletedProcess(command, 0)

        with self.window([['ssh', 'user@192.0.2.15', 'bash -lc pi']]), \
                patch.object(sessions, 'remote_window_session',
                             return_value=self.source()) as asked, \
                patch.object(sessions, 'fetch_transcript', return_value=payload) as fetched, \
                patch.object(sessions.subprocess, 'run', side_effect=run):
            code = sessions.last_session(self.args(full=True))
            copy = pathlib.Path(seen['command'][3])
        self.assertEqual(seen['live'], True, 'the copy must live while the reader runs')
        self.assertEqual(code, 0)
        self.assertEqual(asked.call_args.args[:2], ('12', 'pi'))
        self.assertEqual(fetched.call_args.args[:2], ('user@192.0.2.15',
                                                      '/home/me/.pi/agent/sessions/x/a.jsonl'))
        self.assertEqual(seen['command'][1], str(sessions.sh2pil_last()))
        self.assertEqual(seen['command'][2:6], ['--session', str(copy), '--display-path',
                                                '/home/me/.pi/agent/sessions/x/a.jsonl'])
        self.assertEqual(seen['command'][6:], ['--full', '--nvim', '/usr/bin/true'])
        self.assertEqual(seen['bytes'], payload)
        self.assertEqual(seen['suffix'], '.jsonl')
        self.assertFalse(copy.exists(), 'the copy must be gone when the reader returns')

    def test_a_pi_chat_on_a_host_can_print_the_host_path_instead(self):
        with patch.object(sessions, 'remote_window_session', return_value=self.source()), \
                patch.object(sessions, 'fetch_transcript', return_value=b'{}'), \
                patch.object(sessions.subprocess, 'run',
                             return_value=sessions.subprocess.CompletedProcess(['sh2pil-last'], 0)):
            code, out, err = self.run_last('--window', '12', '--print-session')
        self.assertEqual(code, 0)
        self.assertIn('sh2pil-sessions: session user@192.0.2.15:/home/me/.pi/agent/sessions/x/a.jsonl',
                      err)
        self.assertEqual(sessions.sh2pil_last().exists(), True)
        self.assertNotIn('/tmp', err, 'the local copy is not the path a reader is told')

    def test_a_claude_chat_on_a_host_keeps_the_f3_f4_difference(self):
        payload = self.claude_transcript().read_bytes()
        with self.window([['ssh', 'user@192.0.2.15', 'bash -lc claude']]), \
                patch.object(sessions, 'remote_window_session',
                             return_value=self.source('claude')), \
                patch.object(sessions, 'fetch_transcript', return_value=payload):
            code, out, _ = self.run_last('--window', '12')
            self.assertEqual(code, 0)
            self.assertIn('## assistant', out)
            self.assertNotIn('## user', out)
            code, out, _ = self.run_last('--window', '12', '--full')
        self.assertIn('## user', out)
        self.assertIn('## assistant', out)

    def test_a_host_that_names_no_session_is_refused_in_one_line(self):
        with patch.object(sessions, 'remote_window_session',
                          side_effect=sessions.RemoteRead('cannot find the session of window 12 on '
                                                     'user@192.0.2.15')):
            code, out, err = self.run_last('--window', '12')
        self.assertEqual(code, 1)
        self.assertEqual(out, '')
        self.assertIn('sh2pil-sessions: cannot find the session of window 12 on user@192.0.2.15', err)

    def test_a_read_that_fails_on_the_host_is_refused_in_one_line(self):
        with patch.object(sessions, 'remote_window_session', return_value=self.source()), \
                patch.object(sessions, 'fetch_transcript',
                             side_effect=sessions.RemoteRead('could not read /home/me/a.jsonl on '
                                                        'user@192.0.2.15')):
            code, out, err = self.run_last('--window', '12')
        self.assertEqual(code, 1)
        self.assertEqual(out, '')
        self.assertIn('sh2pil-sessions: could not read /home/me/a.jsonl on user@192.0.2.15', err)

    def test_a_window_outside_the_setting_is_read_locally_as_before(self):
        path = self.claude_transcript()
        with self.window([['claude']]), \
                patch.object(sessions, 'remote_window_session', return_value=None) as asked:
            code, out, err = self.run_last('--window', '12', '--print-session')
        self.assertEqual(code, 0)
        self.assertIn(f'session {path}', err)
        self.assertIn('## assistant', out)
        self.assertEqual(asked.call_args.args[:2], ('12', 'claude'))


if __name__ == '__main__':
    unittest.main()
