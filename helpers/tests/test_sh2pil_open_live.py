"""Regression tests for exact Pi live-session ownership."""
import argparse
import contextlib
import importlib.machinery
import importlib.util
import io
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import Mock, patch

SCRIPT = pathlib.Path(__file__).resolve().parents[1] / 'sh2pil-open'
# Loading the helper from its source path writes a bytecode cache beside it, which would land in
# the working tree as an untracked directory.  Compile nothing.
sys.dont_write_bytecode = True
loader = importlib.machinery.SourceFileLoader('sh2pil_open', str(SCRIPT))
spec = importlib.util.spec_from_loader(loader.name, loader)
sh2pil_open = importlib.util.module_from_spec(spec)
loader.exec_module(sh2pil_open)


class LiveSessionsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = pathlib.Path(self.tmp.name) / 'sessions'
        self.project = self.root / '--project--'
        self.project.mkdir(parents=True)
        self.registry = pathlib.Path(self.tmp.name) / 'live'
        self.registry.mkdir()
        self.first = self.transcript('first')
        self.second = self.transcript('second')

    def transcript(self, identifier):
        path = self.project / f'2026-01-01_{identifier}.jsonl'
        path.write_text(json.dumps({'type': 'session', 'cwd': '/project'}) + '\n')
        return path

    def record(self, pid, file, started='Mon Jan  1 00:00:00 2026'):
        (self.registry / f'{pid}.json').write_text(json.dumps({
            'pid': pid, 'file': str(file), 'started': started,
        }))

    def test_only_registered_session_is_live_even_when_both_were_recently_written(self):
        self.record(123, self.second)
        with (patch.object(sh2pil_open, 'LIVE_DIR', self.registry),
              patch.object(sh2pil_open, 'pi_processes', return_value=[(123, '/project', 'pi')]),
              patch.object(sh2pil_open, 'process_start', return_value='Mon Jan  1 00:00:00 2026'),
              patch.object(sh2pil_open, 'kitten_prefix', return_value=None),
              patch.object(sh2pil_open, 'tmux_panes', return_value=[])):
            entries = sh2pil_open.live_sessions(self.root)
        self.assertEqual([e['id'] for e in entries], ['second'])
        self.assertEqual(entries[0]['pids'], [123])
        self.assertTrue(entries[0]['certain'])

    def test_clean_exit_and_dead_or_reused_pid_are_not_live(self):
        self.record(123, self.second)
        with (patch.object(sh2pil_open, 'LIVE_DIR', self.registry),
              patch.object(sh2pil_open, 'pi_processes', return_value=[])):
            self.assertEqual(sh2pil_open.live_sessions(self.root), [])
        with (patch.object(sh2pil_open, 'LIVE_DIR', self.registry),
              patch.object(sh2pil_open, 'pi_processes', return_value=[(123, '/project', 'pi')]),
              patch.object(sh2pil_open, 'process_start', return_value='different start')):
            self.assertEqual(sh2pil_open.live_sessions(self.root), [])
        (self.registry / '123.json').unlink()  # session_shutdown on Ctrl+D
        with (patch.object(sh2pil_open, 'LIVE_DIR', self.registry),
              patch.object(sh2pil_open, 'pi_processes', return_value=[(123, '/project', 'pi')])):
            self.assertEqual(sh2pil_open.live_sessions(self.root), [])

    def test_ambiguous_terminals_do_not_focus_a_random_pane(self):
        entry = {'pids': [123]}
        windows = [{'target': '1', 'pids': [123], 'label': 'first'},
                   {'target': '2', 'pids': [123], 'label': 'second'}]
        with patch.object(sh2pil_open, 'session_windows', return_value=windows):
            self.assertEqual(sh2pil_open.owner_window(entry), (None, '', 2))
            self.assertEqual(sh2pil_open.owner_window(entry, '2')[0]['target'], '2')
        with patch.object(sh2pil_open, 'session_windows', return_value=windows[:1]):
            self.assertEqual(sh2pil_open.owner_window(entry)[0]['target'], '1')

    def test_unknown_process_prompts_only_if_not_registered(self):
        self.record(123, self.second)
        with (patch.object(sh2pil_open, 'LIVE_DIR', self.registry),
              patch.object(sh2pil_open, 'pi_processes', return_value=[(123, '/project', 'pi')]),
              patch.object(sh2pil_open, 'process_start', return_value='Mon Jan  1 00:00:00 2026')):
            self.assertEqual(sh2pil_open.warn_untracked('/project'), 0)
            self.assertEqual(sh2pil_open.warn_untracked('/other'), 0)
        (self.registry / '123.json').unlink()
        with (patch.object(sh2pil_open, 'LIVE_DIR', self.registry),
              patch.object(sh2pil_open, 'pi_processes', return_value=[(123, '/project', 'pi')]),
              patch.object(sh2pil_open.sys.stdin, 'isatty', return_value=False)):
            self.assertEqual(sh2pil_open.warn_untracked('/project'), 1)

class LiveStateTest(unittest.TestCase):
    """The state a record carries, and the clock that makes it believable."""

    NOW = 1_800_000_000.0

    def state(self, **fields):
        return sh2pil_open.live_state(fields, self.NOW)

    def test_a_record_from_an_older_extension_says_only_that_the_chat_is_live(self):
        got = self.state(pid=1, file='/x.jsonl', started='Mon Jan  1 00:00:00 2026')
        self.assertEqual(got['state'], '')
        self.assertEqual(got['last_state'], '')
        self.assertEqual(got['label'], '')
        self.assertFalse(got['stale'])

    def test_a_fresh_working_state_is_believed_and_carries_its_age(self):
        got = self.state(state='working', at=self.NOW - 5, since=self.NOW - 300)
        self.assertEqual(got['state'], 'working')
        self.assertFalse(got['stale'])
        self.assertEqual(got['age'], 300)

    def test_a_working_state_whose_clock_stopped_is_unknown_but_not_forgotten(self):
        got = self.state(state='tool', detail='bash', at=self.NOW - 91, since=self.NOW - 200)
        self.assertEqual(got['state'], '')
        self.assertEqual(got['last_state'], 'tool')
        self.assertTrue(got['stale'])
        self.assertEqual(got['detail'], '')  # Nothing is shown for a state not believed.

    def test_a_working_state_with_no_clock_at_all_is_not_believed(self):
        got = self.state(state='working')
        self.assertEqual(got['state'], '')
        self.assertTrue(got['stale'])

    def test_a_blocking_prompt_is_believed_however_long_a_person_takes(self):
        got = self.state(state='blocked', detail='confirm', label='Allow bash?',
                         at=self.NOW - 3600, since=self.NOW - 3600)
        self.assertEqual(got['state'], 'blocked')
        self.assertFalse(got['stale'])
        self.assertEqual(got['detail'], 'confirm')
        self.assertEqual(got['label'], 'Allow bash?')
        self.assertEqual(got['age'], 3600)

    def test_a_settled_chat_is_believed_however_long_it_has_waited(self):
        got = self.state(state='idle', at=self.NOW - 7200, since=self.NOW - 7200)
        self.assertEqual(got['state'], 'idle')
        self.assertFalse(got['stale'])

    def test_a_clock_from_the_future_is_not_stale(self):
        got = self.state(state='working', at=self.NOW + 500, since=self.NOW + 400)
        self.assertEqual(got['state'], 'working')
        self.assertFalse(got['stale'])
        self.assertEqual(got['age'], 0)  # A negative age is not shown as one.

    def test_an_unknown_or_malformed_state_is_not_an_error(self):
        for value in ('dancing', 42, None, ['working'], {'state': 'working'}):
            with self.subTest(value=value):
                got = self.state(state=value, at=self.NOW)
                self.assertEqual(got['state'], '')
                self.assertFalse(got['stale'])

    def test_a_malformed_clock_is_not_a_crash(self):
        for value in ('soon', None, True, float('nan'), -1):
            with self.subTest(value=value):
                got = self.state(state='idle', detail='', at=value, since=value)
                self.assertEqual(got['state'], 'idle')
                self.assertEqual(got['age'], 0)

    def test_a_tool_name_is_kept_and_a_dialog_label_is_not_added_to_it(self):
        got = self.state(state='tool', detail='bash', label='Gate', at=self.NOW)
        self.assertEqual(got['detail'], 'bash')
        self.assertEqual(got['label'], '')  # A label belongs to a prompt, not to a tool.

    def test_a_label_is_one_short_line(self):
        got = self.state(state='blocked', detail='confirm', at=self.NOW,
                         label='Allow\nwrite  to   /etc/hosts?\t' + 'x' * 200)
        self.assertTrue(got['label'].startswith('Allow write to /etc/hosts? x'))
        self.assertEqual(len(got['label']), 60)
        self.assertNotIn('\n', got['label'])

    def test_a_label_that_is_not_text_is_dropped(self):
        got = self.state(state='blocked', detail='confirm', label=42, at=self.NOW)
        self.assertEqual(got['label'], '')

    def test_the_plain_listing_names_the_state_and_what_it_is_doing(self):
        self.assertEqual(sh2pil_open.live_state_column({'state': 'tool', 'detail': 'bash'}),
                         'tool:bash')
        self.assertEqual(sh2pil_open.live_state_column({'state': 'idle'}), 'idle')
        self.assertEqual(sh2pil_open.live_state_column({'state': '', 'stale': True}), 'stale')
        self.assertEqual(sh2pil_open.live_state_column({'state': ''}), '-')


class LiveStateThroughSessionsTest(unittest.TestCase):
    """The state reaches the listing that the picker already reads."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = pathlib.Path(self.tmp.name) / 'sessions'
        self.project = self.root / '--project--'
        self.project.mkdir(parents=True)
        self.registry = pathlib.Path(self.tmp.name) / 'live'
        self.registry.mkdir()
        self.transcript = self.project / '2026-01-01_aaa.jsonl'
        self.transcript.write_text(json.dumps({'type': 'session', 'cwd': '/project'}) + '\n')

    def write(self, **fields):
        (self.registry / '123.json').write_text(json.dumps({
            'pid': 123, 'file': str(self.transcript),
            'started': 'Mon Jan  1 00:00:00 2026', **fields}))

    def read(self):
        with (patch.object(sh2pil_open, 'LIVE_DIR', self.registry),
              patch.object(sh2pil_open, 'pi_processes', return_value=[(123, '/project', 'pi')]),
              patch.object(sh2pil_open, 'process_start', return_value='Mon Jan  1 00:00:00 2026'),
              patch.object(sh2pil_open, 'kitten_prefix', return_value=None),
              patch.object(sh2pil_open, 'tmux_panes', return_value=[])):
            return sh2pil_open.live_sessions(self.root)

    def test_a_record_from_an_older_extension_still_lists_the_session(self):
        self.write()
        entries = self.read()
        self.assertEqual([e['id'] for e in entries], ['aaa'])
        self.assertEqual(entries[0]['state'], '')
        self.assertEqual(entries[0]['pids'], [123])

    def test_a_stale_working_chat_is_still_listed_as_a_live_session(self):
        self.write(state='working', at=1, since=1)
        entries = self.read()
        self.assertEqual([e['id'] for e in entries], ['aaa'])
        self.assertEqual(entries[0]['state'], '')
        self.assertTrue(entries[0]['stale'])
        self.assertEqual(entries[0]['last_state'], 'working')

    def test_the_state_of_a_chat_that_needs_a_person_reaches_the_listing(self):
        self.write(state='blocked', detail='confirm', label='Gate', at=time.time())
        entries = self.read()
        self.assertEqual(entries[0]['state'], 'blocked')
        self.assertEqual(entries[0]['label'], 'Gate')

    def test_a_record_that_is_not_a_dictionary_is_skipped(self):
        (self.registry / '123.json').write_text('[1, 2, 3]')
        self.assertEqual(self.read(), [])


class LiveStatesTest(unittest.TestCase):
    """The cheap read: what every registered chat is doing, and nothing else."""

    NOW = 1_800_000_000.0

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.registry = pathlib.Path(self.tmp.name) / 'live'
        self.registry.mkdir()

    def write(self, name: str, body) -> None:
        path = self.registry / name
        path.write_text(body if isinstance(body, str) else json.dumps(body))

    def test_the_state_of_each_record_comes_back_with_its_transcript_id(self):
        self.write('123.json', {'pid': 123, 'started': 'x',
                                'file': '/store/project/2026-01-01_aaa.jsonl',
                                'state': 'tool', 'detail': 'bash', 'at': self.NOW})
        got = sh2pil_open.live_states(self.registry, self.NOW)
        self.assertEqual([e['id'] for e in got], ['aaa'])
        self.assertEqual(got[0]['state'], 'tool')
        self.assertEqual(got[0]['detail'], 'bash')

    def test_a_record_from_an_older_extension_comes_back_with_no_state(self):
        self.write('123.json', {'pid': 123, 'started': 'x',
                                'file': '/store/project/2026-01-01_aaa.jsonl'})
        got = sh2pil_open.live_states(self.registry, self.NOW)
        self.assertEqual(got[0]['state'], '')
        self.assertFalse(got[0]['stale'])

    def test_a_stale_working_state_is_reported_as_unknown_here_too(self):
        self.write('123.json', {'pid': 123, 'started': 'x',
                                'file': '/store/project/2026-01-01_aaa.jsonl',
                                'state': 'working', 'at': 1, 'since': 1})
        got = sh2pil_open.live_states(self.registry, self.NOW)
        self.assertEqual(got[0]['state'], '')
        self.assertEqual(got[0]['last_state'], 'working')
        self.assertTrue(got[0]['stale'])

    def test_records_that_cannot_be_read_are_skipped(self):
        self.write('1.json', 'not json at all')
        self.write('2.json', [1, 2, 3])
        self.write('3.json', {'pid': 3})          # no transcript named
        self.write('4.json', {'pid': 4, 'file': ''})
        self.write('5.json', {'pid': 5, 'file': '/store/project/2026-01-01_bbb.jsonl'})
        got = sh2pil_open.live_states(self.registry, self.NOW)
        self.assertEqual([e['id'] for e in got], ['bbb'])

    def test_no_records_is_not_an_error(self):
        self.assertEqual(sh2pil_open.live_states(self.registry, self.NOW), [])

    def test_the_read_does_not_check_the_process_or_the_transcript(self):
        # Neither a running process nor a transcript on disk is needed: this read answers what a
        # chat is doing, and the caller keeps it beside the answer `live` gave.
        self.write('999999.json', {'pid': 999999, 'started': 'long ago',
                                   'file': '/nowhere/2026-01-01_ccc.jsonl',
                                   'state': 'idle', 'at': self.NOW, 'since': self.NOW - 30})
        with patch.object(sh2pil_open, 'pi_processes', side_effect=AssertionError('no process check')):
            got = sh2pil_open.live_states(self.registry, self.NOW)
        self.assertEqual(got[0]['state'], 'idle')
        self.assertEqual(got[0]['age'], 30)


class ZmxSessionsTest(unittest.TestCase):
    """The zmx flag, its naming rules, and the window lookup that replaces ancestry."""

    def test_remote_taken_names_include_ended_zmx_sessions(self):
        completed = sh2pil_open.subprocess.CompletedProcess(
            ['ssh'], 0, b'pi-api\npi-docs\n', b'')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=completed) as run:
            names = sh2pil_open.zmx_remote_taken_names('build-host')
        self.assertEqual(names, {'pi-api', 'pi-docs'})
        command = run.call_args.args[0]
        self.assertEqual(command[9], 'build-host')
        self.assertIn('ls --short', command[10])

    def test_remote_projects_uses_zoxide_and_returns_absolute_unique_paths(self):
        completed = sh2pil_open.subprocess.CompletedProcess(
            ['ssh'], 0, b'/srv/one\n/srv/two\nrelative\n/srv/one\n', b'')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=completed) as run:
            paths = sh2pil_open.zmx_remote_projects('build-host')
        self.assertEqual(paths, ['/srv/one', '/srv/two'])
        command = run.call_args.args[0]
        self.assertEqual(command[9], 'build-host')
        self.assertIn('/bin/zsh -lic', command[10])
        self.assertIn('zoxide query -l', command[10])
        run.assert_called_once_with(
            command, capture_output=True, timeout=12, env=sh2pil_open.ssh_env())

    def test_remote_projects_pick_the_hosts_own_login_shell(self):
        # fedora has bash and no zsh, and a command that named zsh stopped there with
        # "No such file or directory" before zoxide ran at all.
        command = sh2pil_open.zoxide_remote_command('query', '-l')
        self.assertIn("[ -x /bin/zsh ] && exec /bin/zsh -lic 'zoxide query -l'", command)
        self.assertIn("[ -x /bin/bash ] && exec /bin/bash -lic 'zoxide query -l'", command)
        self.assertIn("exec sh -c 'zoxide query -l'", command)
        self.assertLess(command.index('/bin/bash'), command.index('exec sh -c'))

    def test_the_shell_snippet_runs_the_command_in_bash_when_the_host_has_no_zsh(self):
        # The snippet is plain sh, so a host that has neither zsh nor bash still runs the
        # command, and the same snippet can be checked here against a host that has both.
        # zsh is only checked where it exists, because a Linux CI runner has no /bin/zsh
        # and the first case would then say nothing about the snippet.
        if pathlib.Path('/bin/zsh').exists():
            with patch.object(sh2pil_open, 'REMOTE_SHELLS', ('/bin/zsh', '/bin/bash')):
                in_zsh = self.run_snippet("printf 'shell=%s\\n' \"$0\"")
            self.assertEqual(in_zsh, 'shell=/bin/zsh\n')
        with patch.object(sh2pil_open, 'REMOTE_SHELLS', ('/bin/no-such-zsh', '/bin/bash')):
            in_bash = self.run_snippet("printf 'shell=%s\\n' \"$0\"")
        self.assertEqual(in_bash, 'shell=/bin/bash\n')
        with patch.object(sh2pil_open, 'REMOTE_SHELLS', ('/bin/no-such-zsh', '/bin/no-such-bash')):
            bare = self.run_snippet("printf 'shell=%s\\n' \"$0\"")
        self.assertEqual(bare, 'shell=sh\n')

    def run_snippet(self, command):
        """Run what a host would run: the snippet, handed to /bin/sh."""
        result = sh2pil_open.subprocess.run(['/bin/sh', '-c', sh2pil_open.remote_login_command(command)],
                                         capture_output=True, text=True)
        self.assertEqual(result.returncode, 0)
        return result.stdout

    def test_remote_zoxide_binary_override_is_validated(self):
        with patch.object(sh2pil_open, 'CONFIG', self.config):
            self.config.write_text('zoxide_remote_binary: ~/.local/bin/zoxide\n')
            self.assertEqual(sh2pil_open.zoxide_remote_command('query', '-l'),
                             'exec "$HOME"/.local/bin/zoxide query -l')
            self.config.write_text('zoxide_remote_binary: relative/zoxide\n')
            with self.assertRaises(ValueError):
                sh2pil_open.zoxide_remote_command('query', '-l')

    def test_remote_list_reads_session_rows_without_pi_metadata(self):
        output = (
            '→\\tname=dev\\tclients=1\\tcreated=200\\tcwd=file://host/srv/project\\t'
            'project=api\\tcmd=pi --name API\n'
            '  \\tname=shell\\tclients=0\\tcreated=100\\tcwd=file://host/home/me\\n'
        ).replace('\\t', '\t')
        completed = sh2pil_open.subprocess.CompletedProcess(
            ['ssh'], 0, output.encode(), b'')
        with (patch.object(sh2pil_open, 'CONFIG', self.config),
              patch.object(sh2pil_open.subprocess, 'run', return_value=completed) as run):
            expected_remote = sh2pil_open.remote_zmx_command('list')
            rows = sh2pil_open.zmx_remote_sessions('build-host')
        self.assertEqual([row['name'] for row in rows], ['dev', 'shell'])
        self.assertEqual(rows[0]['cwd'], '/srv/project')
        self.assertEqual(rows[0]['clients'], 1)
        self.assertEqual(rows[0]['chat'], '')
        self.assertEqual(rows[0]['file'], '')
        run.assert_called_once_with(
            [sh2pil_open.ssh_binary(), '-S', sh2pil_open.ssh_control_path(),
             '-o', 'ControlMaster=no', '-o', 'BatchMode=yes',
             '-o', 'ConnectTimeout=5', 'build-host', expected_remote],
            capture_output=True, timeout=8, env=sh2pil_open.ssh_env())

    def test_zmx_connect_reuses_a_live_master_without_authentication(self):
        healthy = sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, b'', b'')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=healthy) as run:
            self.assertEqual(sh2pil_open.zmx_connect('build-host'), 0)
        run.assert_called_once_with(
            [sh2pil_open.ssh_binary(), '-S', sh2pil_open.ssh_control_path(),
             '-O', 'check', 'build-host'],
            capture_output=True, timeout=5, env=sh2pil_open.ssh_env())

    def test_zmx_connect_restart_ends_the_master_then_starts_a_new_one(self):
        # A restart is for a master that is stale or wrong: the check is ignored, the old
        # master is ended, and a new one is established.
        alive = sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, b'', b'')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=alive) as run, \
             patch.object(sh2pil_open.subprocess, 'Popen') as popen:
            popen.return_value.stderr = io.StringIO('')
            popen.return_value.wait.return_value = 0
            self.assertEqual(sh2pil_open.zmx_connect('build-host', restart=True), 0)
        self.assertEqual(run.call_count, 2)
        check = run.call_args_list[0].args[0]
        end = run.call_args_list[1].args[0]
        self.assertIn('-O', check)
        self.assertIn('check', check)
        self.assertIn('-O', end)
        self.assertIn('exit', end)
        self.assertEqual(run.call_args_list[1].kwargs['env'], sh2pil_open.ssh_env())
        self.assertTrue(popen.called)

    def test_zmx_connect_starts_a_persistent_master_when_none_exists(self):
        # The check is one `ssh -O check`; the master itself is started with Popen, because
        # its stderr is relayed line by line while a YubiKey touch is waited for.  Both are
        # mocked here: a real ssh would try to resolve the host and turn a unit test into a
        # network test.  HOME is a temporary directory that holds an agent socket, so the
        # test says the same thing on every machine instead of reading the one it runs on.
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        home = pathlib.Path(tmp.name)
        (home / '.ssh').mkdir()
        (home / '.ssh' / 'agent.sock').touch()
        dead = sh2pil_open.subprocess.CompletedProcess(['ssh'], 255, b'', b'')
        with patch.dict(os.environ, {'HOME': str(home)}):
            control_path = sh2pil_open.ssh_control_path()
            with patch.object(sh2pil_open.subprocess, 'run', return_value=dead) as run, \
                 patch.object(sh2pil_open.subprocess, 'Popen') as popen:
                popen.return_value.stderr = io.StringIO('')
                popen.return_value.wait.return_value = 0
                self.assertEqual(sh2pil_open.zmx_connect('build-host'), 0)
            command = popen.call_args_list[0].args[0]
            self.assertIn('-M', command)
            self.assertIn('-N', command)
            self.assertIn('-f', command)
            self.assertIn('ControlPersist=12h', command)
            self.assertIn('PreferredAuthentications=publickey', command)
            self.assertIn('PasswordAuthentication=no', command)
            self.assertIn('KbdInteractiveAuthentication=no', command)
            self.assertIn('BatchMode=no', command)
            self.assertEqual(command[0], sh2pil_open.ssh_binary())
            self.assertEqual(popen.call_args.kwargs['env']['SSH_AUTH_SOCK'],
                             str(home / '.ssh' / 'agent.sock'))
            self.assertEqual(command[-1], 'build-host')
            self.assertEqual(run.call_args.args[0],
                             [sh2pil_open.ssh_binary(), '-S', control_path,
                              '-O', 'check', 'build-host'])

    def test_remote_zmx_discovery_prefers_configured_binary_then_mise_shim(self):
        with patch.object(sh2pil_open, 'CONFIG', self.config):
            default = sh2pil_open.remote_zmx_command('list')
        self.assertIn('command -v zmx', default)
        self.assertIn('mise/shims/zmx', default)
        self.config.write_text('zmx_remote_binary: ~/.local/share/mise/shims/zmx\n')
        with patch.object(sh2pil_open, 'CONFIG', self.config):
            command = sh2pil_open.remote_zmx_command('list')
        self.assertEqual(command, 'exec "$HOME"/.local/share/mise/shims/zmx list')

    def test_remote_list_accepts_user_at_ip_destinations(self):
        output = '→\\tname=dev\\tclients=1\\tcreated=200\\tcwd=/srv/project\\n'.replace(
            '\\t', '\t')
        completed = sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, output.encode(), b'')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=completed) as run:
            rows = sh2pil_open.zmx_remote_sessions('user@computer')
        self.assertEqual([row['name'] for row in rows], ['dev'])
        self.assertEqual(run.call_args.args[0][9], 'user@computer')
        self.assertTrue(sh2pil_open.valid_ssh_destination('alice@192.0.2.10'))
        self.assertTrue(sh2pil_open.valid_ssh_destination('alice@2001:db8::10'))

    def test_remote_list_rejects_non_destinations_and_ssh_options(self):
        for value in ('-oProxyCommand=evil', 'alice@bad host', 'alice@computer;id'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                sh2pil_open.zmx_remote_sessions(value)

    def test_remote_new_chat_creates_a_unique_zmx_session_and_launches_ssh(self):
        with (patch.object(sh2pil_open, 'CONFIG', self.config),
              patch.object(sh2pil_open, 'zmx_remote_taken_names',
                           return_value={'pi-home-infra'}),
              patch.object(sh2pil_open, 'launch', return_value=0) as launch):
            result = sh2pil_open.zmx_remote_new('build-host', '/srv/home infra', 'pane')
        self.assertEqual(result, 0)
        argv, cwd, label, place, emit = launch.call_args.args
        self.assertEqual(argv[:3], [sh2pil_open.ssh_binary(), '-t', 'build-host'])
        self.assertIn("cd -- '/srv/home infra'", argv[3])
        self.assertIn("attach --labels project=home-infra pi-home-infra-2 /bin/sh -c", argv[3])
        self.assertIn('/bin/zsh -lic', argv[3])
        self.assertIn('-x /bin/bash', argv[3])
        self.assertEqual((cwd, label, place, emit),
                         (str(pathlib.Path.home()), '', 'pane', False))

    def test_remote_new_chat_rejects_invalid_destinations_and_relative_paths(self):
        with patch.object(sh2pil_open, 'launch') as launch:
            self.assertEqual(sh2pil_open.zmx_remote_new('-oProxyCommand=evil', '/srv/app', 'tab'), 1)
            self.assertEqual(sh2pil_open.zmx_remote_new('build-host', 'relative/app', 'tab'), 1)
        launch.assert_not_called()

    def test_remote_attach_uses_ssh_and_quotes_the_session_name(self):
        with (patch.object(sh2pil_open, 'CONFIG', self.config),
              patch.object(sh2pil_open, 'launch', return_value=0) as launch):
            result = sh2pil_open.zmx_remote_switch('build-host', 'dev; touch nope', '', 'tab')
        self.assertEqual(result, 0)
        argv = launch.call_args.args[0]
        self.assertEqual(argv[:6], [sh2pil_open.ssh_binary(), '-S',
                                     sh2pil_open.ssh_control_path(), '-o',
                                     'ControlMaster=no', '-t'])
        self.assertEqual(argv[6], 'build-host')
        self.assertIn("attach 'dev; touch nope'", argv[7])
        self.assertIn('mise/shims/zmx', argv[7])

    def test_remote_kill_uses_ssh_and_quotes_the_session_name(self):
        completed = sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, b'', b'')
        with (patch.object(sh2pil_open, 'CONFIG', self.config),
              patch.object(sh2pil_open.subprocess, 'run', return_value=completed) as run):
            result = sh2pil_open.zmx_kill('dev; touch nope', 'build-host')
        self.assertEqual(result, 0)
        command = run.call_args.args[0]
        self.assertEqual(command[:9], [sh2pil_open.ssh_binary(), '-S',
                                       sh2pil_open.ssh_control_path(), '-o',
                                       'ControlMaster=no', '-o', 'BatchMode=yes',
                                       '-o', 'ConnectTimeout=5'])
        self.assertEqual(command[9], 'build-host')
        self.assertIn("kill 'dev; touch nope'", command[10])
        run.assert_called_once_with(command, capture_output=True, timeout=15,
                                    env=sh2pil_open.ssh_env())

    def test_remote_kill_rejects_invalid_ssh_destinations(self):
        with patch.object(sh2pil_open.subprocess, 'run') as run:
            self.assertEqual(sh2pil_open.zmx_kill('dev', '-oProxyCommand=evil'), 1)
        run.assert_not_called()

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.config = pathlib.Path(self.tmp.name) / 'config.yaml'
        self.project = pathlib.Path(self.tmp.name) / 'home-infra'
        self.project.mkdir()

    def test_the_flag_comes_from_the_shared_config_and_defaults_to_off(self):
        with patch.object(sh2pil_open, 'CONFIG', self.config):
            self.assertFalse(sh2pil_open.config_flag('zmx'))
            self.config.write_text('# zmx: true\nzmx: false\nzmx: true\n')
            self.assertTrue(sh2pil_open.config_flag('zmx'))
            self.assertFalse(sh2pil_open.config_flag('other'))

    def test_a_similar_key_is_not_the_flag(self):
        self.config.write_text('zmx_extra: true\n')
        with patch.object(sh2pil_open, 'CONFIG', self.config):
            self.assertFalse(sh2pil_open.config_flag('zmx'))

    def test_tokens_keep_only_the_characters_zmx_accepts(self):
        self.assertEqual(sh2pil_open.zmx_token('home-infra'), 'home-infra')
        self.assertEqual(sh2pil_open.zmx_token('my project (2)'), 'my-project-2')
        self.assertEqual(sh2pil_open.zmx_token('/'), 'session')

    def test_a_taken_name_gets_a_suffix_so_two_chats_never_share_a_terminal(self):
        with patch.object(sh2pil_open, 'zmx_taken_names', return_value=set()):
            self.assertEqual(sh2pil_open.zmx_free_name('pi-abc'), 'pi-abc')
        with patch.object(sh2pil_open, 'zmx_taken_names', return_value={'pi-abc'}):
            self.assertEqual(sh2pil_open.zmx_free_name('pi-abc'), 'pi-abc-2')
        with patch.object(sh2pil_open, 'zmx_taken_names', return_value={'pi-abc', 'pi-abc-2'}):
            self.assertEqual(sh2pil_open.zmx_free_name('pi-abc'), 'pi-abc-3')

    def test_a_resume_names_and_labels_the_session_after_the_chat(self):
        identifier = '01a10240-6c8c-7306-b6e8-42980b81559a'
        with patch.object(sh2pil_open, 'zmx_free_name', side_effect=lambda base: base):
            argv = sh2pil_open.zmx_attach_argv(['pi', '--session', identifier], str(self.project),
                                            identifier, '/bin/zmx')
        self.assertEqual(argv[:6], ['env', '-u', 'ZMX_SESSION', 'PIB_ZMX=/bin/zmx',
                                    '/bin/zmx', 'attach'])
        self.assertEqual(argv[6], '--labels')
        self.assertEqual(argv[7], f'project=home-infra pi={identifier}')
        self.assertEqual(argv[8], 'pi-01a10240')
        self.assertEqual(argv[9:], ['pi', '--session', identifier])

    def test_a_new_chat_is_named_after_the_project_and_labelled_without_an_id(self):
        with patch.object(sh2pil_open, 'zmx_free_name', side_effect=lambda base: base):
            argv = sh2pil_open.zmx_attach_argv(['pi'], str(self.project), '', '/bin/zmx')
        self.assertEqual(argv[7], 'project=home-infra')
        self.assertEqual(argv[8], 'pi-home-infra')

    def test_only_chat_actions_are_wrapped(self):
        opened = []
        fields = {'cwd': str(self.project), 'place': 'tab', 'emit_cmd': True, 'label': '',
                  'session': 'aaa', 'fork': False, 'force': False, 'name': ''}
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'config_flag', return_value=True),
              patch.object(sh2pil_open, 'zmx_free_name', side_effect=lambda base: base),
              patch.object(sh2pil_open, 'launch',
                           side_effect=lambda argv, cwd, label, place, emit:
                           opened.append(argv) or 0)):
            sh2pil_open.open_terminal('pi', argparse.Namespace(**fields))
            sh2pil_open.open_terminal('shell', argparse.Namespace(**fields))
        self.assertEqual(opened[0][0], 'env')
        self.assertEqual(opened[0][-1], 'aaa')
        self.assertEqual(opened[1], [])

    def test_the_flag_off_keeps_the_plain_terminal(self):
        opened = []
        fields = {'cwd': str(self.project), 'place': 'tab', 'emit_cmd': True, 'label': '',
                  'session': 'aaa', 'fork': False, 'force': False, 'name': ''}
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'config_flag', return_value=False),
              patch.object(sh2pil_open, 'launch',
                           side_effect=lambda argv, cwd, label, place, emit:
                           opened.append(argv) or 0)):
            sh2pil_open.open_terminal('pi', argparse.Namespace(**fields))
        self.assertEqual(opened[0][0], 'pi')

    def test_the_label_map_reads_the_pi_column_of_zmx_list(self):
        listing = ('\u2192 name=chezmoi\tpid=1\tclients=1\tpi=aaa\tproject=chezmoi\n'
                   '  name=other\tpid=2\tclients=1\tcwd=x\n'
                   '  name=ended\tpid=3\tclients=0\tended=5\texit_code=0\n')
        with patch.object(sh2pil_open, 'zmx_run', return_value=listing):
            self.assertEqual(sh2pil_open.zmx_pi_sessions(), {'aaa': 'chezmoi'})

    def test_the_window_comes_from_the_label_and_the_tracked_window_id(self):
        def fake_run(arguments):
            return '60\n' if arguments[0] == 'print-env' else ''
        windows = [{'target': '60', 'label': 'kitty window 60', 'pids': [9]},
                   {'target': '61', 'label': 'kitty window 61', 'pids': [10]}]
        with (patch.object(sh2pil_open, 'zmx_pi_sessions', return_value={'aaa': 'chezmoi'}),
              patch.object(sh2pil_open, 'zmx_run', side_effect=fake_run),
              patch.object(sh2pil_open, 'kitten_prefix', return_value=['kitten', '@']),
              patch.object(sh2pil_open, 'kitty_windows', return_value=windows)):
            window = sh2pil_open.zmx_window('aaa')
            self.assertEqual(window['target'], '60')
            self.assertEqual(window['label'], 'kitty window 60 (zmx chezmoi)')
            self.assertIsNone(sh2pil_open.zmx_window('bbb'))
            self.assertIsNone(sh2pil_open.zmx_window('aaa', '61'))

    def test_the_window_must_still_hold_a_client_for_that_session(self):
        def fake_run(arguments):
            return '60\n' if arguments[0] == 'print-env' else ''
        attached = [{'target': '60', 'label': 'kitty window 60', 'pids': [9],
                     'commands': [['/bin/zsh'], ['/bin/zmx', 'attach', 'pi-abc']]}]
        elsewhere = [{'target': '60', 'label': 'kitty window 60', 'pids': [9],
                      'commands': [['/bin/zsh'], ['/bin/zmx', 'attach', 'pi-abc-2']]}]
        with (patch.object(sh2pil_open, 'zmx_pi_sessions', return_value={'aaa': 'pi-abc'}),
              patch.object(sh2pil_open, 'zmx_run', side_effect=fake_run),
              patch.object(sh2pil_open, 'kitten_prefix', return_value=['kitten', '@'])):
            with patch.object(sh2pil_open, 'kitty_windows', return_value=attached):
                self.assertEqual(sh2pil_open.zmx_window('aaa')['target'], '60')
            with patch.object(sh2pil_open, 'kitty_windows', return_value=elsewhere):
                self.assertIsNone(sh2pil_open.zmx_window('aaa'))

    def test_live_ownership_falls_back_to_the_zmx_label(self):
        registry = pathlib.Path(self.tmp.name) / 'live'
        registry.mkdir()
        root = pathlib.Path(self.tmp.name) / 'sessions'
        project = root / '--project--'
        project.mkdir(parents=True)
        transcript = project / '2026-01-01_aaa.jsonl'
        transcript.write_text(json.dumps({'type': 'session', 'cwd': '/project'}) + '\n')
        (registry / '123.json').write_text(json.dumps(
            {'pid': 123, 'file': str(transcript), 'started': 'start'}))
        window = {'target': '60', 'label': 'kitty window 60 (zmx chezmoi)', 'pids': [9]}
        with (patch.object(sh2pil_open, 'LIVE_DIR', registry),
              patch.object(sh2pil_open, 'pi_processes', return_value=[(123, '/project', 'pi')]),
              patch.object(sh2pil_open, 'process_start', return_value='start'),
              patch.object(sh2pil_open, 'kitten_prefix', return_value=['kitten', '@']),
              patch.object(sh2pil_open, 'tmux_panes', return_value=[]),
              patch.object(sh2pil_open, 'kitty_windows', return_value=[window]),
              patch.object(sh2pil_open, 'zmx_window', return_value=window)):
            entries = sh2pil_open.live_sessions(root)
        self.assertEqual(entries[0]['owner'], 'kitty window 60 (zmx chezmoi)')
        self.assertEqual(entries[0]['owner_choices'], 1)
    def test_a_chat_does_not_pin_a_title_but_a_shell_still_does(self):
        fields = {'cwd': str(self.project), 'place': 'tab', 'emit_cmd': True, 'label': '',
                  'session': 'aaa', 'fork': False, 'force': False, 'name': ''}
        opened = []
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'config_flag', return_value=True),
              patch.object(sh2pil_open, 'zmx_free_name', side_effect=lambda base: base),
              patch.object(sh2pil_open, 'launch',
                           side_effect=lambda argv, cwd, label, place, emit:
                           opened.append(label) or 0)):
            sh2pil_open.open_terminal('pi', argparse.Namespace(**fields))
            sh2pil_open.open_terminal('shell', argparse.Namespace(**fields))
        self.assertEqual(opened[0], '')  # pi names its own window
        self.assertEqual(opened[1], 'shell home-infra')

    def test_a_zmx_chat_runs_in_the_requested_terminal(self):
        fields = {'cwd': str(self.project), 'place': 'current', 'emit_cmd': True, 'label': '',
                  'session': 'aaa', 'fork': False, 'force': False, 'name': ''}
        places = []
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'config_flag', return_value=True),
              patch.object(sh2pil_open, 'zmx_free_name', side_effect=lambda base: base),
              patch.object(sh2pil_open, 'launch',
                           side_effect=lambda argv, cwd, label, place, emit:
                           places.append(place) or 0)):
            sh2pil_open.open_terminal('pi', argparse.Namespace(**fields))
        self.assertEqual(places, ['current'])

    def test_attach_opens_a_client_with_no_command_and_no_title(self):
        opened = []
        fields = {'cwd': str(self.project), 'place': 'tab', 'emit_cmd': True, 'label': '',
                  'session': 'aaa'}
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'zmx_session_name', return_value='pi-abc'),
              patch.object(sh2pil_open, 'launch',
                           side_effect=lambda argv, cwd, label, place, emit:
                           opened.append((argv, label)) or 0)):
            sh2pil_open.open_terminal('attach', argparse.Namespace(**fields))
        self.assertEqual(opened[0][0], ['env', '-u', 'ZMX_SESSION', 'PIB_ZMX=/bin/zmx',
                                        '/bin/zmx', 'attach', 'pi-abc'])
        self.assertEqual(opened[0][1], '')

    def test_zmx_client_honors_every_requested_placement(self):
        for place in ('current', 'tab', 'window', 'pane'):
            self.assertEqual(sh2pil_open.zmx_client_place(place), place)

    def test_attaching_to_a_chat_uses_the_requested_terminal(self):
        # An attach starts a client just as a resume does, so both paths honor placement.
        places = []
        fields = {'cwd': str(self.project), 'place': 'current', 'emit_cmd': True, 'label': '',
                  'session': 'aaa'}
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'zmx_session_name', return_value='pi-abc'),
              patch.object(sh2pil_open, 'launch',
                           side_effect=lambda argv, cwd, label, place, emit:
                           places.append(place) or 0),
              contextlib.redirect_stderr(io.StringIO())):
            self.assertEqual(sh2pil_open.open_terminal('attach', argparse.Namespace(**fields)), 0)
        self.assertEqual(places, ['current'])

    def test_attach_reports_a_session_it_cannot_find(self):
        fields = {'cwd': str(self.project), 'place': 'tab', 'emit_cmd': True, 'label': '',
                  'session': 'aaa'}
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'zmx_session_name', return_value=''),
              patch.object(sh2pil_open.sys.stdin, 'isatty', return_value=False)):
            self.assertEqual(sh2pil_open.open_terminal('attach', argparse.Namespace(**fields)), 1)

    def test_switch_attaches_when_the_chat_runs_with_no_terminal(self):
        entry = {'id': 'aaa', 'pids': [123]}
        called = []
        with (patch.object(sh2pil_open, 'live_entry', return_value=entry),
              patch.object(sh2pil_open, 'owner_window', return_value=(None, '', 0)),
              patch.object(sh2pil_open, 'zmx_session_name', return_value='pi-abc'),
              patch.object(sh2pil_open, 'open_terminal',
                           side_effect=lambda action, args: called.append(action) or 0),
              patch.object(sh2pil_open, 'parse_args',
                           return_value=argparse.Namespace(action='switch', session='aaa'))):
            self.assertEqual(sh2pil_open.main([]), 0)
        self.assertEqual(called, ['attach'])

    def test_owner_window_falls_back_to_zmx_when_no_process_match_exists(self):
        entry = {'pids': [123], 'id': 'aaa'}
        window = {'target': '60', 'label': 'kitty window 60 (zmx chezmoi)', 'pids': [9]}
        with (patch.object(sh2pil_open, 'session_windows', return_value=[]),
              patch.object(sh2pil_open, 'zmx_window', return_value=window)):
            self.assertEqual(sh2pil_open.owner_window(entry),
                             (window, 'the zmx session carries this pi session id', 1))


class RemoteSessionsTest(unittest.TestCase):
    """A host's own session rows, and the verbs that read and open one of them."""

    def test_list_asks_the_hosts_own_pib_and_returns_its_rows(self):
        rows = [{'id': 'aaa', 'harness': 'pi', 'name': 'Fix docs', 'project': 'api',
                 'cwd': '/srv/api', 'alive': True, 'live': True, 'bytes': 10,
                 'modified': 1.0, 'file': '/srv/api/sessions/aaa.jsonl'}]
        completed = sh2pil_open.subprocess.CompletedProcess(
            ['ssh'], 0, json.dumps(rows).encode(), b'')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=completed) as run:
            found = sh2pil_open.session_remote_list('build-host', 'pi', live=True)
        self.assertEqual(found, rows)
        command = run.call_args.args[0]
        self.assertEqual(command[:9], [sh2pil_open.ssh_binary(), '-S',
                                       sh2pil_open.ssh_control_path(), '-o',
                                       'ControlMaster=no', '-o', 'BatchMode=yes',
                                       '-o', 'ConnectTimeout=5'])
        self.assertEqual(command[9], 'build-host')
        # The remote command finds sh2pil-sessions without a login shell, whose greeting would land in the
        # JSON, and asks the host about its own running pi processes.
        self.assertIn('command -v sh2pil-sessions', command[10])
        self.assertIn('$HOME/.local/bin/sh2pil-sessions', command[10])
        self.assertIn('list --json --harness pi --live', command[10])
        # The helper's own code travels with the read, for a host that has no copy: the
        # host's install is preferred, and `python3 -` runs the bytes that arrive instead.
        self.assertIn('exec python3 - list --json --harness pi --live', command[10])
        self.assertEqual(run.call_args.kwargs['input'].decode(),
                         sh2pil_open.helper_source(sh2pil_open.sessions_helper()))
        run.assert_called_once_with(
            command, capture_output=True, timeout=30, env=sh2pil_open.ssh_env(),
            input=sh2pil_open.helper_source(sh2pil_open.sessions_helper()).encode())

    def test_list_asks_no_live_question_of_the_opencode_store(self):
        completed = sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, b'[]', b'')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=completed) as run:
            self.assertEqual(sh2pil_open.session_remote_list('build-host', 'opencode', True), [])
        remote = run.call_args.args[0][10]
        self.assertIn('list --json --harness opencode', remote)
        self.assertNotIn('--live', remote)

    def test_list_falls_back_to_the_code_this_picker_ships_when_the_hosts_copy_is_old(self):
        # A host whose own sh2pil-sessions is older than this picker rejects a read of several stores at
        # once.  The same read is then made with the code this side sends, which knows every
        # store this build knows, so one machine being an update behind shows no error here.
        old = sh2pil_open.subprocess.CompletedProcess(
            ['ssh'], 2, b'', b"sh2pil-sessions list: error: argument --harness: invalid choice: "
                             b"'pi,opencode,claude,codex' (choose from 'pi', 'opencode')\n")
        fresh = sh2pil_open.subprocess.CompletedProcess(
            ['ssh'], 0, b'[{"id": "aaa", "harness": "claude"}]', b'')
        with patch.object(sh2pil_open.subprocess, 'run', side_effect=[old, fresh]) as run:
            rows = sh2pil_open.session_remote_list('build-host', 'pi,opencode,claude,codex')
        self.assertEqual(rows, [{'id': 'aaa', 'harness': 'claude'}])
        self.assertEqual(run.call_count, 2)
        retried = run.call_args_list[1].args[0][10]
        self.assertEqual(retried, 'exec python3 - list --json --harness '
                                  'pi,opencode,claude,codex')
        self.assertEqual(run.call_args_list[1].kwargs['input'].decode(),
                         sh2pil_open.helper_source(sh2pil_open.sessions_helper()))

    def test_the_store_question_falls_back_the_same_way(self):
        # An older sh2pil-sessions has no `harnesses` command at all, and then the header would have to
        # guess which stores the host has.  The question is asked of the shipped code instead.
        old = sh2pil_open.subprocess.CompletedProcess(
            ['ssh'], 2, b'', b"sh2pil-sessions: error: argument command: invalid choice: 'harnesses'\n")
        stores = [{'harness': 'pi', 'present': True, 'reason': ''}]
        fresh = sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, json.dumps(stores).encode(), b'')
        with patch.object(sh2pil_open.subprocess, 'run', side_effect=[old, fresh]) as run:
            self.assertEqual(sh2pil_open.harnesses_remote('build-host'), stores)
        self.assertEqual(run.call_count, 2)
        self.assertEqual(run.call_args_list[1].args[0][10], 'exec python3 - harnesses --json')

    def test_a_read_that_cannot_connect_at_all_is_not_tried_twice(self):
        # ssh exits 255 when it cannot run the command, so a host that is down costs one
        # ConnectTimeout rather than two.
        dead = sh2pil_open.subprocess.CompletedProcess(
            ['ssh'], 255, b'', b'ssh: connect to host build-host port 22: Connection timed out\n')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=dead) as run:
            with self.assertRaises(RuntimeError):
                sh2pil_open.session_remote_list('build-host', 'pi')
        self.assertEqual(run.call_count, 1)

    def test_list_drops_rows_that_are_not_sessions_and_reports_what_the_host_said(self):
        completed = sh2pil_open.subprocess.CompletedProcess(
            ['ssh'], 0, json.dumps([{'harness': 'pi'}, {'id': 'aaa'}]).encode(), b'')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=completed):
            self.assertEqual(sh2pil_open.session_remote_list('build-host', 'pi'), [{'id': 'aaa'}])
        missing = sh2pil_open.subprocess.CompletedProcess(
            ['ssh'], 127, b'', b'sh2pil-sessions is not installed on this host (PATH and ~/.local/bin)\n')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=missing):
            with self.assertRaises(RuntimeError) as raised:
                sh2pil_open.session_remote_list('build-host', 'pi')
        self.assertIn('sh2pil-sessions is not installed', str(raised.exception))
        with patch.object(sh2pil_open.subprocess, 'run') as run:
            with self.assertRaises(ValueError):
                sh2pil_open.session_remote_list('-oProxyCommand=evil', 'pi')
        run.assert_not_called()

    def test_show_reads_the_tail_through_the_hosts_own_reader(self):
        completed = sh2pil_open.subprocess.CompletedProcess(
            ['ssh'], 0, b'## user\n\nhello\n', b'')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=completed) as run:
            text = sh2pil_open.session_remote_show('build-host', 'aaa', 'pi', 400)
        self.assertEqual(text, '## user\n\nhello\n')
        self.assertIn('show aaa --harness pi --tail 400', run.call_args.args[0][10])
        # The reader that travels with the read is this side's own sh2pil-sessions, byte for byte.
        self.assertEqual(run.call_args.kwargs['input'].decode(),
                         sh2pil_open.helper_source(sh2pil_open.sessions_helper()))
        failed = sh2pil_open.subprocess.CompletedProcess(['ssh'], 1, b'', b'no such session\n')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=failed):
            with self.assertRaises(RuntimeError) as raised:
                sh2pil_open.session_remote_show('build-host', 'gone', 'pi', 0)
        self.assertIn('no such session', str(raised.exception))

    def test_show_reads_a_pi_transcript_straight_from_the_path_the_host_reported(self):
        completed = sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, b'## user\n\nhi\n', b'')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=completed) as run:
            text = sh2pil_open.session_remote_show('build-host', 'aaa', 'pi', 400,
                                                '/srv/api/sessions/aaa.jsonl')
        self.assertEqual(text, '## user\n\nhi\n')
        remote = run.call_args.args[0][10]
        # One transcript, not a store scan, and the reader itself travels with the read.
        self.assertIn('sh2pil-last --session /srv/api/sessions/aaa.jsonl --full --tail 400', remote)
        self.assertIn('exec python3 - --session', remote)
        self.assertIn('Open the last message of a pi session',
                      run.call_args.kwargs['input'].decode())

    def test_each_helper_is_found_beside_this_one_and_then_in_the_usual_places(self):
        # The code a host without the helper is run with is the code sitting beside sh2pil-open,
        # so a checkout works as well as an install.
        self.assertTrue(sh2pil_open.sessions_helper().name == 'sh2pil-sessions')
        self.assertTrue(sh2pil_open.sh2pil_last().name == 'sh2pil-last')
        self.assertTrue(sh2pil_open.sessions_helper().is_file())
        self.assertTrue(sh2pil_open.sh2pil_last().is_file())

    def test_a_host_without_the_helper_is_sent_the_code_over_the_connection(self):
        command = sh2pil_open.remote_helper_command('sh2pil-sessions', 'list', '--json')
        self.assertIn('command -v sh2pil-sessions', command)
        self.assertIn('"$HOME/.local/bin/sh2pil-sessions"', command)
        self.assertTrue(command.endswith('else exec python3 - list --json; fi'))
        # Nothing is written on that host, and the destination is checked like every other.
        self.assertNotIn('cat >', command)
        with patch.object(sh2pil_open.subprocess, 'run') as run:
            with self.assertRaises(ValueError):
                sh2pil_open.session_remote_list('-oProxyCommand=evil', 'pi')
        run.assert_not_called()

    def test_a_host_that_still_carries_the_old_helper_name_is_used_as_it_is(self):
        # A host is migrated separately from this repository, so the name an earlier release
        # installed is tried after the current one.  The host then answers with its own copy
        # instead of being sent the whole helper on every read.
        command = sh2pil_open.remote_helper_command('sh2pil-sessions', 'list', '--json')
        self.assertIn('command -v pib >/dev/null 2>&1; then exec pib list --json', command)
        self.assertIn('[ -x "$HOME/.local/bin/pib" ]; then exec "$HOME/.local/bin/pib" list --json',
                      command)
        self.assertLess(command.index('command -v sh2pil-sessions'),
                        command.index('command -v pib'))
        # Every helper this release ships has an older name to fall back to.
        for name, legacy in sh2pil_open.LEGACY_HELPER_NAMES.items():
            self.assertIn(f'command -v {legacy[0]}',
                          sh2pil_open.remote_helper_command(name, 'go'))
        # A name with no older spelling is only looked for as itself.
        unknown = sh2pil_open.remote_helper_command('some-other-helper', 'go')
        self.assertIn('command -v some-other-helper', unknown)
        self.assertNotIn('command -v pib', unknown)


    def test_open_builds_an_ssh_terminal_for_the_remote_chat(self):
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch:
            result = sh2pil_open.session_remote_open('build-host', '/srv/home infra', 'aaa',
                                                  'pi', False, '', '', 'pane')
        self.assertEqual(result, 0)
        argv, cwd, label, place, emit = launch.call_args.args
        self.assertEqual(argv[:7], [sh2pil_open.ssh_binary(), '-S',
                                    sh2pil_open.ssh_control_path(), '-o',
                                    'ControlMaster=no', '-t', 'build-host'])
        self.assertIn("cd -- '/srv/home infra'", argv[7])
        self.assertIn('pi --session aaa', argv[7])
        # No label: the remote pi names its own window over the SSH pty, as it does locally.
        self.assertEqual((cwd, label, place, emit),
                         (str(pathlib.Path.home()), '', 'pane', False))

    def test_open_carries_a_fork_name_and_starts_a_new_chat_without_one(self):
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch:
            sh2pil_open.session_remote_open('build-host', '/srv/api', 'aaa', 'pi', True,
                                         'Fix docs (fork)')
        remote = launch.call_args.args[0][7]
        self.assertIn('pi --fork aaa', remote)
        self.assertIn('--name', remote)
        # The name travels as its own quoted argument, so a fork keeps the name it was given.
        self.assertIn('Fix docs (fork)', remote)
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch:
            sh2pil_open.session_remote_open('build-host', '/srv/api', '', 'opencode')
        argv, _, label, _, _ = launch.call_args.args
        self.assertIn('/bin/zsh -lic opencode', argv[7])
        self.assertNotIn('--session', argv[7])
        self.assertEqual(label, 'opencode api')

    def test_open_starts_in_the_hosts_home_when_the_project_is_gone(self):
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch:
            sh2pil_open.session_remote_open('build-host', '', 'aaa')
        remote = launch.call_args.args[0][7]
        self.assertNotIn('cd --', remote)
        self.assertIn('pi --session aaa', remote)

    def test_open_refuses_a_destination_or_a_directory_it_must_not_run(self):
        with patch.object(sh2pil_open, 'launch') as launch:
            self.assertEqual(
                sh2pil_open.session_remote_open('-oProxyCommand=evil', '/srv/app', 'aaa'), 1)
            self.assertEqual(
                sh2pil_open.session_remote_open('build-host', 'relative/app', 'aaa'), 1)
        launch.assert_not_called()

    def test_copy_prints_and_copies_the_command_that_opens_the_session(self):
        output = io.StringIO()
        copied = []
        with (patch.object(sh2pil_open.shutil, 'which', return_value='/usr/bin/pbcopy'),
              patch.object(sh2pil_open.subprocess, 'run',
                           side_effect=lambda argv, **rest: copied.append(rest.get('input')) or
                           argparse.Namespace(returncode=0)),
              contextlib.redirect_stdout(output)):
            self.assertEqual(
                sh2pil_open.session_remote_copy('build-host', 'aaa', 'pi', '/srv/api'), 0)
        printed = output.getvalue()
        self.assertIn('build-host', printed)
        self.assertIn('pi --session aaa', printed)
        self.assertEqual(copied, [printed.rstrip('\n').encode()])


class RemoteStateTest(unittest.TestCase):
    """What a host's own chats are doing, read where the records are written.

    A state record is written by the extension inside the chat's own process, so it lives on
    the machine that owns the chat and nowhere else.  The read is the same shape as the local
    `state`, over the same shared master as every other remote read.
    """

    def answer(self, entries):
        return sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, json.dumps(entries).encode(), b'')

    def test_state_asks_the_hosts_own_helper_and_returns_its_records(self):
        entries = [{'id': 'aaa', 'state': 'blocked', 'detail': 'confirm', 'label': 'Gate',
                    'age': 12, 'stale': False},
                   {'id': 'bbb', 'state': 'idle', 'detail': '', 'label': '', 'age': 3,
                    'stale': False}]
        with (patch.object(sh2pil_open, 'ssh_master_present', return_value=True),
              patch.object(sh2pil_open.subprocess, 'run', return_value=self.answer(entries)) as run):
            self.assertEqual(sh2pil_open.state_remote('build-host'), entries)
        command = run.call_args.args[0]
        self.assertEqual(command[:9], [sh2pil_open.ssh_binary(), '-S',
                                       sh2pil_open.ssh_control_path(), '-o',
                                       'ControlMaster=no', '-o', 'BatchMode=yes',
                                       '-o', 'ConnectTimeout=5'])
        self.assertEqual(command[9], 'build-host')
        remote = command[10]
        # The host's own copy answers, because its records are its own.
        self.assertIn('command -v sh2pil-open', remote)
        self.assertIn('$HOME/.local/bin/sh2pil-open', remote)
        self.assertIn('state --json', remote)
        # And its own code travels for a host that has none, so both ends read one shape.
        self.assertIn('exec python3 - state --json', remote)
        self.assertEqual(run.call_args.kwargs['input'].decode(),
                         sh2pil_open.helper_source(sh2pil_open.own_helper()))
        # The read cannot outlive the clock that asks for it.
        self.assertLess(run.call_args.kwargs['timeout'], 10)

    def test_state_falls_back_to_the_shipped_code_when_the_hosts_helper_is_old(self):
        # A host one update behind has no `state` verb at all -- phase 1 added it -- so the
        # question is asked of the code this side sends instead of going unanswered.
        old = sh2pil_open.subprocess.CompletedProcess(
            ['ssh'], 2, b'', b"sh2pil-open: error: argument action: invalid choice: 'state'\n")
        entries = [{'id': 'aaa', 'state': 'working', 'detail': ''}]
        with (patch.object(sh2pil_open, 'ssh_master_present', return_value=True),
              patch.object(sh2pil_open.subprocess, 'run',
                           side_effect=[old, self.answer(entries)]) as run):
            self.assertEqual(sh2pil_open.state_remote('build-host'), entries)
        self.assertEqual(run.call_count, 2)
        self.assertEqual(run.call_args_list[1].args[0][10], 'exec python3 - state --json')
        self.assertEqual(run.call_args_list[1].kwargs['input'].decode(),
                         sh2pil_open.helper_source(sh2pil_open.own_helper()))

    def test_a_host_that_cannot_be_reached_is_not_retried(self):
        # ssh exits 255 when it cannot run the command at all, and a failed read costs the
        # states and nothing else: no retry, no second ConnectTimeout on a clock.
        down = sh2pil_open.subprocess.CompletedProcess(
            ['ssh'], 255, b'', b'ssh: connect to host build-host port 22: Connection refused\n')
        with (patch.object(sh2pil_open, 'ssh_master_present', return_value=True),
              patch.object(sh2pil_open.subprocess, 'run', return_value=down) as run):
            with self.assertRaises(RuntimeError) as caught:
                sh2pil_open.state_remote('build-host')
        self.assertIn('Connection refused', str(caught.exception))
        self.assertEqual(run.call_count, 1)

    def test_a_host_without_a_master_is_refused_rather_than_contacted(self):
        # The picker reads a host's states on a clock.  If the master has gone, an ordinary
        # read would authenticate again -- which means a YubiKey touch nobody asked for -- so
        # the read refuses instead, and the rows keep the state they already show.
        with (patch.object(sh2pil_open, 'ssh_master_present', return_value=False),
              patch.object(sh2pil_open.subprocess, 'run') as run):
            with self.assertRaises(RuntimeError) as caught:
                sh2pil_open.state_remote('build-host')
        self.assertIn('no SSH master', str(caught.exception))
        run.assert_not_called()

    def test_the_master_question_never_contacts_the_host(self):
        # `ssh -O check` is a control-socket question: it must carry no host command, because a
        # command would mean connecting, and connecting on a clock means a touch.
        with patch.object(sh2pil_open.subprocess, 'run',
                          return_value=sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, b'', b'')) as run:
            self.assertTrue(sh2pil_open.ssh_master_present('build-host'))
        self.assertEqual(run.call_args.args[0][:6], [sh2pil_open.ssh_binary(), '-S',
                                                     sh2pil_open.ssh_control_path(), '-O', 'check',
                                                     'build-host'])

    def test_a_record_with_no_id_decorates_no_row(self):
        # A chat is named by its id.  A record without one cannot be attached to a row, and a
        # row whose id is empty is a project header rather than a chat.
        entries = [{'id': 'aaa', 'state': 'idle'}, {'state': 'blocked'}, 'junk', {'id': ''}]
        with (patch.object(sh2pil_open, 'ssh_master_present', return_value=True),
              patch.object(sh2pil_open.subprocess, 'run', return_value=self.answer(entries))):
            self.assertEqual(sh2pil_open.state_remote('build-host'),
                             [{'id': 'aaa', 'state': 'idle'}])

    def test_an_answer_that_is_not_a_list_of_records_is_an_error(self):
        for body in (b'{"id": "aaa"}', b'sh2pil-open: something went wrong\n', b'not json\n'):
            result = sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, body, b'')
            with (patch.object(sh2pil_open, 'ssh_master_present', return_value=True),
                  patch.object(sh2pil_open.subprocess, 'run', return_value=result)):
                with self.assertRaises(RuntimeError):
                    sh2pil_open.state_remote('build-host')

    def test_the_verb_prints_the_records_as_rows_or_as_json(self):
        entries = [{'id': 'aaa', 'state': 'blocked', 'last_state': '', 'detail': 'confirm',
                    'label': 'Gate', 'age': 12, 'stale': False}]
        for as_json, wanted in ((True, '"state": "blocked"'), (False, 'blocked')):
            output = io.StringIO()
            with (patch.object(sh2pil_open, 'ssh_master_present', return_value=True),
                  patch.object(sh2pil_open.subprocess, 'run', return_value=self.answer(entries)),
                  patch.object(sh2pil_open, 'parse_args', return_value=argparse.Namespace(
                      action='state-remote', server='build-host', json=as_json)),
                  contextlib.redirect_stdout(output)):
                self.assertEqual(sh2pil_open.main([]), 0)
            self.assertIn(wanted, output.getvalue())


class RemoteToolTest(unittest.TestCase):
    """The directory tools a host runs for itself: yazi and lazygit.

    A directory tool acts on the files it shows, and the files of a remote row are on that
    host, so the tool runs there.  The host is asked first whether it can start the tool at
    all, because the terminal that would carry the failure closes at once.
    """

    def problem(self, told: str, returncode: int = 0):
        """Run the check against an answer a host could have given."""
        result = argparse.Namespace(returncode=returncode, stdout=told.encode())
        return patch.object(sh2pil_open, 'remote_run', return_value=result)

    def test_the_tool_runs_in_the_hosts_directory_through_its_own_login_shell(self):
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch, \
             self.problem(''):
            result = sh2pil_open.tool_remote('build-host', '/srv/home infra', 'yazi')
        self.assertEqual(result, 0)
        argv, cwd, label, place, emit = launch.call_args.args
        self.assertEqual(argv[:7], [sh2pil_open.ssh_binary(), '-S',
                                    sh2pil_open.ssh_control_path(), '-o',
                                    'ControlMaster=no', '-t', 'build-host'])
        self.assertIn("cd -- '/srv/home infra'", argv[7])
        self.assertIn('-lic yazi', argv[7])
        # lazygit climbs to the repository itself, so it gets the project directory and no
        # repository top: a path this machine cannot see is not one it can ask git about.
        self.assertNotIn('rev-parse', argv[7])
        self.assertEqual((cwd, label, place, emit),
                         (str(pathlib.Path.home()), 'yazi home infra', 'tab', False))

    def test_an_unreachable_host_is_not_refused_by_the_check(self):
        # No master, a refused connection, or a timeout: the check cannot answer, so the
        # launch goes ahead and its own terminal carries the reason.
        with patch.object(sh2pil_open, 'remote_run',
                          side_effect=subprocess.TimeoutExpired('ssh', 15)), \
             patch.object(sh2pil_open, 'launch', return_value=0) as launch:
            self.assertEqual(sh2pil_open.tool_remote('build-host', '/srv/api', 'yazi'), 0)
        launch.assert_called_once()
        refused = argparse.Namespace(returncode=255, stdout=b'', stderr=b'Permission denied')
        with patch.object(sh2pil_open, 'remote_run', return_value=refused), \
             patch.object(sh2pil_open, 'launch', return_value=0) as launch:
            self.assertEqual(sh2pil_open.tool_remote('build-host', '/srv/api', 'yazi'), 0)
        launch.assert_called_once()

    def test_a_missing_directory_or_tool_names_the_host_and_launches_nothing(self):
        cases = [('sh2pil-no-dir\n', '/srv/api does not exist on build-host'),
                 ('sh2pil-no-tool\n', 'yazi is not installed on build-host')]
        for told, reason in cases:
            with self.subTest(told=told), \
                 patch.object(sh2pil_open, 'launch') as launch, \
                 self.problem(told):
                self.assertEqual(
                    sh2pil_open.remote_tool_problem('build-host', '/srv/api', 'yazi'), reason)
                self.assertEqual(sh2pil_open.tool_remote('build-host', '/srv/api', 'yazi'), 1)
            launch.assert_not_called()

    def test_the_check_does_not_ask_about_a_directory_the_caller_left_empty(self):
        # An empty directory means "start in the host's home", which is a choice and not a
        # mistake: the check still asks about the tool, and never about the directory.
        asked = []
        result = argparse.Namespace(returncode=0, stdout=b'')
        with patch.object(sh2pil_open, 'remote_run',
                          side_effect=lambda *rest, **kw: asked.append(rest[1]) or result):
            self.assertEqual(sh2pil_open.remote_tool_problem('build-host', '', 'lazygit'), '')
        self.assertEqual(len(asked), 1)
        self.assertNotIn('sh2pil-no-dir', asked[0])
        self.assertIn('command -v lazygit', asked[0])

    def test_the_check_runs_the_tool_lookup_in_the_hosts_own_login_shell(self):
        asked = []
        result = argparse.Namespace(returncode=0, stdout=b'')
        with patch.object(sh2pil_open, 'remote_run',
                          side_effect=lambda *rest, **kw: asked.append(rest[1]) or result):
            sh2pil_open.remote_tool_problem('build-host', '/srv/api', 'yazi')
        # The marker is printed inside the login shell, so it arrives whichever shell the
        # host has, including the `exec` that replaces the shell the connection started.
        self.assertIn('/bin/zsh -lic', asked[0])
        self.assertIn('|| echo sh2pil-no-tool', asked[0])
        self.assertIn("test -d /srv/api", asked[0])

    def test_a_kind_uses_the_hosts_default_when_no_tool_is_named(self):
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch, \
             self.problem(''):
            self.assertEqual(
                sh2pil_open.tool_remote('build-host', '/srv/api', kind='file_browser'), 0)
        self.assertIn('-lic yazi', launch.call_args.args[0][7])
        self.assertEqual(launch.call_args.args[2], 'yazi api')
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch, \
             self.problem(''):
            self.assertEqual(
                sh2pil_open.tool_remote('build-host', '/srv/api', kind='git_tool'), 0)
        self.assertIn('-lic lazygit', launch.call_args.args[0][7])

    def test_a_named_tool_wins_over_the_kind(self):
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch, \
             self.problem(''):
            self.assertEqual(
                sh2pil_open.tool_remote('build-host', '/srv/api', 'lf', kind='file_browser'), 0)
        self.assertIn('-lic lf', launch.call_args.args[0][7])

    def test_neither_a_tool_nor_a_kind_is_refused(self):
        errors = io.StringIO()
        with patch.object(sh2pil_open, 'launch') as launch, \
             contextlib.redirect_stderr(errors):
            self.assertEqual(sh2pil_open.tool_remote('build-host', '/srv/api'), 1)
        self.assertIn('needs a tool name or --kind', errors.getvalue())
        launch.assert_not_called()
        with patch.object(sh2pil_open, 'launch') as launch, \
             contextlib.redirect_stderr(errors):
            self.assertEqual(
                sh2pil_open.tool_remote('build-host', '/srv/api', kind='browser'), 1)
        self.assertIn('is not a directory-tool kind', errors.getvalue())
        launch.assert_not_called()

    def test_an_emitted_command_offers_a_destination_or_a_tool_name_it_must_not_run(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            self.assertEqual(
                sh2pil_open.tool_remote('build-host', '/srv/api', 'yazi', '', 'tab', True), 0)
        self.assertIn('ssh', output.getvalue())
        self.assertIn('build-host', output.getvalue())
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch, \
             patch.object(sh2pil_open, 'remote_run',
                          return_value=argparse.Namespace(returncode=0, stdout=b'')) as run:
            # Any tool name is allowed now, because the tool is a setting; only a name that is
            # not a name is refused.
            self.assertEqual(
                sh2pil_open.tool_remote('build-host', '/srv/api', 'jj', '', 'tab', True), 0)
            launch.reset_mock()
            run.reset_mock()
            self.assertEqual(sh2pil_open.tool_remote('options;rm', '/srv/api', 'yazi'), 1)
            self.assertEqual(sh2pil_open.tool_remote('build-host', 'relative/api', 'yazi'), 1)
            self.assertEqual(sh2pil_open.tool_remote('build-host', '/srv/api', 'nvim; rm -rf /'), 1)
        launch.assert_not_called()
        run.assert_not_called()


class RemoteShellTest(unittest.TestCase):
    """A login shell on a host: the host's own shell, in the host's own directory.

    Nothing is run in it, so there is no tool to look up: only the directory is asked about,
    and a host that cannot be reached is not refused by that question.
    """

    def test_the_shell_is_the_hosts_own_login_shell_in_the_hosts_directory(self):
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch, \
             patch.object(sh2pil_open, 'remote_run',
                          return_value=argparse.Namespace(returncode=0, stdout=b'')):
            result = sh2pil_open.shell_remote('build-host', '/srv/home infra')
        self.assertEqual(result, 0)
        argv, cwd, label, place, emit = launch.call_args.args
        self.assertEqual(argv[:7], [sh2pil_open.ssh_binary(), '-S',
                                    sh2pil_open.ssh_control_path(), '-o',
                                    'ControlMaster=no', '-t', 'build-host'])
        remote = argv[7]
        self.assertIn("cd -- '/srv/home infra'", remote)
        # A login shell, not a command run through one: no -lic, and each candidate is tried
        # so a host with bash and no zsh is opened rather than refused.
        self.assertNotIn('-lic', remote)
        self.assertIn('exec /bin/zsh -l', remote)
        self.assertIn('exec /bin/bash -l', remote)
        self.assertEqual((cwd, label, place, emit),
                         (str(pathlib.Path.home()), 'shell home infra', 'tab', False))

    def test_only_the_directory_is_asked_about_and_the_label_follows_it(self):
        asked = []
        result = argparse.Namespace(returncode=0, stdout=b'')
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch, \
             patch.object(sh2pil_open, 'remote_run',
                          side_effect=lambda *rest, **kw: asked.append(rest[1]) or result):
            sh2pil_open.shell_remote('build-host', '/srv/api')
        self.assertEqual(len(asked), 1)
        self.assertIn('test -d /srv/api', asked[0])
        self.assertNotIn('command -v', asked[0])
        self.assertEqual(launch.call_args.args[2], 'shell api')

    def test_an_empty_directory_is_the_hosts_home_and_is_not_checked(self):
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch, \
             patch.object(sh2pil_open, 'remote_run') as run:
            sh2pil_open.shell_remote('build-host', '')
        run.assert_not_called()
        argv, _, label, _, _ = launch.call_args.args
        self.assertNotIn('cd --', argv[7])
        self.assertEqual(label, 'shell')

    def test_a_missing_directory_names_the_host_and_launches_nothing(self):
        with patch.object(sh2pil_open, 'launch') as launch, \
             patch.object(sh2pil_open, 'remote_run',
                          return_value=argparse.Namespace(returncode=0,
                                                          stdout=b'sh2pil-no-dir\n')):
            self.assertEqual(sh2pil_open.shell_remote('build-host', '/srv/api'), 1)
            self.assertEqual(sh2pil_open.remote_directory_problem('build-host', '/srv/api'),
                             '/srv/api does not exist on build-host')
        launch.assert_not_called()

    def test_an_unreachable_host_still_opens_a_shell(self):
        # The check cannot answer without a master, so it says nothing and the shell opens:
        # a connection failure belongs in the terminal that tried to connect.
        refused = argparse.Namespace(returncode=255, stdout=b'', stderr=b'Permission denied')
        with patch.object(sh2pil_open, 'remote_run', return_value=refused), \
             patch.object(sh2pil_open, 'launch', return_value=0) as launch:
            self.assertEqual(sh2pil_open.shell_remote('build-host', '/srv/api'), 0)
        launch.assert_called_once()

    def test_an_emitted_command_offers_a_destination_it_must_not_run(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            self.assertEqual(sh2pil_open.shell_remote('build-host', '/srv/api', '', 'tab', True), 0)
        self.assertIn('ssh', output.getvalue())
        self.assertIn('build-host', output.getvalue())
        with patch.object(sh2pil_open, 'launch') as launch, \
             patch.object(sh2pil_open, 'remote_run') as run:
            self.assertEqual(sh2pil_open.shell_remote('options;rm', '/srv/api'), 1)
            self.assertEqual(sh2pil_open.shell_remote('build-host', 'relative/api'), 1)
        launch.assert_not_called()
        run.assert_not_called()


class HostEnvTest(unittest.TestCase):
    """The environment one host's session is given, from ssh_env and ssh_env.<destination>.

    A terminal this machine opens on a host is still this machine's terminal, so TERM is the
    value that has to travel: a host with no terminfo entry for xterm-kitty needs the session
    to describe itself as something the host knows.  The setting is a host's own, so no
    sshd has to be changed for it, and it is matched against the destination exactly as
    zmx_servers spells it.
    """

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.config = pathlib.Path(self.tmp.name) / 'config.yaml'

    @contextlib.contextmanager
    def configured(self, text):
        self.config.write_text(text)
        with patch.object(sh2pil_open, 'CONFIG', self.config):
            yield

    def test_a_session_on_a_host_exports_the_shared_variables_first(self):
        with self.configured('ssh_env: TERM=xterm-256color\n'):
            remote = sh2pil_open.remote_shell_argv('build-host', '/srv/api')[-1]
        self.assertTrue(remote.startswith('export TERM=xterm-256color; '), remote)
        self.assertIn("cd -- /srv/api", remote)
        # The exports come before everything else, so the login shell, and what it runs,
        # inherit them.
        self.assertLess(remote.index('export TERM'), remote.index('cd --'))

    def test_one_host_replaces_a_variable_without_dropping_the_rest(self):
        text = ('ssh_env: TERM=xterm-256color, LANG=C.UTF-8\n'
                'ssh_env.build-host: TERM=screen-256color\n')
        with self.configured(text):
            self.assertEqual(sh2pil_open.remote_env('build-host'),
                             [('TERM', 'screen-256color'), ('LANG', 'C.UTF-8')])
            self.assertEqual(sh2pil_open.remote_env('build-host.example'),
                             [('TERM', 'xterm-256color'), ('LANG', 'C.UTF-8')])

    def test_a_value_reaches_the_host_exactly_as_it_was_written(self):
        with self.configured('ssh_env: GREETING=hello world\n'):
            remote = sh2pil_open.remote_shell_argv('build-host', '')[-1]
        self.assertTrue(remote.startswith("export GREETING='hello world'; "), remote)

    def test_an_entry_that_is_not_a_variable_is_ignored_and_the_rest_still_arrive(self):
        errors = io.StringIO()
        with self.configured('ssh_env: 1BAD=oops, TERM=xterm-256color\n'):
            with contextlib.redirect_stderr(errors):
                remote = sh2pil_open.remote_shell_argv('build-host', '')[-1]
        self.assertNotIn('1BAD', remote)
        self.assertIn('export TERM=xterm-256color; ', remote)
        self.assertIn('1BAD=oops', errors.getvalue())

    def test_a_host_with_no_setting_is_untouched(self):
        with self.configured('zmx_servers: build-host\n'):
            remote = sh2pil_open.remote_shell_argv('build-host', '')[-1]
        self.assertNotIn('export', remote)

    def test_every_session_the_picker_opens_on_a_host_gets_the_environment(self):
        with self.configured('ssh_env: TERM=xterm-256color\n'):
            commands = [
                sh2pil_open.session_remote_argv('build-host', '/srv/api', 'abc'),
                sh2pil_open.remote_tool_argv('build-host', '/srv/api', 'lazygit'),
                sh2pil_open.remote_shell_argv('build-host', '/srv/api'),
                sh2pil_open.remote_editor_argv('build-host', '/srv/api', ''),
            ]
        for argv in commands:
            self.assertTrue(argv[-1].startswith('export TERM=xterm-256color; '), argv[-1])

    def test_a_remote_host_a_chat_and_an_attach_get_the_environment_too(self):
        with self.configured('ssh_env: TERM=xterm-256color\n'), \
             patch.object(sh2pil_open, 'zmx_remote_taken_names', return_value=set()), \
             patch.object(sh2pil_open, 'launch', return_value=0) as launch:
            sh2pil_open.zmx_remote_new('build-host', '/srv/api', 'tab')
            self.assertTrue(launch.call_args.args[0][-1].startswith(
                'export TERM=xterm-256color; '), launch.call_args.args[0][-1])
            sh2pil_open.zmx_remote_switch('build-host', 'pi-api', '/srv/api', 'tab')
            self.assertTrue(launch.call_args.args[0][-1].startswith(
                'export TERM=xterm-256color; '), launch.call_args.args[0][-1])

    def test_a_read_is_left_alone(self):
        # A question asked of a host is not a session anyone works in, and its answer is
        # parsed, so nothing is exported in front of it.
        completed = sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, b'/srv/one\n', b'')
        with self.configured('ssh_env: TERM=xterm-256color\n'), \
             patch.object(sh2pil_open.subprocess, 'run', return_value=completed) as run:
            sh2pil_open.zmx_remote_projects('build-host')
        self.assertNotIn('export', run.call_args.args[0][-1])


class ConfigEntriesTest(unittest.TestCase):
    """The shared config is read by the picker and the helper, and the picker's nested
    `tools:` section must not read as a top-level setting here."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.config = pathlib.Path(self.tmp.name) / 'config.yaml'

    @contextlib.contextmanager
    def configured(self, text):
        self.config.write_text(text)
        with patch.object(sh2pil_open, 'CONFIG', self.config):
            yield

    def test_a_nested_line_is_not_a_top_level_entry(self):
        text = ('zmx_servers: build-host # the builder\n'
                'tools:\n'
                '  file_browser: yazi\n'
                '  hosts:\n'
                '    build-host:\n'
                '      file_browser: lf\n')
        with self.configured(text):
            entries = sh2pil_open.config_entries()
        # `tools:` is a top-level line that opens a section; the helpers do not read it, and
        # nothing under it may read as a setting of its own.
        self.assertEqual(entries, [('zmx_servers', 'build-host'), ('tools', '')])
        self.assertEqual(sh2pil_open.config_value('file_browser'), '')

    def test_flat_keys_and_ssh_env_still_read(self):
        text = ('zmx_servers: build-host\n'
                'file_browser: ranger\n'
                'ssh_env: TERM=xterm-256color\n'
                'ssh_env.build-host: LANG=C.UTF-8\n')
        with self.configured(text):
            self.assertEqual(sh2pil_open.config_value('file_browser'), 'ranger')
            self.assertEqual(sh2pil_open.remote_env('build-host'),
                             [('TERM', 'xterm-256color'), ('LANG', 'C.UTF-8')])


class RemoteEditorTest(unittest.TestCase):
    """The project editor on a host's own directory: that host's editor, chosen there.

    An editor is a machine's own tool, with its own plugins and its own clipboard, so nothing
    about an editor here travels with the command, and the editor that will run is the one the
    check looks for.
    """

    def test_the_hosts_own_editor_opens_the_hosts_directory(self):
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch, \
             patch.object(sh2pil_open, 'remote_run',
                          return_value=argparse.Namespace(returncode=0, stdout=b'')):
            result = sh2pil_open.editor_remote('build-host', '/srv/home infra')
        self.assertEqual(result, 0)
        argv, cwd, label, place, emit = launch.call_args.args
        self.assertEqual(argv[:7], [sh2pil_open.ssh_binary(), '-S',
                                    sh2pil_open.ssh_control_path(), '-o',
                                    'ControlMaster=no', '-t', 'build-host'])
        remote = argv[7]
        self.assertIn("cd -- '/srv/home infra'", remote)
        # The host's login shell is what expands the choice, so the fallback reaches it as
        # written and the path this machine would use never appears.
        self.assertIn('exec ${EDITOR:-nvim}', remote)
        self.assertIn("'/srv/home infra'", remote)
        self.assertNotIn('opt/homebrew', remote)
        self.assertEqual((cwd, label, place, emit), (str(pathlib.Path.home()), '', 'tab', False))

    def test_a_named_editor_replaces_the_hosts_choice(self):
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch, \
             patch.object(sh2pil_open, 'remote_run',
                          return_value=argparse.Namespace(returncode=0, stdout=b'')):
            sh2pil_open.editor_remote('build-host', '/srv/api', 'vim')
        self.assertIn('exec vim /srv/api', launch.call_args.args[0][7])

    def test_the_check_looks_for_the_editor_the_login_shell_will_use(self):
        asked = []
        result = argparse.Namespace(returncode=0, stdout=b'')
        with patch.object(sh2pil_open, 'launch', return_value=0), \
             patch.object(sh2pil_open, 'remote_run',
                          side_effect=lambda *rest, **kw: asked.append(rest[1]) or result):
            sh2pil_open.editor_remote('build-host', '/srv/api')
            sh2pil_open.editor_remote('build-host', '/srv/api', 'vim')
        self.assertIn('test -d /srv/api', asked[0])
        self.assertIn('set -- ${EDITOR:-nvim}', asked[0])
        self.assertIn('command -v "$1"', asked[0])
        self.assertIn('echo sh2pil-no-editor "$1"', asked[0])
        self.assertIn('set -- vim', asked[1])

    def test_a_missing_editor_or_directory_names_the_host_and_launches_nothing(self):
        cases = [('sh2pil-no-dir\n', '', '/srv/api does not exist on build-host'),
                 ('sh2pil-no-editor\n', '',
                  "neither the host's $EDITOR nor nvim is installed on build-host"),
                 # The host names the editor it would have run, so the reader is told what to
                 # install, or which of the host's own choices is broken.
                 ('sh2pil-no-editor emacs\n', '', 'emacs is not installed on build-host'),
                 ('sh2pil-no-editor vim\n', 'vim', 'vim is not installed on build-host')]
        for told, editor, reason in cases:
            with self.subTest(reason=reason), \
                 patch.object(sh2pil_open, 'launch') as launch, \
                 patch.object(sh2pil_open, 'remote_run',
                              return_value=argparse.Namespace(returncode=0,
                                                              stdout=told.encode())):
                self.assertEqual(
                    sh2pil_open.remote_editor_problem('build-host', '/srv/api', editor), reason)
                self.assertEqual(
                    sh2pil_open.editor_remote('build-host', '/srv/api', editor), 1)
            launch.assert_not_called()

    def test_an_unreachable_host_still_opens_the_editor(self):
        refused = argparse.Namespace(returncode=255, stdout=b'', stderr=b'Permission denied')
        with patch.object(sh2pil_open, 'remote_run', return_value=refused), \
             patch.object(sh2pil_open, 'launch', return_value=0) as launch:
            self.assertEqual(sh2pil_open.editor_remote('build-host', '/srv/api'), 0)
        launch.assert_called_once()

    def test_a_destination_or_a_directory_it_must_not_run(self):
        with patch.object(sh2pil_open, 'launch') as launch, \
             patch.object(sh2pil_open, 'remote_run') as run:
            self.assertEqual(sh2pil_open.editor_remote('options;rm', '/srv/api'), 1)
            self.assertEqual(sh2pil_open.editor_remote('build-host', 'relative/api'), 1)
        launch.assert_not_called()
        run.assert_not_called()


class ClipboardImageTest(unittest.TestCase):
    """Reading the image on this machine's clipboard, with no terminal to ask."""

    def directory(self):
        directory = pathlib.Path(tempfile.mkdtemp()) / 'paste'
        directory.mkdir()
        self.addCleanup(shutil.rmtree, directory.parent, ignore_errors=True)
        return directory

    @unittest.skipUnless(sys.platform == 'darwin', 'the pasteboard is read with AppleScript')
    def test_an_image_on_the_clipboard_is_written_where_the_caller_can_read_it(self):
        directory = self.directory()
        asked = []

        def fake_run(argv, **kwargs):
            asked.append(argv)
            # The script names the file it writes, so the write is done where the script
            # says and both halves of the pair are checked together.
            pathlib.Path(re.search(r'POSIX file "([^"]+)"', argv[-1]).group(1)).write_bytes(
                b'\x89PNG\r\n\x1a\n')
            return argparse.Namespace(returncode=0, stdout=b'')

        with patch.object(sh2pil_open.tempfile, 'mkdtemp', return_value=str(directory)), \
             patch.object(sh2pil_open.subprocess, 'run', side_effect=fake_run):
            image = sh2pil_open.clipboard_image()
        self.assertEqual(image, directory / 'image.png')
        self.assertEqual(image.read_bytes(), b'\x89PNG\r\n\x1a\n')
        self.assertEqual(asked[0][0], 'osascript')
        self.assertIn('«class PNGf»', asked[0][-1])

    def test_a_clipboard_that_holds_no_image_leaves_nothing_behind(self):
        directory = self.directory()
        refused = argparse.Namespace(returncode=1, stdout=b'', stderr=b'coercion failed')
        with patch.object(sh2pil_open.tempfile, 'mkdtemp', return_value=str(directory)), \
             patch.object(sh2pil_open.subprocess, 'run', return_value=refused):
            self.assertIsNone(sh2pil_open.clipboard_image())
        self.assertFalse(directory.exists())


class PasteRemoteTest(unittest.TestCase):
    """Storing one image on a host: the host writes a file, its path is the answer."""

    def image(self):
        """The clipboard, as the reader hands it over: one file in a directory of its own."""
        directory = pathlib.Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, directory, ignore_errors=True)
        path = directory / 'image.png'
        path.write_bytes(b'\x89PNG\r\n\x1a\n')
        return path

    def test_the_host_writes_the_image_and_prints_its_path(self):
        asked = []

        def fake_run(server, command, **kwargs):
            asked.append((server, command, kwargs))
            name = command.split('cat > "$dir/')[1].split('"')[0]
            return argparse.Namespace(returncode=0, stdout=f'/tmp/sh2pil-paste/{name}\n'.encode())

        with patch.object(sh2pil_open, 'clipboard_image', return_value=self.image()), \
             patch.object(sh2pil_open, 'remote_run', side_effect=fake_run):
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                self.assertEqual(sh2pil_open.paste_remote('build-host'), 0)
        server, command, keywords = asked[0]
        name = command.split('cat > "$dir/')[1].split('"')[0]
        self.assertEqual(server, 'build-host')
        self.assertEqual(keywords['script'], b'\x89PNG\r\n\x1a\n')
        self.assertEqual(output.getvalue(), f'/tmp/sh2pil-paste/{name}\n')
        # The bytes arrive on standard input, and everything else happens inside the one
        # directory this tool owns on the host.
        self.assertIn('umask 077', command)
        # macOS hands a temporary directory over with a trailing slash, and a path that says
        # `//` in the middle is what the reader then sees in the prompt.
        self.assertIn('base=${TMPDIR:-/tmp}', command)
        self.assertIn('dir=${base%/}/sh2pil-paste', command)
        self.assertNotIn('TMPDIR:-/tmp}/sh2pil-sessions', command)
        self.assertIn('chmod 700', command)
        self.assertIn('find "$dir" -type f -mtime +1 -delete', command)

    def test_every_paste_gets_a_name_of_its_own(self):
        names = [sh2pil_open.paste_name() for _ in range(2)]
        self.assertNotEqual(names[0], names[1])
        for name in names:
            self.assertRegex(name, r'^image-[0-9a-f]{8}\.png$')

    def test_an_option_like_destination_never_reaches_ssh(self):
        with patch.object(sh2pil_open, 'clipboard_image') as image, \
             patch.object(sh2pil_open, 'remote_run') as run:
            told = io.StringIO()
            with contextlib.redirect_stderr(told):
                self.assertEqual(sh2pil_open.paste_remote('options;rm -rf /'), 1)
        self.assertIn('SSH config alias', told.getvalue())
        run.assert_not_called()
        image.assert_not_called()

    def test_a_clipboard_without_an_image_says_so_and_touches_no_host(self):
        with patch.object(sh2pil_open, 'clipboard_image', return_value=None), \
             patch.object(sh2pil_open, 'remote_run') as run:
            told = io.StringIO()
            with contextlib.redirect_stderr(told):
                self.assertEqual(sh2pil_open.paste_remote('build-host'), 1)
        self.assertIn('no image on this clipboard', told.getvalue())
        run.assert_not_called()

    def test_a_host_that_refuses_the_write_names_its_own_reason(self):
        refused = argparse.Namespace(returncode=255, stdout=b'', stderr=b'Permission denied\n')
        with patch.object(sh2pil_open, 'clipboard_image', return_value=self.image()), \
             patch.object(sh2pil_open, 'remote_run', return_value=refused):
            told = io.StringIO()
            with contextlib.redirect_stderr(told):
                self.assertEqual(sh2pil_open.paste_remote('build-host'), 1)
        self.assertIn('Permission denied', told.getvalue())

    def test_a_host_that_says_nothing_is_a_failure_too(self):
        silent = argparse.Namespace(returncode=0, stdout=b'', stderr=b'')
        with patch.object(sh2pil_open, 'clipboard_image', return_value=self.image()), \
             patch.object(sh2pil_open, 'remote_run', return_value=silent):
            told = io.StringIO()
            with contextlib.redirect_stderr(told):
                self.assertEqual(sh2pil_open.paste_remote('build-host'), 1)
        self.assertIn('did not say where the image was written', told.getvalue())

    def test_an_emitted_command_reads_no_clipboard_and_asks_no_host(self):
        with patch.object(sh2pil_open, 'clipboard_image') as image, \
             patch.object(sh2pil_open, 'remote_run') as run:
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                self.assertEqual(sh2pil_open.paste_remote('build-host', True), 0)
        line = output.getvalue()
        self.assertIn('ssh', line)
        self.assertIn('-S ' + sh2pil_open.ssh_control_path(), line)
        self.assertIn('ControlMaster=no', line)
        self.assertIn('BatchMode=yes', line)
        self.assertIn('build-host', line)
        self.assertIn('umask 077', line)
        image.assert_not_called()
        run.assert_not_called()


class SshDestinationTest(unittest.TestCase):
    """The host on an ssh command line, read out of a process list."""

    def test_the_destination_is_the_first_word_that_is_not_an_option(self):
        cases = [
            (['ssh', 'build-host'], 'build-host'),
            (['/opt/homebrew/bin/ssh', '-t', 'build-host', 'cd -- /srv'], 'build-host'),
            (['ssh', '-p', '22222', 'root@192.0.2.11'], 'root@192.0.2.11'),
            (['ssh', '-p2222', 'host'], 'host'),
            (['ssh', '--', 'host'], 'host'),
            (['ssh', '-S', '/Users/me/.ssh/pib-sk-%C', '-o', 'ControlMaster=no',
              '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=5', '-t', 'user@192.0.2.15',
              'export TERM=xterm-256color; cd -- /srv/api'], 'user@192.0.2.15'),
        ]
        for argv, wanted in cases:
            with self.subTest(argv=argv):
                self.assertEqual(sh2pil_open.ssh_destination_from_argv(argv), wanted)

    def test_a_word_that_must_not_reach_a_shell_is_not_a_destination(self):
        self.assertEqual(sh2pil_open.ssh_destination_from_argv(['ssh', '-t', 'options;rm -rf /']), '')
        self.assertEqual(sh2pil_open.ssh_destination_from_argv(['ssh', '-t', '-oProxyCommand=x']),
                         '')

    def test_a_command_that_is_not_ssh_names_no_host(self):
        for argv in ([], ['ssh'], ['ssh', '-t'], ['zmx', 'attach', 'dev'],
                     ['nvim', '/srv/api']):
            with self.subTest(argv=argv):
                self.assertEqual(sh2pil_open.ssh_destination_from_argv(argv), '')

    def test_a_window_reports_the_host_of_the_ssh_process_it_runs(self):
        window = {'commands': [['/bin/zsh', '-lic', 'pi'], ['ssh', '-t', 'build-host', 'x']]}
        self.assertEqual(sh2pil_open.window_ssh_destination(window), 'build-host')
        self.assertEqual(sh2pil_open.window_ssh_destination({'commands': []}), '')
        self.assertEqual(sh2pil_open.window_ssh_destination({}), '')


class PasteWindowTest(unittest.TestCase):
    """One key in a window: the window's own host decides where the image goes."""

    # A window that runs a chat on a host, exactly as the picker opens one.
    SSH_LINE = ['/opt/homebrew/bin/ssh', '-S', '/Users/me/.ssh/pib-sk-%C',
                '-o', 'ControlMaster=no', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=5',
                '-t', 'user@192.0.2.15',
                'export TERM=xterm-256color; cd -- /srv/api && { zsh -lic pi; }']
    PREFIX = ['kitten', '@', '--to', 'unix:/tmp/kitty']

    def window(self, commands=None, identifier='42'):
        return {'medium': 'kitty', 'label': f'kitty window {identifier}', 'target': identifier,
                'title': '', 'cwd': '', 'pids': [7], 'prefix': self.PREFIX,
                'commands': [self.SSH_LINE] if commands is None else commands}

    def image(self):
        directory = pathlib.Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, directory, ignore_errors=True)
        path = directory / 'image.png'
        path.write_bytes(b'\x89PNG\r\n\x1a\n')
        return path

    def log(self):
        directory = pathlib.Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, directory, ignore_errors=True)
        return directory / 'errors.log'

    def test_the_host_comes_from_the_windows_own_ssh_process(self):
        asked = []
        typed = []

        def fake_run(server, command, **kwargs):
            asked.append((server, command))
            return argparse.Namespace(returncode=0,
                                      stdout=b'/tmp/sh2pil-paste/image-ab12cd34.png\n')

        with patch.object(sh2pil_open, 'kitten_prefix', return_value=self.PREFIX), \
             patch.object(sh2pil_open, 'kitty_windows', return_value=[self.window()]), \
             patch.object(sh2pil_open, 'clipboard_image', return_value=self.image()), \
             patch.object(sh2pil_open, 'remote_run', side_effect=fake_run), \
             patch.object(sh2pil_open, 'send_window_text',
                          side_effect=lambda *rest: typed.append(rest) or True):
            self.assertEqual(sh2pil_open.paste_window('42'), 0)
        self.assertEqual([server for server, _ in asked], ['user@192.0.2.15'])
        self.assertEqual(typed, [(self.PREFIX, '42', '/tmp/sh2pil-paste/image-ab12cd34.png')])

    def test_a_window_that_runs_here_gets_a_path_here(self):
        image = self.image()
        typed = []
        with patch.object(sh2pil_open, 'kitten_prefix', return_value=self.PREFIX), \
             patch.object(sh2pil_open, 'kitty_windows',
                          return_value=[self.window([['/bin/zsh', '-lic', 'pi']])]), \
             patch.object(sh2pil_open, 'clipboard_image', return_value=image), \
             patch.object(sh2pil_open, 'remote_run') as run, \
             patch.object(sh2pil_open, 'send_window_text',
                          side_effect=lambda *rest: typed.append(rest) or True):
            self.assertEqual(sh2pil_open.paste_window('42'), 0)
        run.assert_not_called()
        self.assertEqual(typed, [(self.PREFIX, '42', str(image))])
        # The file has to outlive the key: the store reads it when the prompt is sent.
        self.assertTrue(image.exists())

    def test_no_image_on_the_clipboard_runs_the_terminals_own_paste(self):
        with patch.object(sh2pil_open, 'kitten_prefix', return_value=self.PREFIX), \
             patch.object(sh2pil_open, 'kitty_windows', return_value=[self.window()]), \
             patch.object(sh2pil_open, 'clipboard_image', return_value=None), \
             patch.object(sh2pil_open, 'paste_clipboard_text', return_value=0) as fallback, \
             patch.object(sh2pil_open, 'send_window_text') as typed:
            self.assertEqual(sh2pil_open.paste_window('42'), 0)
        fallback.assert_called_once_with(self.PREFIX, '42')
        typed.assert_not_called()

    def test_the_fallback_is_kitty_own_paste_on_the_same_window(self):
        with patch.object(sh2pil_open.subprocess, 'run',
                          return_value=argparse.Namespace(returncode=0)) as run:
            self.assertEqual(sh2pil_open.paste_clipboard_text(self.PREFIX, '42'), 0)
        self.assertEqual(run.call_args.args[0], self.PREFIX + [
            'action', '--match', 'id:42', 'paste_from_clipboard'])

    def test_one_path_is_typed_as_it_is_with_no_escape_read(self):
        with patch.object(sh2pil_open.subprocess, 'run',
                          return_value=argparse.Namespace(returncode=0)) as run:
            self.assertTrue(sh2pil_open.send_window_text(self.PREFIX, '42',
                                                      '/tmp/sh2pil-paste/a\nb.png'))
        self.assertEqual(run.call_args.args[0],
                         self.PREFIX + ['send-text', '--match', 'id:42', '--stdin'])
        self.assertEqual(run.call_args.kwargs['input'], b'/tmp/sh2pil-paste/a\nb.png')

    def test_a_window_kitty_does_not_know_types_nothing_and_is_written_down(self):
        log = self.log()
        with patch.object(sh2pil_open, 'kitten_prefix', return_value=self.PREFIX), \
             patch.object(sh2pil_open, 'kitty_windows', return_value=[]), \
             patch.object(sh2pil_open, 'LOG', log), \
             patch.object(sh2pil_open, 'send_window_text') as typed:
            told = io.StringIO()
            with contextlib.redirect_stderr(told):
                self.assertEqual(sh2pil_open.paste_window('42'), 1)
        typed.assert_not_called()
        self.assertIn('no kitty window 42', told.getvalue())
        # A key-binding child has no screen, so the failure also outlives the keystroke.
        self.assertIn('no kitty window 42', log.read_text())

    def test_a_host_that_refuses_the_write_types_nothing(self):
        refused = argparse.Namespace(returncode=255, stdout=b'', stderr=b'Permission denied')
        with patch.object(sh2pil_open, 'kitten_prefix', return_value=self.PREFIX), \
             patch.object(sh2pil_open, 'kitty_windows', return_value=[self.window()]), \
             patch.object(sh2pil_open, 'clipboard_image', return_value=self.image()), \
             patch.object(sh2pil_open, 'remote_run', return_value=refused), \
             patch.object(sh2pil_open, 'LOG', self.log()), \
             patch.object(sh2pil_open, 'send_window_text') as typed:
            told = io.StringIO()
            with contextlib.redirect_stderr(told):
                self.assertEqual(sh2pil_open.paste_window('42'), 1)
        typed.assert_not_called()
        self.assertIn('Permission denied', told.getvalue())

    def test_no_kitty_at_all_is_a_failure_and_not_a_silent_one(self):
        with patch.object(sh2pil_open, 'kitten_prefix', return_value=None), \
             patch.object(sh2pil_open, 'LOG', self.log()):
            told = io.StringIO()
            with contextlib.redirect_stderr(told):
                self.assertEqual(sh2pil_open.paste_window('42'), 1)
        self.assertIn('no kitty is running', told.getvalue())


class RunToolTest(unittest.TestCase):
    def test_run_tool_refuses_an_empty_command(self):
        told = io.StringIO()
        with contextlib.redirect_stderr(told):
            self.assertEqual(sh2pil_open.run_tool('/tmp', [], 'tool', 'tab', False), 1)
        self.assertIn('needs a command', told.getvalue())

    def test_run_tool_prints_the_command_when_asked(self):
        told = io.StringIO()
        with contextlib.redirect_stdout(told):
            self.assertEqual(sh2pil_open.run_tool('/tmp', ['rg', '--version'], 'rg tmp', 'tab',
                                                True), 0)
        self.assertIn('rg --version', told.getvalue())


class ZmxPickerTest(unittest.TestCase):
    """The zmx rows the picker shows, and the verbs that act on one row."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = pathlib.Path(self.tmp.name) / 'sessions'
        self.project = self.root / '--project--'
        self.project.mkdir(parents=True)

    def transcript(self, identifier, name=''):
        path = self.project / f'2026-01-01_{identifier}.jsonl'
        body = json.dumps({'type': 'session', 'cwd': '/project'}) + '\n'
        if name:
            body += json.dumps({'type': 'session_info', 'name': name}) + '\n'
        path.write_text(body)
        return path

    def test_a_file_url_becomes_a_path(self):
        self.assertEqual(sh2pil_open.zmx_cwd('file://host/Users/me'), '/Users/me')
        self.assertEqual(sh2pil_open.zmx_cwd('file:///Users/me'), '/Users/me')
        self.assertEqual(sh2pil_open.zmx_cwd('/Users/me'), '/Users/me')
        self.assertEqual(sh2pil_open.zmx_cwd(''), '')

    def test_a_transcript_file_is_found_by_its_session_id(self):
        transcript = self.transcript('aaa')
        self.assertEqual(sh2pil_open.transcript_for('aaa', self.root), str(transcript))
        self.assertEqual(sh2pil_open.transcript_for('bbb', self.root), '')
        self.assertEqual(sh2pil_open.transcript_for('', self.root), '')

    def test_rows_name_the_chat_and_keep_a_command_with_spaces(self):
        transcript = self.transcript('aaa', 'Fix ingress docs')
        listing = ('\u2192 name=pi-aaa\tpid=1\tclients=1\tcreated=100\t'
                   "cwd=file://host/Users/me/project\tcmd=pi --fork 1 --name 'Fix docs'\t"
                   'pi=aaa\tproject=project\n'
                   '  name=shell\tpid=2\tclients=0\tcreated=200\tcwd=file://host/Users/me\n'
                   '  pid=3\tclients=0\tcreated=300\n')
        with (patch.object(sh2pil_open, 'zmx_run', return_value=listing),
              patch.object(sh2pil_open, 'SESSIONS_DIR', self.root)):
            entries = sh2pil_open.zmx_sessions()
        self.assertEqual([entry['name'] for entry in entries], ['shell', 'pi-aaa'])
        self.assertEqual(entries[1]['chat'], 'Fix ingress docs')
        self.assertEqual(entries[1]['cmd'], "pi --fork 1 --name 'Fix docs'")
        self.assertEqual(entries[1]['cwd'], '/Users/me/project')
        self.assertEqual(entries[1]['file'], str(transcript))
        self.assertEqual(entries[1]['clients'], 1)
        self.assertTrue(entries[1]['current'])
        self.assertEqual(entries[0]['chat'], '')
        self.assertFalse(entries[0]['current'])

    def test_switch_focuses_the_window_that_shows_the_session(self):
        window = {'target': '101', 'label': 'kitty window 101 (zmx pi-aaa)'}
        focused = []
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'zmx_taken_names', return_value={'pi-aaa'}),
              patch.object(sh2pil_open, 'zmx_client_window', return_value=window),
              patch.object(sh2pil_open, 'focus_window',
                           side_effect=lambda found, emit: focused.append((found, emit)) or 0)):
            self.assertEqual(sh2pil_open.zmx_switch('pi-aaa', '/project', 'tab', True), 0)
        self.assertEqual(focused, [(window, True)])
    def test_switch_attaches_a_client_when_no_window_shows_the_session(self):
        opened = []
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'zmx_taken_names', return_value={'pi-aaa'}),
              patch.object(sh2pil_open, 'zmx_client_window', return_value=None),
              patch.object(sh2pil_open, 'launch',
                           side_effect=lambda argv, cwd, label, place, emit:
                           opened.append((argv, cwd, label, place)) or 0)):
            sh2pil_open.zmx_switch('pi-aaa', str(self.project), 'window')
        self.assertEqual(opened[0], (['env', '-u', 'ZMX_SESSION', 'PIB_ZMX=/bin/zmx',
                                      '/bin/zmx', 'attach', 'pi-aaa'],
                                     str(self.project), '', 'window'))

    def test_switch_opens_the_client_in_the_requested_pane(self):
        # A detached zmx row attaches in the pane where the picker runs when asked.
        places = []
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'zmx_taken_names', return_value={'pi-aaa'}),
              patch.object(sh2pil_open, 'zmx_client_window', return_value=None),
              patch.object(sh2pil_open, 'launch',
                           side_effect=lambda argv, cwd, label, place, emit:
                           places.append(place) or 0),
              contextlib.redirect_stderr(io.StringIO())):
            self.assertEqual(sh2pil_open.zmx_switch('pi-aaa', str(self.project), 'current'), 0)
        self.assertEqual(places, ['current'])

    def test_switch_starts_a_client_in_home_when_the_directory_is_gone(self):
        opened = []
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'zmx_taken_names', return_value={'pi-aaa'}),
              patch.object(sh2pil_open, 'zmx_client_window', return_value=None),
              patch.object(sh2pil_open, 'launch',
                           side_effect=lambda argv, cwd, label, place, emit:
                           opened.append(cwd) or 0)):
            sh2pil_open.zmx_switch('pi-aaa', str(self.project / 'gone'), 'tab')
        self.assertEqual(opened, [str(pathlib.Path.home())])

    def test_switch_and_kill_refuse_a_name_zmx_does_not_know(self):
        # `zmx attach` creates a session for a free name, so a stale row must not become one.
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'zmx_taken_names', return_value=set()),
              patch.object(sh2pil_open.sys.stdin, 'isatty', return_value=False)):
            self.assertEqual(sh2pil_open.zmx_switch('gone', '/project', 'tab'), 1)
            self.assertEqual(sh2pil_open.zmx_kill('gone'), 1)

    def test_kill_runs_zmx_and_reports_its_message(self):
        result = argparse.Namespace(returncode=0, stdout=b'killed session pi-aaa\n', stderr=b'')
        commands = []
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'zmx_taken_names', return_value={'pi-aaa'}),
              patch.object(sh2pil_open.subprocess, 'run',
                           side_effect=lambda argv, **rest: commands.append(argv) or result)):
            self.assertEqual(sh2pil_open.zmx_kill('pi-aaa'), 0)
        self.assertEqual(commands, [['/bin/zmx', 'kill', 'pi-aaa']])

    def test_history_prints_the_end_of_a_session_scrollback(self):
        output = io.StringIO()
        result = argparse.Namespace(returncode=0, stdout=b'one\r\ntwo\r\nthree\r\n',
                                    stderr=b'')
        commands = []
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'zmx_taken_names', return_value={'pi-aaa'}),
              patch.object(sh2pil_open.subprocess, 'run',
                           side_effect=lambda argv, **rest: commands.append(argv) or result),
              contextlib.redirect_stdout(output)):
            self.assertEqual(sh2pil_open.zmx_history('pi-aaa', 2), 0)
        self.assertEqual(commands, [['/bin/zmx', 'history', 'pi-aaa']])
        self.assertEqual(output.getvalue(), 'two\nthree\n')

    def test_history_refuses_a_name_zmx_does_not_know(self):
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'zmx_taken_names', return_value=set()),
              patch.object(sh2pil_open.sys.stdin, 'isatty', return_value=False)):
            self.assertEqual(sh2pil_open.zmx_history('gone', 5), 1)

    def test_history_reports_what_zmx_said_when_the_read_fails(self):
        result = argparse.Namespace(returncode=1, stdout=b'', stderr=b'no such session\n')
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open, 'zmx_taken_names', return_value={'pi-aaa'}),
              patch.object(sh2pil_open.subprocess, 'run', return_value=result),
              patch.object(sh2pil_open.sys.stdin, 'isatty', return_value=False)):
            self.assertEqual(sh2pil_open.zmx_history('pi-aaa', 5), 1)

    def test_copy_prints_the_attach_command_and_puts_it_on_the_clipboard(self):
        copied = []
        output = io.StringIO()
        with (patch.object(sh2pil_open.shutil, 'which', return_value='/usr/bin/pbcopy'),
              patch.object(sh2pil_open.subprocess, 'run',
                           side_effect=lambda argv, **rest: copied.append(rest.get('input')) or
                           argparse.Namespace(returncode=0)),
              contextlib.redirect_stdout(output)):
            self.assertEqual(sh2pil_open.zmx_copy('pi-aaa'), 0)
        self.assertEqual(output.getvalue(), 'zmx attach pi-aaa\n')
        self.assertEqual(copied, [b'zmx attach pi-aaa'])

    def test_a_line_without_a_name_is_not_a_row(self):
        with patch.object(sh2pil_open, 'zmx_run', return_value='  pid=1\tclients=0\n'):
            self.assertEqual(sh2pil_open.zmx_sessions(), [])


class SessionRemoteFileTest(unittest.TestCase):
    """One host's transcript, handed over as the bytes the host stores."""

    def test_the_bytes_of_the_path_come_back_unchanged(self):
        payload = b'{"type":"session"}\n{"type":"message"}\n'
        completed = sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, payload, b'')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=completed) as run:
            data = sh2pil_open.session_remote_file('build-host', '/srv/api/store/a.jsonl')
        self.assertEqual(data, payload)
        remote = run.call_args.args[0][10]
        self.assertEqual(remote, "cat /srv/api/store/a.jsonl")

    def test_a_path_with_a_space_stays_one_word(self):
        completed = sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, b'', b'')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=completed) as run:
            sh2pil_open.session_remote_file('build-host', "/srv/my api/a.jsonl")
        self.assertEqual(run.call_args.args[0][10], "cat '/srv/my api/a.jsonl'")

    def test_a_relative_or_multiline_path_is_refused(self):
        for path in ('store/a.jsonl', '', '/srv/a\nrm -rf /', '/srv/a\r'):
            with self.assertRaises(ValueError):
                sh2pil_open.session_remote_file('build-host', path)

    def test_a_host_that_cannot_read_it_reports_its_own_words(self):
        completed = sh2pil_open.subprocess.CompletedProcess(['ssh'], 1, b'',
                                                         b'cat: no such file\n')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=completed):
            with self.assertRaises(RuntimeError) as caught:
                sh2pil_open.session_remote_file('build-host', '/srv/gone.jsonl')
        self.assertIn('no such file', str(caught.exception))

    def test_the_bytes_are_written_without_a_decoding_step(self):
        written = bytearray()
        stdout = type('Out', (), {'buffer': type('B', (), {'write': written.extend})()})()
        with patch.object(sh2pil_open.sys, 'stdout', stdout):
            sh2pil_open.write_bytes(b'\xff\xfe answer\n')
        self.assertEqual(bytes(written), b'\xff\xfe answer\n')


if __name__ == '__main__':
    unittest.main()


class ClaudeStoreTest(unittest.TestCase):
    """The Claude Code store: its own transcripts, its own resume and fork command line."""

    def test_local_resume_fork_and_new_use_the_claude_command_line(self):
        with tempfile.TemporaryDirectory() as tmp:
            fields = {'cwd': tmp, 'place': 'tab', 'emit_cmd': True, 'label': 'claude demo',
                      'session': 'aaa', 'fork': False, 'name': '', 'force': False}
            opened = []
            with (patch.object(sh2pil_open, 'config_flag', return_value=False),
                  patch.object(sh2pil_open, 'launch',
                               side_effect=lambda argv, cwd, label, place, emit:
                               opened.append((argv, label)) or 0)):
                sh2pil_open.open_terminal('claude', argparse.Namespace(**fields))
                fields['fork'] = True
                fields['name'] = 'forked chat'
                sh2pil_open.open_terminal('claude', argparse.Namespace(**fields))
                fields['label'] = ''
                sh2pil_open.open_terminal('claude-new', argparse.Namespace(**fields))
        self.assertEqual(opened[0], (['claude', '--resume', 'aaa'], 'claude demo'))
        self.assertEqual(opened[1][0], ['claude', '--resume', 'aaa', '--fork-session',
                                        '--name', 'forked chat'])
        self.assertEqual(opened[1][1], 'claude demo')
        self.assertEqual(opened[2][0], ['claude'])

    def test_a_gone_project_still_resumes_a_claude_transcript(self):
        fields = {'cwd': '/nonexistent/project', 'place': 'tab', 'emit_cmd': True, 'label': '',
                  'session': '/srv/store/aaa.jsonl', 'fork': False, 'name': '', 'force': False}
        opened = []
        with (patch.object(sh2pil_open, 'config_flag', return_value=False),
              patch.object(sh2pil_open, 'launch',
                           side_effect=lambda argv, cwd, label, place, emit:
                           opened.append((argv, cwd)) or 0)):
            sh2pil_open.open_terminal('claude', argparse.Namespace(**fields))
        # The transcript path is passed through, and the terminal starts in $HOME.
        self.assertEqual(opened[0][0], ['claude', '--resume', '/srv/store/aaa.jsonl'])
        self.assertEqual(opened[0][1], str(pathlib.Path.home()))

    def test_remote_claude_resume_fork_and_new_go_through_its_own_command(self):
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch:
            sh2pil_open.session_remote_open('build-host', '/srv/api', 'aaa', 'claude')
        argv, cwd, label, _, _ = launch.call_args.args
        self.assertIn('/bin/zsh -lic', argv[7])
        self.assertIn('claude --resume aaa', argv[7])
        self.assertEqual(label, 'claude aaa')
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch:
            sh2pil_open.session_remote_open('build-host', '/srv/api', 'aaa', 'claude', True,
                                         'Fix docs (fork)')
        remote = launch.call_args.args[0][7]
        self.assertIn('claude --resume aaa --fork-session', remote)
        self.assertIn('--name', remote)
        self.assertIn('Fix docs (fork)', remote)
        with patch.object(sh2pil_open, 'launch', return_value=0) as launch:
            sh2pil_open.session_remote_open('build-host', '/srv/api', '', 'claude')
        argv, _, label, _, _ = launch.call_args.args
        self.assertIn('/bin/zsh -lic claude', argv[7])
        self.assertNotIn('--resume', argv[7])
        self.assertEqual(label, 'claude api')

    def test_a_claude_read_never_asks_the_host_about_its_processes(self):
        completed = sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, b'[]', b'')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=completed) as run:
            self.assertEqual(sh2pil_open.session_remote_list('build-host', 'claude', True), [])
        remote = run.call_args.args[0][10]
        self.assertIn('list --json --harness claude', remote)
        self.assertNotIn('--live', remote)

    def test_a_claude_transcript_with_a_path_still_reads_through_pib(self):
        completed = sh2pil_open.subprocess.CompletedProcess(['ssh'], 0, b'## user\n\nhi\n', b'')
        with patch.object(sh2pil_open.subprocess, 'run', return_value=completed) as run:
            text = sh2pil_open.session_remote_show('build-host', 'aaa', 'claude', 400,
                                                '/srv/api/store/aaa.jsonl')
        self.assertEqual(text, '## user\n\nhi\n')
        remote = run.call_args.args[0][10]
        # The path cannot be read by sh2pil-last, so the read resolves the session on its host.
        self.assertIn('show aaa --harness claude --tail 400', remote)
        self.assertNotIn('sh2pil-last', remote)


class LoginShellTest(unittest.TestCase):
    """The shell that runs a tool command and opens a new window.

    Nothing here may require a particular shell to be installed: a machine with no zsh must
    still open a window, and the reader's own shell is the one they expect to get back.
    """

    def test_the_readers_own_shell_is_used_when_it_exists(self):
        with patch.dict(os.environ, {'SHELL': '/bin/sh'}):
            self.assertEqual(sh2pil_open.login_shell(), '/bin/sh')

    def test_a_shell_that_is_not_installed_is_not_named(self):
        with patch.dict(os.environ, {'SHELL': '/bin/no-such-shell'}):
            chosen = sh2pil_open.login_shell()
        self.assertNotEqual(chosen, '/bin/no-such-shell')
        self.assertTrue(os.access(chosen, os.X_OK))

    def test_without_shell_in_the_environment_the_passwd_entry_answers(self):
        with patch.dict(os.environ, {}, clear=True), \
             patch.object(sh2pil_open.pwd, 'getpwuid', return_value=Mock(pw_shell='/bin/sh')):
            self.assertEqual(sh2pil_open.login_shell(), '/bin/sh')

    def test_a_passwd_entry_that_cannot_be_read_still_answers(self):
        with patch.dict(os.environ, {}, clear=True), \
             patch.object(sh2pil_open.pwd, 'getpwuid', side_effect=KeyError('no entry')):
            self.assertTrue(os.access(sh2pil_open.login_shell(), os.X_OK))

    def test_the_resolved_shell_is_installed(self):
        # SHELL is resolved once, at import, and the launchers use it directly.
        self.assertTrue(os.access(sh2pil_open.SHELL, os.X_OK))
