"""Regression tests for the target one kitty window acts on, which the cmd+. key asks for."""
import contextlib
import importlib.machinery
import importlib.util
import io
import pathlib
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = pathlib.Path(__file__).resolve().parents[1] / 'sh2pil-open'
# Loading the helper from its source path writes a bytecode cache beside it, which would land in
# the working tree as an untracked directory.  Compile nothing.
sys.dont_write_bytecode = True
loader = importlib.machinery.SourceFileLoader('sh2pil_open_window', str(SCRIPT))
spec = importlib.util.spec_from_loader(loader.name, loader)
sh2pil_open = importlib.util.module_from_spec(spec)
loader.exec_module(sh2pil_open)


def window(commands, cwd='/', title='', identifier='7'):
    return {'medium': 'kitty', 'label': f'kitty window {identifier}', 'target': identifier,
            'title': title, 'cwd': cwd, 'pids': [1], 'commands': commands,
            'prefix': ['kitten', '@']}


def ssh_window(destination, remote_command, cwd='/', title=''):
    return window([['ssh', '-S', '/tmp/master', '-o', 'ControlMaster=no', '-t', destination,
                    remote_command]], cwd=cwd, title=title)


class ZmxNameTest(unittest.TestCase):
    """Which zmx session a window holds a client for, local or over SSH."""

    def test_a_local_client_names_its_session(self):
        row = window([['env', '-u', 'ZMX_SESSION', 'PIB_ZMX=/opt/homebrew/bin/zmx',
                       '/opt/homebrew/bin/zmx', 'attach', 'pi-abc-1']])
        self.assertEqual(sh2pil_open.window_zmx_name(row), 'pi-abc-1')

    def test_a_new_release_client_names_its_session_too(self):
        row = window([['env', '-u', 'ZMX_SESSION', 'SH2PIL_ZMX=/opt/homebrew/bin/zmx',
                       'PIB_ZMX=/opt/homebrew/bin/zmx', '/opt/homebrew/bin/zmx',
                       'attach', 'pi-abc-1']])
        self.assertEqual(sh2pil_open.window_zmx_name(row), 'pi-abc-1')

    def test_a_remote_client_names_its_session_from_the_ssh_command(self):
        row = ssh_window('host', 'env -u ZMX_SESSION PIB_ZMX=/usr/bin/zmx zmx attach feat-2')
        self.assertEqual(sh2pil_open.window_zmx_name(row), 'feat-2')

    def test_a_plain_window_names_nothing(self):
        self.assertEqual(sh2pil_open.window_zmx_name(window([['/bin/zsh']])), '')
        self.assertEqual(sh2pil_open.window_zmx_name({'commands': []}), '')

    def test_the_client_path_is_not_read_as_a_session(self):
        row = window([['env', 'PIB_ZMX=/usr/bin/zmx', '/bin/zsh']])
        self.assertEqual(sh2pil_open.window_zmx_name(row), '')

    def test_a_session_created_with_labels_names_the_session_not_the_option(self):
        # `zmx attach --labels kv <name> <cmd>` is how this program starts a chat in a new
        # session, so the word after `attach` is the option and the name follows its value.
        row = window([['/opt/homebrew/bin/zmx', 'attach', '--labels', 'project=repo', 'pi-repo',
                       '/bin/sh', '-lic', 'pi']])
        self.assertEqual(sh2pil_open.window_zmx_name(row), 'pi-repo')

    def test_the_label_value_may_hold_several_pairs(self):
        row = window([['/opt/homebrew/bin/zmx', 'attach', '--labels',
                       'project=repo pi=abc-1', 'pi-repo', '/bin/sh', '-lic', 'pi']])
        self.assertEqual(sh2pil_open.window_zmx_name(row), 'pi-repo')

    def test_a_remote_session_created_with_labels_names_its_session(self):
        row = ssh_window('host', 'cd -- /srv/repo && env -u ZMX_SESSION '
                                 'SH2PIL_ZMX=/usr/bin/zmx zmx attach --labels project=repo '
                                 'pi-repo /bin/sh -c pi')
        self.assertEqual(sh2pil_open.window_zmx_name(row), 'pi-repo')

    def test_attach_with_labels_and_no_name_names_nothing(self):
        row = window([['/opt/homebrew/bin/zmx', 'attach', '--labels', 'project=repo']])
        self.assertEqual(sh2pil_open.window_zmx_name(row), '')


class ConfiguredServersTest(unittest.TestCase):
    """The hosts the picker reads, which are the only ones a window may point at."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.config = pathlib.Path(self.tmp.name) / 'config.yaml'
        self.patch = patch.object(sh2pil_open, 'CONFIG', self.config)
        self.patch.start()
        self.addCleanup(self.patch.stop)

    def test_commas_and_spaces_both_separate(self):
        self.config.write_text('zmx_servers: user@192.0.2.15, buildbox\n')
        self.assertEqual(sh2pil_open.configured_servers(),
                         ['user@192.0.2.15', 'buildbox'])

    def test_no_setting_is_no_host(self):
        self.config.write_text('default_view: sessions\n')
        self.assertEqual(sh2pil_open.configured_servers(), [])


class WindowTargetTest(unittest.TestCase):
    """The host and project one window's menu applies to, and the refusals."""

    def target(self, row, servers=('user@192.0.2.15',), zmx=None, remote=None, stores=None):
        with patch.object(sh2pil_open, 'kitten_prefix', return_value=['kitten', '@']), \
                patch.object(sh2pil_open, 'kitty_windows', return_value=[row]), \
                patch.object(sh2pil_open, 'configured_servers', return_value=list(servers)), \
                patch.object(sh2pil_open, 'zmx_sessions', return_value=zmx or []), \
                patch.object(sh2pil_open, 'zmx_remote_sessions', return_value=remote or []), \
                patch.object(sh2pil_open, 'session_remote_list', return_value=remote or []), \
                patch.object(sh2pil_open, 'target_stores', return_value=stores or []):
            return sh2pil_open.window_target(row['target'])

    def test_a_local_window_uses_its_own_directory(self):
        target = self.target(window([['/bin/zsh']], cwd='/Users/me/repo'))
        self.assertEqual(target,
                         {'server': '', 'cwd': '/Users/me/repo', 'project': 'repo',
                          'stores': []})

    def test_a_host_window_takes_the_directory_of_its_zmx_session(self):
        # The window's own directory is where its ssh client started.  The session's directory
        # is the one the reader is working in, so the session wins.
        row = ssh_window('user@192.0.2.15', '/usr/bin/zmx attach pi-x', cwd='/Users/me')
        target = self.target(row,
                             remote=[{'name': 'pi-x', 'cwd': '/home/me/buildbox'}],
                             stores=['pi', 'claude'])
        self.assertEqual(target['server'], 'user@192.0.2.15')
        self.assertEqual(target['cwd'], '/home/me/buildbox')
        self.assertEqual(target['project'], 'buildbox')
        self.assertEqual(target['stores'], ['pi', 'claude'])

    def test_a_host_window_without_zmx_is_placed_by_the_name_in_its_title(self):
        row = ssh_window('user@192.0.2.15', 'bash -lc pi', title='Fix ingress docs')
        remote = [{'id': 'a', 'name': 'Fix ingress docs', 'cwd': '/home/me/infra',
                   'modified': 1},
                  {'id': 'b', 'name': 'Fix ingress docs', 'cwd': '/home/me/older',
                   'modified': 0}]
        target = self.target(row, remote=remote)
        self.assertEqual(target['cwd'], '/home/me/infra')

    def test_a_host_window_in_a_labelled_session_is_placed_by_that_session(self):
        # The regression: a chat this program starts lives in a session created with labels,
        # so its window must be placed by the session's directory even when its title names no
        # session on the host yet.
        row = ssh_window('user@192.0.2.15',
                         'env -u ZMX_SESSION SH2PIL_ZMX=/usr/bin/zmx zmx attach '
                         '--labels project=repo pi-repo /bin/sh -c pi', cwd='/Users/me')
        target = self.target(row, remote=[{'name': 'pi-repo', 'cwd': '/home/me/repo'}])
        self.assertEqual(target['server'], 'user@192.0.2.15')
        self.assertEqual(target['cwd'], '/home/me/repo')
        self.assertEqual(target['project'], 'repo')

    def test_a_host_window_with_pi_s_own_title_is_placed_by_the_middle_part(self):
        # Pi's own title is `<app> - <name> - <project>`, and the name is the middle part: a
        # window a reader opened by hand carries the whole title, not the name alone.
        row = ssh_window('user@192.0.2.15', 'bash -lc pi',
                         title='π - Fix ingress docs - sh2pil')
        remote = [{'id': 'a', 'name': 'Fix ingress docs', 'cwd': '/home/me/infra',
                   'modified': 1}]
        target = self.target(row, remote=remote)
        self.assertEqual(target['cwd'], '/home/me/infra')
        self.assertEqual(target['project'], 'infra')

    def test_a_host_window_that_resumes_a_session_is_placed_by_its_id(self):
        # The picker writes the ssh command line, so a resumed chat names its session there
        # even when the title has not caught up.
        row = ssh_window('user@192.0.2.15',
                         'cd -- /home/me/infra && { [ -x /bin/zsh ] && exec /bin/zsh -lic '
                         "'pi --session ses_abc'; exec sh -c 'pi --session ses_abc'; }",
                         title='pi')
        remote = [{'id': 'ses_abc', 'name': 'Unnamed', 'cwd': '/home/me/infra', 'modified': 1}]
        target = self.target(row, remote=remote)
        self.assertEqual(target['cwd'], '/home/me/infra')

    def test_a_host_window_that_names_only_its_directory_is_placed_by_it(self):
        # A chat the host has not recorded yet still names the project it runs in, because
        # the picker wrote `cd -- <directory>` in front of it.
        row = ssh_window('user@192.0.2.15',
                         'cd -- /home/me/newproj && { [ -x /bin/zsh ] && exec /bin/zsh -lic '
                         "'pi'; exec sh -c 'pi'; }", title='pi')
        target = self.target(row)
        self.assertEqual(target['cwd'], '/home/me/newproj')

    def test_a_host_outside_the_setting_is_refused(self):
        row = ssh_window('elsewhere', 'bash -lc pi')
        with self.assertRaises(RuntimeError) as caught:
            self.target(row)
        self.assertIn('not a sh2pil-sessions target', str(caught.exception))

    def test_a_host_window_this_cannot_place_is_refused(self):
        row = ssh_window('user@192.0.2.15', 'bash -lc tmux attach', cwd='/Users/me',
                         title='tmux')
        with self.assertRaises(RuntimeError) as caught:
            self.target(row)
        self.assertIn('cannot find the project', str(caught.exception))

    def test_a_local_window_with_no_directory_is_refused(self):
        with self.assertRaises(RuntimeError) as caught:
            self.target(window([['/bin/zsh']], cwd=''))
        self.assertIn('cannot find the project', str(caught.exception))

    def test_no_kitty_is_refused(self):
        with patch.object(sh2pil_open, 'kitten_prefix', return_value=None):
            with self.assertRaises(RuntimeError) as caught:
                sh2pil_open.window_target('7')
        self.assertIn('no kitty', str(caught.exception))


class WindowMenuCommandTest(unittest.TestCase):
    """What window-menu hands the picker."""

    def test_the_resolved_target_travels_to_the_picker(self):
        target = {'server': 'user@192.0.2.15', 'cwd': '/home/me/buildbox',
                  'project': 'buildbox', 'stores': ['pi', 'claude']}
        printed = []
        with patch.object(sh2pil_open, 'window_target', return_value=target), \
                patch.object(sh2pil_open, 'picker', return_value=pathlib.Path('/x/sh2pil')), \
                patch('sys.stdout') as out:
            self.assertEqual(sh2pil_open.window_menu('7', emit=True), 0)
            printed = ''.join(call.args[0] for call in out.write.call_args_list)
        self.assertIn('--menu-cwd /home/me/buildbox', printed)
        self.assertIn('--menu-server user@192.0.2.15', printed)
        self.assertIn('--menu-stores pi,claude', printed)

    def test_a_target_that_cannot_be_read_is_reported_here(self):
        with patch.object(sh2pil_open, 'window_target',
                          side_effect=RuntimeError('no kitty is running')), \
                patch.object(sh2pil_open, 'fail', return_value=1) as failed:
            self.assertEqual(sh2pil_open.window_menu('7'), 1)
        self.assertIn('no kitty is running', failed.call_args.args[0])


class WindowSessionTest(unittest.TestCase):
    """The host and the session one window's chat runs, which the f3 and f4 keys ask for."""

    def resolve(self, row, harness='pi', servers=('user@192.0.2.15',), remote=None, zmx=None):
        with patch.object(sh2pil_open, 'kitten_prefix', return_value=['kitten', '@']), \
                patch.object(sh2pil_open, 'kitty_windows', return_value=[row]), \
                patch.object(sh2pil_open, 'configured_servers', return_value=list(servers)), \
                patch.object(sh2pil_open, 'session_remote_list', return_value=remote or []) as read, \
                patch.object(sh2pil_open, 'zmx_remote_sessions', return_value=zmx or []):
            answer = sh2pil_open.window_session(row['target'], harness)
        return answer, read

    def pi_row(self, identifier='a', name='', cwd='/home/me/infra', modified=1):
        return {'id': identifier, 'harness': 'pi', 'name': name, 'cwd': cwd,
                'modified': modified, 'file': f'/home/me/.pi/agent/sessions/x/{identifier}.jsonl'}

    def claude_row(self, identifier='c1', cwd='/srv/api', modified=1):
        return {'id': identifier, 'harness': 'claude', 'name': '', 'cwd': cwd,
                'modified': modified,
                'file': f'/home/me/.claude/projects/-srv-api/{identifier}.jsonl'}

    def codex_row(self, identifier='x1', cwd='/srv/api', modified=1):
        return {'id': identifier, 'harness': 'codex', 'name': '', 'cwd': cwd,
                'modified': modified,
                'file': f'/home/me/.codex/sessions/2026/{identifier}.jsonl'}

    def opened(self, destination, cwd, identifier, store, fork=False, name=''):
        """Return the ssh command line the picker runs to open one chat on a host.

        The line is built by the helper itself, so these tests read the shape the picker
        really produces, environment exports and login snippet included.
        """
        with patch.object(sh2pil_open, 'config_entries',
                          return_value=[('ssh_env', 'TERM=xterm-256color')]):
            return sh2pil_open.session_remote_argv(destination, cwd, identifier, store, fork, name)

    def test_a_window_on_a_configured_host_names_its_session(self):
        row = ssh_window('user@192.0.2.15', 'bash -lc pi', title='Fix ingress docs')
        answer, read = self.resolve(row, remote=[self.pi_row('b', 'Fix ingress docs',
                                                            '/home/me/older', 0),
                                                self.pi_row('a', 'Fix ingress docs',
                                                            '/home/me/infra', 1)])
        self.assertEqual(answer['host'], 'user@192.0.2.15')
        self.assertEqual(answer['session'], 'a')
        self.assertEqual(answer['cwd'], '/home/me/infra')
        self.assertEqual(answer['file'],
                         '/home/me/.pi/agent/sessions/x/a.jsonl')
        self.assertEqual(read.call_args.args[:2], ('user@192.0.2.15', 'pi'))

    def test_a_title_pis_own_title_names_the_session_too(self):
        row = ssh_window('user@192.0.2.15', 'bash -lc pi',
                         title='\u03c0 - Fix ingress docs - infra')
        answer, _ = self.resolve(row, remote=[self.pi_row('a', 'Fix ingress docs')])
        self.assertEqual(answer['session'], 'a')

    def test_a_window_the_picker_did_not_label_is_placed_by_its_zmx_session(self):
        row = ssh_window('user@192.0.2.15', 'zmx attach pi-x', title='claude')
        remote = [{'id': 'c1', 'harness': 'claude', 'name': '', 'cwd': '/srv/api',
                   'modified': 2, 'file': '/home/me/.claude/projects/-srv-api/c1.jsonl'},
                  {'id': 'c2', 'harness': 'claude', 'name': '', 'cwd': '/srv/other',
                   'modified': 3, 'file': '/home/me/.claude/projects/-srv-other/c2.jsonl'}]
        answer, read = self.resolve(row, harness='claude', remote=remote,
                                    zmx=[{'name': 'pi-x', 'cwd': '/srv/api'}])
        self.assertEqual(answer['session'], 'c1')
        self.assertEqual(read.call_args.args[:2], ('user@192.0.2.15', 'claude'))

    def test_a_local_window_has_no_host(self):
        answer, read = self.resolve(window([['pi']], cwd='/Users/me/repo'))
        self.assertEqual(answer, {'host': ''})
        read.assert_not_called()

    def test_a_host_outside_the_setting_has_no_host(self):
        row = ssh_window('elsewhere', 'bash -lc pi')
        answer, read = self.resolve(row)
        self.assertEqual(answer, {'host': ''})
        read.assert_not_called()

    def test_no_kitty_and_an_unknown_window_have_no_host(self):
        with patch.object(sh2pil_open, 'kitten_prefix', return_value=None):
            self.assertEqual(sh2pil_open.window_session('7'), {'host': ''})
        row = ssh_window('user@192.0.2.15', 'bash -lc pi')
        with patch.object(sh2pil_open, 'kitten_prefix', return_value=['kitten', '@']), \
                patch.object(sh2pil_open, 'kitty_windows', return_value=[row]):
            self.assertEqual(sh2pil_open.window_session('999999'), {'host': ''})

    def test_a_configured_host_that_names_no_session_is_refused(self):
        row = ssh_window('user@192.0.2.15', 'bash -lc tmux attach', title='tmux')
        with self.assertRaises(RuntimeError) as caught:
            self.resolve(row, remote=[self.pi_row('a', 'something else', '/elsewhere')])
        self.assertIn('cannot find the session of window 7 on user@192.0.2.15',
                      str(caught.exception))

    def test_a_pi_chat_is_named_by_the_command_line_that_resumed_it(self):
        # A title can name two chats in one project; the command the window runs there names
        # one id, so it wins over the title.
        row = window([self.opened('user@192.0.2.15', '/home/me/infra', 'a', 'pi')],
                     title='something else')
        answer, _ = self.resolve(row, remote=[self.pi_row('b', 'Fix ingress docs',
                                                         '/home/me/infra', 9),
                                              self.pi_row('a', 'Fix ingress docs')])
        self.assertEqual(answer['session'], 'a')

    def test_a_resumed_claude_chat_is_read_as_claude_and_named_by_its_id(self):
        # The picker opens a remote Claude Code resume as one ssh command that runs
        # `claude --resume <id>` in the session's own directory, and Claude Code publishes no
        # name at all, so that command line is the one thing that names the chat.
        row = window([self.opened('user@192.0.2.15', '/srv/api', 'c1', 'claude')],
                     title='claude api')
        answer, read = self.resolve(row, remote=[self.claude_row('c2', modified=2),
                                                self.claude_row('c1', modified=1)])
        self.assertEqual(answer['harness'], 'claude')
        self.assertEqual(answer['session'], 'c1')
        self.assertEqual(answer['file'], '/home/me/.claude/projects/-srv-api/c1.jsonl')
        self.assertEqual(read.call_args.args[:2], ('user@192.0.2.15', 'claude'))

    def test_a_forked_claude_chat_is_placed_by_the_directory_it_runs_in(self):
        # A copy runs under an id of its own, so the id on the line names the chat it was
        # copied from: the newest row of that directory is the copy the window shows.
        row = window([self.opened('user@192.0.2.15', '/srv/api', 'c1', 'claude',
                                  fork=True, name='api copy')], title='claude api')
        answer, _ = self.resolve(row, remote=[self.claude_row('c1', modified=1),
                                             self.claude_row('c9', modified=9)])
        self.assertEqual(answer['session'], 'c9')

    def test_a_resumed_codex_chat_names_its_own_id(self):
        row = window([self.opened('user@192.0.2.15', '/srv/api', 'x2', 'codex')],
                     title='codex api')
        answer, read = self.resolve(row, remote=[self.codex_row('x1', modified=9),
                                                self.codex_row('x2', modified=1)])
        self.assertEqual(answer['harness'], 'codex')
        self.assertEqual(answer['session'], 'x2')
        self.assertEqual(read.call_args.args[:2], ('user@192.0.2.15', 'codex'))

    def test_a_new_codex_chat_is_placed_by_the_directory_it_runs_in(self):
        row = window([self.opened('user@192.0.2.15', '/srv/api', '', 'codex')],
                     title='codex api')
        answer, _ = self.resolve(row, remote=[self.codex_row('x1', '/srv/other', 9),
                                             self.codex_row('x2', '/srv/api', 1)])
        self.assertEqual(answer['harness'], 'codex')
        self.assertEqual(answer['session'], 'x2')

    def test_a_chat_whose_store_has_no_reader_here_is_refused(self):
        row = window([self.opened('user@192.0.2.15', '/srv/api', 'o1', 'opencode')],
                     title='opencode o1')
        with self.assertRaises(RuntimeError) as caught:
            self.resolve(row)
        self.assertIn('runs opencode, which has no transcript reader here',
                      str(caught.exception))

    def test_a_plain_ssh_window_says_nothing_about_its_chat(self):
        row = window([['ssh', '-t', 'user@192.0.2.15']], title='host')
        self.assertEqual(sh2pil_open.window_remote_chat(row),
                         {'store': '', 'directory': '', 'session': '', 'fork': False})

    def test_a_session_with_no_transcript_path_is_refused(self):
        row = ssh_window('user@192.0.2.15', 'bash -lc pi', title='Fix ingress docs')
        row_without_a_file = self.pi_row('a', 'Fix ingress docs')
        row_without_a_file['file'] = ''
        with self.assertRaises(RuntimeError) as caught:
            self.resolve(row, remote=[row_without_a_file])
        self.assertIn('reports no transcript path', str(caught.exception))

    def test_an_unknown_store_is_refused(self):
        with self.assertRaises(ValueError):
            sh2pil_open.window_session('7', 'opencode')

    def test_the_json_answer_carries_the_host_and_the_session(self):
        row = ssh_window('user@192.0.2.15', 'bash -lc pi', title='Fix ingress docs')
        written = {}
        with patch.object(sh2pil_open, 'window_session',
                          return_value={'host': 'user@192.0.2.15', 'harness': 'pi',
                                        'session': 'a', 'file': '/x/a.jsonl', 'cwd': '/srv',
                                        'name': 'Fix ingress docs'}), \
                patch.object(sh2pil_open.json, 'dump',
                             side_effect=lambda value, *a, **k: written.update(value)), \
                patch.object(sh2pil_open, 'fail', return_value=1) as failed:
            self.assertEqual(sh2pil_open.print_window_session('7', 'pi', True), 0)
        failed.assert_not_called()
        self.assertEqual(written['host'], 'user@192.0.2.15')
        self.assertEqual(written['file'], '/x/a.jsonl')

    def test_a_refusal_is_one_line_through_fail(self):
        with patch.object(sh2pil_open, 'window_session',
                          side_effect=RuntimeError('could not read host')), \
                patch.object(sh2pil_open, 'fail', return_value=1) as failed:
            self.assertEqual(sh2pil_open.print_window_session('7', 'pi'), 1)
        self.assertEqual(failed.call_args.args[0], 'sh2pil-open: could not read host')


class RemotePruneTest(unittest.TestCase):
    """The age-based delete runs on the host that holds the store."""

    def completed(self, stdout=b'{}', stderr=b'', returncode=0):
        return subprocess.CompletedProcess([], returncode, stdout, stderr)

    def call(self, result, *arguments):
        with patch.object(sh2pil_open, 'remote_helper_read', return_value=result) as read, \
                patch.object(sh2pil_open, 'sessions_helper', return_value=pathlib.Path('/sh2pil-sessions')):
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                code = sh2pil_open.main(list(arguments))
        return code, out.getvalue(), read

    def test_the_age_and_the_flags_travel_to_the_host(self):
        # The age is resolved by the host's own store, so every flag the picker decided on
        # travels with it, and the JSON the host answers comes straight back to the picker.
        code, out, read = self.call(self.completed(b'{"sessions": []}'),
                                    'session-remote-prune', 'host',
                                    '--older-than', '1d3h', '--zmx', '--yes')
        self.assertEqual(code, 0)
        self.assertEqual(out, '{"sessions": []}')
        _server, name, arguments, _helper = read.call_args.args
        self.assertEqual(name, 'sh2pil-sessions')
        self.assertEqual(arguments, ['prune', '--older-than', '1d3h', '--harness', 'all',
                                     '--json', '--zmx', '--yes'])

    def test_a_read_carries_no_yes(self):
        # The question the picker asks is the same call without --yes: a read that deleted
        # would make the count meaningless and the confirmation a formality.
        _code, _out, read = self.call(self.completed(b'{"sessions": []}'),
                                      'session-remote-prune', 'host', '--older-than', '1d')
        _server, _name, arguments, _helper = read.call_args.args
        self.assertNotIn('--yes', arguments)
        self.assertNotIn('--zmx', arguments)

    def test_a_failed_prune_is_reported_through_fail(self):
        with patch.object(sh2pil_open, 'remote_helper_read',
                          return_value=self.completed(b'', b'sh2pil-sessions: no store\n', 1)), \
                patch.object(sh2pil_open, 'sessions_helper', return_value=pathlib.Path('/sh2pil-sessions')), \
                patch.object(sh2pil_open, 'fail', return_value=1) as failed:
            code = sh2pil_open.main(['session-remote-prune', 'host', '--older-than', '1d'])
        self.assertEqual(code, 1)
        self.assertEqual(failed.call_args.args[0],
                         'sh2pil-open: could not prune host: sh2pil-sessions: no store')


if __name__ == '__main__':
    unittest.main()
