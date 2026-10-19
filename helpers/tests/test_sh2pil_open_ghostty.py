"""Tests for the Ghostty medium: detection, the surface list, focus, and launch.

Ghostty reports no pid, no tty, and no command line for a surface, so nothing here is
matched by process.  Every test runs without Ghostty installed or running: the AppleScript
answers are recorded strings.
"""
import importlib.machinery
import importlib.util
import io
import json
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
loader = importlib.machinery.SourceFileLoader('sh2pil_open_ghostty', str(SCRIPT))
spec = importlib.util.spec_from_loader(loader.name, loader)
sh2pil_open = importlib.util.module_from_spec(spec)
loader.exec_module(sh2pil_open)


def result(code=0, stdout=b'', stderr=b''):
    return subprocess.CompletedProcess(args=[], returncode=code, stdout=stdout, stderr=stderr)


def row(identifier='AAA-BBB', name='~/proj', cwd='/Users/me/proj'):
    return {'medium': 'ghostty', 'label': f'ghostty terminal {identifier}',
            'target': identifier, 'title': name, 'cwd': cwd, 'pids': [], 'commands': [],
            'prefix': ['osascript'], 'window': 'tab-group-1', 'tab': 'tab-1'}


class GhosttyPrefixTest(unittest.TestCase):
    """Which Ghostty this process may drive, and the proof that it never starts one."""

    def test_another_platform_is_not_a_medium(self):
        with patch.object(sh2pil_open.sys, 'platform', 'linux'), \
                patch.object(sh2pil_open.shutil, 'which', return_value='/usr/bin/osascript'), \
                patch.object(sh2pil_open.subprocess, 'run') as run:
            self.assertIsNone(sh2pil_open.ghostty_prefix())
        run.assert_not_called()

    def test_no_osascript_is_not_a_medium(self):
        with patch.object(sh2pil_open.sys, 'platform', 'darwin'), \
                patch.object(sh2pil_open.shutil, 'which', return_value=None), \
                patch.object(sh2pil_open.subprocess, 'run') as run:
            self.assertIsNone(sh2pil_open.ghostty_prefix())
        run.assert_not_called()

    def test_a_running_ghostty_is_the_medium(self):
        with patch.object(sh2pil_open.sys, 'platform', 'darwin'), \
                patch.object(sh2pil_open.shutil, 'which', return_value='/usr/bin/osascript'), \
                patch.object(sh2pil_open.subprocess, 'run',
                             return_value=result(stdout=b'true\n')):
            self.assertEqual(sh2pil_open.ghostty_prefix(), ['osascript'])

    def test_a_closed_ghostty_is_asked_but_never_started(self):
        # `tell application "Ghostty"` would launch the app; `is running` does not.
        with patch.object(sh2pil_open.sys, 'platform', 'darwin'), \
                patch.object(sh2pil_open.shutil, 'which', return_value='/usr/bin/osascript'), \
                patch.object(sh2pil_open.subprocess, 'run',
                             return_value=result(stdout=b'false\n')) as run:
            self.assertIsNone(sh2pil_open.ghostty_prefix())
        asked = run.call_args.args[0]
        self.assertIn('application "Ghostty" is running', asked)
        self.assertNotIn('tell application', ' '.join(asked))

    def test_a_refused_asking_is_not_a_medium(self):
        with patch.object(sh2pil_open.sys, 'platform', 'darwin'), \
                patch.object(sh2pil_open.shutil, 'which', return_value='/usr/bin/osascript'), \
                patch.object(sh2pil_open.subprocess, 'run',
                             return_value=result(code=1, stderr=b'boom')):
            self.assertIsNone(sh2pil_open.ghostty_prefix())

    def test_an_app_that_does_not_answer_isleft_alone(self):
        # A killed AppleScript client can wedge Ghostty's window provider; a helper that
        # waits forever would hang the key binding that started it.
        with patch.object(sh2pil_open.sys, 'platform', 'darwin'), \
                patch.object(sh2pil_open.shutil, 'which', return_value='/usr/bin/osascript'), \
                patch.object(sh2pil_open.subprocess, 'run',
                             side_effect=subprocess.TimeoutExpired('osascript', 10)):
            self.assertIsNone(sh2pil_open.ghostty_prefix())


class GhosttyTerminalsTest(unittest.TestCase):
    """The surface list, which is the only way a Ghostty surface is named."""

    ROWS = [
        {'window': 'tab-group-1', 'tab': 'tab-1', 'terminal': 'AAA',
         'name': '~/proj', 'cwd': '/Users/me/proj'},
        {'window': 'tab-group-1', 'tab': 'tab-2', 'terminal': 'BBB',
         'name': 'chat', 'cwd': '/Users/me/other project'},
    ]

    def list_with(self, answer):
        with patch.object(sh2pil_open, 'ghostty_prefix', return_value=['osascript']), \
                patch.object(sh2pil_open.subprocess, 'run', return_value=answer):
            return sh2pil_open.ghostty_terminals()

    def test_one_row_per_surface_with_the_kitty_shape(self):
        rows = self.list_with(result(stdout=json.dumps(self.ROWS).encode()))
        self.assertEqual([one['target'] for one in rows], ['AAA', 'BBB'])
        first = rows[0]
        self.assertEqual(first['medium'], 'ghostty')
        self.assertEqual(first['label'], 'ghostty terminal AAA')
        self.assertEqual(first['title'], '~/proj')
        self.assertEqual(first['cwd'], '/Users/me/proj')
        self.assertEqual(first['prefix'], ['osascript'])
        self.assertEqual((first['window'], first['tab']), ('tab-group-1', 'tab-1'))
        # The interface has neither, and every caller treats both as optional.
        self.assertEqual(first['pids'], [])
        self.assertEqual(first['commands'], [])

    def test_a_directory_with_a_space_survives(self):
        rows = self.list_with(result(stdout=json.dumps(self.ROWS).encode()))
        self.assertEqual(rows[1]['cwd'], '/Users/me/other project')

    def test_an_empty_instance_is_empty_not_an_error(self):
        self.assertEqual(self.list_with(result(stdout=b'[]')), [])

    def test_an_empty_answer_is_empty(self):
        self.assertEqual(self.list_with(result(stdout=b'')), [])

    def test_malformed_output_is_refused_quietly(self):
        self.assertEqual(self.list_with(result(stdout=b'not json')), [])

    def test_a_failure_is_refused_quietly(self):
        self.assertEqual(self.list_with(result(code=1, stderr=b'-1743')), [])

    def test_a_row_without_a_terminal_id_is_skipped(self):
        self.assertEqual(self.list_with(result(stdout=b'[{"window": "w"}]')), [])

    def test_a_list_that_never_answers_is_empty(self):
        with patch.object(sh2pil_open, 'ghostty_prefix', return_value=['osascript']), \
                patch.object(sh2pil_open.subprocess, 'run',
                             side_effect=subprocess.TimeoutExpired('osascript', 10)):
            self.assertEqual(sh2pil_open.ghostty_terminals(), [])

    def test_no_ghostty_means_no_process(self):
        with patch.object(sh2pil_open, 'ghostty_prefix', return_value=None), \
                patch.object(sh2pil_open.subprocess, 'run') as run:
            self.assertEqual(sh2pil_open.ghostty_terminals(), [])
        run.assert_not_called()


class GhosttyFocusTest(unittest.TestCase):
    """Bringing one surface forward, which also brings its window to the front."""

    def test_the_focus_command_names_the_terminal(self):
        argv = sh2pil_open.ghostty_focus_argv(['osascript'], 'AAA')
        self.assertEqual(argv[:2], ['osascript', '-e'])
        self.assertIn('focus (first terminal whose id is "AAA")', argv[2])

    def test_a_ghostty_row_reaches_focus_window(self):
        with patch.object(sh2pil_open.subprocess, 'run', return_value=result()) as run:
            self.assertEqual(sh2pil_open.focus_window(row()), 0)
        argv = run.call_args.args[0]
        self.assertIn('focus (first terminal whose id is "AAA-BBB")', argv[2])

    def test_an_emitted_focus_is_the_osascript_line(self):
        with patch('sys.stdout', new_callable=io.StringIO) as out:
            self.assertEqual(sh2pil_open.focus_window(row(), emit=True), 0)
        self.assertIn('osascript', out.getvalue())
        self.assertIn('AAA-BBB', out.getvalue())

    def test_a_medium_that_cannot_focus_says_so(self):
        with patch.object(sh2pil_open, 'fail', return_value=1) as failed, \
                patch.object(sh2pil_open.subprocess, 'run',
                             return_value=result(code=1, stderr=b'no such terminal')):
            self.assertEqual(sh2pil_open.focus_window(row()), 1)
        self.assertIn('ghostty terminal AAA-BBB', failed.call_args.args[0])
        self.assertIn('no such terminal', failed.call_args.args[0])

    def test_a_focus_that_never_answers_is_reported(self):
        with patch.object(sh2pil_open, 'fail', return_value=1) as failed, \
                patch.object(sh2pil_open.subprocess, 'run',
                             side_effect=subprocess.TimeoutExpired('osascript', 10)):
            self.assertEqual(sh2pil_open.focus_window(row()), 1)
        self.assertIn('no answer', failed.call_args.args[0])


class GhosttyLaunchArgvTest(unittest.TestCase):
    """The AppleScript that creates one surface, for each of the three placements."""

    def script(self, place='tab', label='', cwd='/Users/me/proj',
               argv=None):
        argv = ['pi', '--session', 'abc'] if argv is None else argv
        return sh2pil_open.ghostty_launch_argv(['osascript'], argv, cwd, label, place)[2]

    def test_a_tab_is_created_in_the_front_window(self):
        text = self.script('tab')
        self.assertIn('new tab in front window with configuration cfg', text)
        self.assertIn('set command of cfg to "pi --session abc"', text)
        self.assertIn('set initial working directory of cfg to "/Users/me/proj"', text)

    def test_a_window_is_created_on_its_own(self):
        text = self.script('window')
        self.assertIn('new window with configuration cfg', text)
        self.assertIn('focused terminal of selected tab of created', text)

    def test_a_pane_splits_the_focused_terminal_to_the_right(self):
        text = self.script('pane')
        self.assertIn('split (focused terminal of selected tab of front window) direction '
                      'right with configuration cfg', text)

    def test_a_label_pins_the_tab_title(self):
        text = self.script('tab', label='proj')
        self.assertIn('perform action "set_tab_title:proj" on target', text)

    def test_no_label_leaves_the_title_to_the_program_inside(self):
        self.assertNotIn('set_tab_title', self.script('tab', label=''))

    def test_a_plain_shell_runs_the_login_shell(self):
        text = self.script('tab', argv=[sh2pil_open.SHELL, '-l'])
        self.assertIn('set command of cfg to', text)
        self.assertIn('-l', text)

    def test_a_directory_with_a_space_stays_one_value(self):
        text = self.script('tab', cwd='/Users/me/other project', label='')
        self.assertIn('"/Users/me/other project"', text)

    def test_a_quote_in_the_label_is_escaped(self):
        text = self.script('tab', label='a"b')
        self.assertIn('set_tab_title:a\\"b', text)

    def test_a_newline_in_the_label_is_joined_with_linefeed(self):
        text = self.script('tab', label='one\ntwo')
        self.assertIn('"set_tab_title:one" & linefeed & "two"', text)


class GhosttyLaunchFlowTest(unittest.TestCase):
    """Which medium launch() picks, and what a refusal leaves behind."""

    def launch(self, place='tab', inside=True, emit=False):
        with patch.object(sh2pil_open, 'inside_ghostty', return_value=inside), \
                patch.object(sh2pil_open, 'ghostty_launch', return_value=0) as ghostty, \
                patch.object(sh2pil_open, 'kitten_prefix', return_value=None) as kitten, \
                patch.object(sh2pil_open, 'note_failure'), \
                patch('sys.stdout', new_callable=io.StringIO) as out, \
                patch('sys.stderr', new_callable=io.StringIO) as err:
            code = sh2pil_open.launch(['pi'], '/Users/me/proj', '', place, emit)
        return code, ghostty, kitten, out.getvalue(), err.getvalue()

    def test_inside_ghostty_the_running_app_is_asked_first(self):
        code, ghostty, kitten, _, _ = self.launch()
        self.assertEqual(code, 0)
        ghostty.assert_called_once()
        kitten.assert_not_called()

    def test_outside_ghostty_kitty_is_asked_first(self):
        code, ghostty, kitten, _, _ = self.launch(inside=False)
        self.assertEqual(code, 0)
        kitten.assert_called_once()
        ghostty.assert_called_once()

    def test_emit_inside_ghostty_shows_the_applescript_program(self):
        with patch.object(sh2pil_open, 'inside_ghostty', return_value=True), \
                patch.object(sh2pil_open, 'ghostty_prefix', return_value=['osascript']), \
                patch('sys.stdout', new_callable=io.StringIO) as out:
            self.assertEqual(sh2pil_open.launch(['pi'], '/Users/me/proj', '', 'tab', True), 0)
        printed = out.getvalue()
        self.assertIn('osascript', printed)
        self.assertIn('tell application "Ghostty"', printed)

    def test_emit_outside_ghostty_keeps_the_shell_line(self):
        with patch.object(sh2pil_open, 'inside_ghostty', return_value=False), \
                patch('sys.stdout', new_callable=io.StringIO) as out:
            self.assertEqual(sh2pil_open.launch(['pi'], '/Users/me/proj', '', 'tab', True), 0)
        self.assertIn('cd /Users/me/proj &&', out.getvalue())

    def test_a_refused_script_names_the_permission_and_prints_the_command(self):
        with patch.object(sh2pil_open, 'ghostty_prefix', return_value=['osascript']), \
                patch.object(sh2pil_open.subprocess, 'run',
                             return_value=result(code=1, stderr=b'-1743 not authorized')), \
                patch.object(sh2pil_open, 'note_failure') as logged, \
                patch('sys.stderr', new_callable=io.StringIO) as err:
            self.assertEqual(sh2pil_open.ghostty_launch(['pi'], '/tmp', '', 'tab'), 1)
        self.assertIn('Automation', err.getvalue())
        self.assertIn('Automation', logged.call_args.args[0])

    def test_a_wedged_app_is_named_not_waited_on(self):
        with patch.object(sh2pil_open, 'ghostty_prefix', return_value=['osascript']), \
                patch.object(sh2pil_open.subprocess, 'run',
                             side_effect=subprocess.TimeoutExpired('osascript', 10)), \
                patch.object(sh2pil_open, 'note_failure') as logged, \
                patch('sys.stderr', new_callable=io.StringIO) as err:
            self.assertEqual(sh2pil_open.ghostty_launch(['pi'], '/tmp', '', 'tab'), 1)
        self.assertIn('restart Ghostty', err.getvalue())
        self.assertIn('restart Ghostty', logged.call_args.args[0])

    def test_a_refusal_falls_through_to_the_printed_command(self):
        with patch.object(sh2pil_open, 'inside_ghostty', return_value=True), \
                patch.object(sh2pil_open, 'ghostty_launch', return_value=1), \
                patch.object(sh2pil_open, 'kitten_prefix', return_value=None), \
                patch('sys.stdout', new_callable=io.StringIO) as out, \
                patch('sys.stderr', new_callable=io.StringIO):
            self.assertEqual(sh2pil_open.launch(['pi'], '/Users/me/proj', '', 'tab', False), 0)
        self.assertIn('cd /Users/me/proj &&', out.getvalue())

    def test_no_terminal_at_all_still_names_every_medium_it_tried(self):
        with patch.object(sh2pil_open, 'inside_ghostty', return_value=False), \
                patch.object(sh2pil_open, 'ghostty_launch', return_value=None), \
                patch.object(sh2pil_open, 'kitten_prefix', return_value=None), \
                patch('sys.stdout', new_callable=io.StringIO), \
                patch('sys.stderr', new_callable=io.StringIO) as err:
            self.assertEqual(sh2pil_open.launch(['pi'], '/tmp', '', 'tab', False), 0)
        self.assertIn('no kitty, no tmux, and no Ghostty here', err.getvalue())


class GhosttyOwnerTest(unittest.TestCase):
    """A surface is placed only by an exact id, never by a directory or a process."""

    def entry(self):
        return {'id': 'abc', 'pids': [1], 'cwd': '/Users/me/proj'}

    def test_an_exact_id_places_the_surface(self):
        with patch.object(sh2pil_open, 'session_windows', return_value=[]), \
                patch.object(sh2pil_open, 'ghostty_terminals', return_value=[row()]):
            window, reason, choices = sh2pil_open.owner_window(self.entry(), 'AAA-BBB')
        self.assertIsNotNone(window)
        self.assertEqual(window['medium'], 'ghostty')
        self.assertIn('named this Ghostty terminal', reason)

    def test_an_id_that_matches_nothing_places_nothing(self):
        with patch.object(sh2pil_open, 'session_windows', return_value=[]), \
                patch.object(sh2pil_open, 'ghostty_terminals', return_value=[row()]):
            window, reason, choices = sh2pil_open.owner_window(self.entry(), 'OTHER')
        self.assertIsNone(window)
        self.assertEqual(choices, 0)

    def test_a_chat_with_no_exact_answer_has_no_owner(self):
        with patch.object(sh2pil_open, 'session_windows', return_value=[]), \
                patch.object(sh2pil_open, 'ghostty_terminals', return_value=[row()]) as ghostty, \
                patch.object(sh2pil_open, 'ghostty_terminal_id', return_value=''):
            window, _, choices = sh2pil_open.owner_window(self.entry())
        self.assertIsNone(window)
        self.assertEqual(choices, 0)
        ghostty.assert_called_once()


class CurrentTerminalTest(unittest.TestCase):
    """Which terminal this process runs in, which the four window verbs start from."""

    def resolve(self, env, ghostty=''):
        with patch.dict(sh2pil_open.os.environ, env, clear=True), \
                patch.object(sh2pil_open, 'ghostty_front_terminal', return_value=ghostty):
            return sh2pil_open.current_terminal()

    def test_a_kitty_child_names_its_window_first(self):
        self.assertEqual(self.resolve({'KITTY_WINDOW_ID': '42', 'TERM_PROGRAM': 'ghostty'},
                                      ghostty='AAA'), ('kitty', '42'))

    def test_inside_ghostty_the_front_surface_is_the_answer(self):
        self.assertEqual(self.resolve({'TERM_PROGRAM': 'ghostty'}, ghostty='AAA'),
                         ('ghostty', 'AAA'))

    def test_a_tmux_pane_names_itself(self):
        self.assertEqual(self.resolve({'TMUX': '/tmp/sock', 'TMUX_PANE': '%3'}),
                         ('tmux', '%3'))

    def test_no_terminal_says_so(self):
        self.assertEqual(self.resolve({}), ('', ''))

    def test_an_unnamed_ghostty_falls_through_to_the_pane(self):
        self.assertEqual(self.resolve({'TERM_PROGRAM': 'ghostty', 'TMUX': '/tmp/sock',
                                       'TMUX_PANE': '%3'}, ghostty=''), ('tmux', '%3'))


class TerminalMediumTest(unittest.TestCase):
    """The id shape, which is all a --window argument says about its medium."""

    def test_a_number_is_a_kitty_window(self):
        self.assertEqual(sh2pil_open.terminal_medium('42'), 'kitty')

    def test_a_uuid_is_a_ghostty_surface(self):
        self.assertEqual(sh2pil_open.terminal_medium('B7C4C7CF-9B8B-4F23-96FB-9B98EF4C6DBC'),
                         'ghostty')


class GhosttyTitleServerTest(unittest.TestCase):
    """The one hint a Ghostty surface gives that it may be running ssh."""

    SERVERS = ['user@192.0.2.15', 'buildbox']

    def server(self, title):
        with patch.object(sh2pil_open, 'configured_servers', return_value=self.SERVERS):
            return sh2pil_open.ghostty_title_server(title)

    def test_a_configured_host_in_the_title_is_named(self):
        self.assertEqual(self.server('pi - chat - buildbox'), 'buildbox')

    def test_the_host_part_of_a_destination_is_enough(self):
        self.assertEqual(self.server('192.0.2.15: ~/repo'), 'user@192.0.2.15')

    def test_an_unrelated_title_names_nothing(self):
        self.assertEqual(self.server('~/projects/periphery'), '')
        self.assertEqual(self.server(''), '')

    def test_a_longer_word_is_not_the_host(self):
        self.assertEqual(self.server('buildbox2: ~/repo'), '')


class GhosttyWindowTargetTest(unittest.TestCase):
    """Placing the project of a surface the interface cannot fully describe."""

    def target(self, rows, identifier='AAA-BBB'):
        with patch.object(sh2pil_open, 'ghostty_terminals', return_value=rows), \
                patch.object(sh2pil_open, 'configured_servers', return_value=['buildbox']), \
                patch.object(sh2pil_open, 'target_stores', return_value=['pi']):
            return sh2pil_open.window_target(identifier, 'ghostty')

    def test_a_local_surface_is_placed_by_its_working_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            target = self.target([row(cwd=directory)])
        self.assertEqual(target['cwd'], directory)
        self.assertEqual(target['server'], '')
        self.assertEqual(target['stores'], ['pi'])

    def test_a_title_that_names_a_host_is_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(RuntimeError) as caught:
                self.target([row(cwd=directory, name='buildbox: ~/repo')])
        self.assertIn('no command line', str(caught.exception))

    def test_an_unknown_id_is_refused(self):
        with self.assertRaises(RuntimeError) as caught:
            self.target([])
        self.assertIn('no Ghostty terminal', str(caught.exception))

    def test_a_directory_that_is_not_here_is_refused(self):
        with self.assertRaises(RuntimeError) as caught:
            self.target([row(cwd='/nonexistent/ghostty-probe')])
        self.assertIn('no usable working directory', str(caught.exception))


class GhosttyWindowSessionTest(unittest.TestCase):
    """Which chat a surface shows: never a host, because there is no command line."""

    def session(self, rows):
        with patch.object(sh2pil_open, 'ghostty_terminals', return_value=rows), \
                patch.object(sh2pil_open, 'configured_servers', return_value=['buildbox']):
            return sh2pil_open.window_session('AAA-BBB', 'pi', 'ghostty')

    def test_a_local_surface_claims_no_host(self):
        self.assertEqual(self.session([row()]), {'host': ''})

    def test_a_surface_whose_title_names_a_host_is_refused(self):
        with self.assertRaises(RuntimeError) as caught:
            self.session([row(name='buildbox: ~/repo')])
        self.assertIn('cannot be read', str(caught.exception))

    def test_an_unknown_id_claims_no_host(self):
        self.assertEqual(self.session([]), {'host': ''})


class GhosttyActionTest(unittest.TestCase):
    """Typing into a surface, and the refusal that comes before it."""

    def test_the_paste_action_names_the_surface(self):
        argv = sh2pil_open.ghostty_action_argv(['osascript'], 'paste_from_clipboard', 'AAA')
        self.assertIn('perform action "paste_from_clipboard" on (first terminal whose id is '
                      '"AAA")', argv[2])

    def test_input_text_escapes_a_quote(self):
        argv = sh2pil_open.ghostty_input_argv(['osascript'], '/tmp/a"b.png', 'AAA')
        self.assertIn('input text "/tmp/a\\"b.png"', argv[2])

    def paste(self, image, rows=None, answer=None):
        with patch.object(sh2pil_open, 'ghostty_prefix', return_value=['osascript']), \
                patch.object(sh2pil_open, 'ghostty_terminals',
                             return_value=[row()] if rows is None else rows), \
                patch.object(sh2pil_open, 'configured_servers', return_value=['buildbox']), \
                patch.object(sh2pil_open, 'clipboard_image', return_value=image), \
                patch.object(sh2pil_open, 'note_failure'), \
                patch.object(sh2pil_open, 'fail', return_value=1), \
                patch.object(sh2pil_open.subprocess, 'run',
                             return_value=answer or result()) as run:
            return sh2pil_open.paste_window('AAA-BBB', 'ghostty'), run

    def test_no_image_asks_the_terminal_to_paste(self):
        code, run = self.paste(None)
        self.assertEqual(code, 0)
        self.assertIn('paste_from_clipboard', run.call_args.args[0][2])

    def test_an_image_is_typed_as_its_path(self):
        code, run = self.paste(pathlib.Path('/tmp/shot.png'))
        self.assertEqual(code, 0)
        self.assertIn('input text "/tmp/shot.png"', run.call_args.args[0][2])

    def test_a_host_title_types_nothing(self):
        code, run = self.paste(pathlib.Path('/tmp/shot.png'), rows=[row(name='buildbox: x')])
        self.assertEqual(code, 1)
        run.assert_not_called()

    def test_a_terminal_that_is_gone_types_nothing(self):
        code, run = self.paste(pathlib.Path('/tmp/shot.png'), rows=[])
        self.assertEqual(code, 1)
        run.assert_not_called()

    def test_a_wedged_app_reports_the_wait(self):
        with patch.object(sh2pil_open, 'ghostty_prefix', return_value=['osascript']), \
                patch.object(sh2pil_open, 'ghostty_terminals', return_value=[row()]), \
                patch.object(sh2pil_open, 'clipboard_image', return_value=None), \
                patch.object(sh2pil_open, 'note_failure') as logged, \
                patch.object(sh2pil_open, 'fail', return_value=1) as failed, \
                patch.object(sh2pil_open.subprocess, 'run',
                             side_effect=subprocess.TimeoutExpired('osascript', 10)):
            self.assertEqual(sh2pil_open.paste_window('AAA-BBB', 'ghostty'), 1)
        self.assertIn('restart Ghostty', failed.call_args.args[0])
        self.assertIn('restart Ghostty', logged.call_args.args[0])


class GhosttyRetitleTest(unittest.TestCase):
    """Renaming a surface's tab from the store's name."""

    def test_the_tab_title_is_set_on_the_surface(self):
        entry = {'id': 'abc', 'pids': [1], 'name': 'chat', 'cwd': '/tmp'}
        with patch.object(sh2pil_open, 'live_entry', return_value=entry), \
                patch.object(sh2pil_open, 'owner_window', return_value=(row(), 'exact', 1)), \
                patch('sys.stdout', new_callable=io.StringIO) as out, \
                patch('sys.stderr', new_callable=io.StringIO):
            self.assertEqual(sh2pil_open.retitle_session('abc', 'chat', emit=True), 0)
        self.assertIn('set_tab_title:chat', out.getvalue())


class ProcessTableTest(unittest.TestCase):
    """One call gives every parent and start time the owner walk needs."""

    def test_the_table_reads_parents_and_start_times(self):
        rows = (b'  100     1 Mon Oct 19 10:00:00 2026\n'
                b'  101   100 Mon Oct 19 10:00:05 2026\n')
        with patch.object(sh2pil_open.subprocess, 'run',
                          return_value=result(stdout=rows)) as run:
            table = sh2pil_open.process_table()
        self.assertEqual(table[101], (100, 'Mon Oct 19 10:00:05 2026'))
        self.assertEqual(run.call_args.args[0][:2], ['ps', '-eo'])

    def test_a_failed_read_is_empty(self):
        with patch.object(sh2pil_open.subprocess, 'run', return_value=result(code=1)):
            self.assertEqual(sh2pil_open.process_table(), {})

    def test_ancestors_walk_up_and_stop_at_init(self):
        table = {5: (2, 'a'), 2: (1, 'b'), 1: (0, 'c')}
        self.assertEqual(sh2pil_open.terminal_ancestors(5, table), [5, 2])


class TerminalLinkTest(unittest.TestCase):
    """Which surface a chat came from: a pid link, because the environment is not readable."""

    TABLE = {700: (600, 'Mon Oct 19 12:00:00 2026'),
             600: (500, 'Mon Oct 19 11:00:00 2026'),
             500: (1, '')}

    def links(self, files):
        with tempfile.TemporaryDirectory() as directory:
            pids = pathlib.Path(directory) / 'pids'
            pids.mkdir()
            for pid, text in files.items():
                (pids / str(pid)).write_text(text, encoding='utf-8')
            with patch.object(sh2pil_open, 'TERMINALS_DIR', pathlib.Path(directory)), \
                    patch.object(sh2pil_open, 'process_table', return_value=self.TABLE):
                return sh2pil_open.terminal_links()

    def test_a_link_is_read(self):
        self.assertEqual(self.links({600: 'AAA\tMon Oct 19 11:00:00 2026\n'}),
                         {600: ('AAA', 'Mon Oct 19 11:00:00 2026')})

    def test_a_link_for_a_dead_pid_is_dropped(self):
        self.assertEqual(self.links({999: 'AAA\tMon Oct 19 11:00:00 2026\n'}), {})

    def owner(self, files, pids):
        with tempfile.TemporaryDirectory() as directory:
            directory_path = pathlib.Path(directory)
            (directory_path / 'pids').mkdir()
            for pid, text in files.items():
                (directory_path / 'pids' / str(pid)).write_text(text, encoding='utf-8')
            with patch.object(sh2pil_open, 'TERMINALS_DIR', directory_path), \
                    patch.object(sh2pil_open, 'process_table', return_value=self.TABLE):
                return sh2pil_open.ghostty_terminal_id({'pids': pids})

    def test_the_chat_walks_to_the_surface_it_was_started_in(self):
        # 700 is the chat, 600 the shell the leaf became, and 600 holds the link.
        self.assertEqual(self.owner({600: 'AAA\tMon Oct 19 11:00:00 2026\n'}, [700]), 'AAA')

    def test_a_reused_pid_is_refused(self):
        self.assertEqual(self.owner({600: 'AAA\tMon Oct 19 09:00:00 2026\n'}, [700]), '')

    def test_no_link_is_no_terminal(self):
        self.assertEqual(self.owner({}, [700]), '')

    def test_a_link_is_written_for_the_running_pid(self):
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(sh2pil_open, 'TERMINALS_DIR', pathlib.Path(directory)):
                sh2pil_open.write_terminal_link(4242, 'AAA', 'Mon Oct 19 12:00:00 2026')
            written = (pathlib.Path(directory) / 'pids' / '4242').read_text()
        self.assertEqual(written.split('\t')[0], 'AAA')

class TerminalLeafTest(unittest.TestCase):
    """The marker a new surface waits for, and the command it then becomes."""

    def test_the_marker_is_read_and_removed(self):
        with tempfile.TemporaryDirectory() as directory:
            token = 'a' * 16
            path = pathlib.Path(directory) / token
            path.write_text('AAA-BBB\n', encoding='utf-8')
            with patch.object(sh2pil_open, 'TERMINALS_DIR', pathlib.Path(directory)):
                self.assertEqual(sh2pil_open.terminal_marker(token), 'AAA-BBB')
            self.assertFalse(path.exists())

    def test_a_missing_marker_costs_only_the_wait(self):
        with tempfile.TemporaryDirectory() as directory, \
                patch.object(sh2pil_open, 'TERMINALS_DIR', pathlib.Path(directory)), \
                patch.object(sh2pil_open, 'TERMINAL_WAIT', 0):
            self.assertEqual(sh2pil_open.terminal_marker('b' * 16), '')

    def test_the_command_wraps_the_real_one(self):
        argv = sh2pil_open.terminal_leaf_argv('c' * 16, ['/bin/zsh', '-lic', 'pi'])
        self.assertIn('terminal-leaf', argv)
        self.assertEqual(argv[3:5], ['c' * 16, '--'])
        self.assertEqual(argv[5:], ['/bin/zsh', '-lic', 'pi'])

    def test_a_bad_token_never_runs_the_command(self):
        with patch.object(sh2pil_open, 'fail', return_value=1) as failed, \
                patch.object(sh2pil_open, 'terminal_marker') as marker:
            self.assertEqual(sh2pil_open.terminal_leaf('../escape', ['/bin/true']), 1)
        self.assertIn('token', failed.call_args.args[0])
        marker.assert_not_called()

    def test_a_leaf_without_a_command_is_refused(self):
        with patch.object(sh2pil_open, 'fail', return_value=1) as failed:
            self.assertEqual(sh2pil_open.terminal_leaf('d' * 16, []), 1)
        self.assertIn('command', failed.call_args.args[0])


class TerminalMarkerWriteTest(unittest.TestCase):
    """The creating side: the marker is written while the surface is still starting."""

    def test_the_created_surface_is_marked_before_the_label_is_set(self):
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(sh2pil_open, 'TERMINALS_DIR', pathlib.Path(directory)), \
                    patch.object(sh2pil_open, 'ghostty_prefix', return_value=['osascript']), \
                    patch.object(sh2pil_open.subprocess, 'run',
                                 return_value=result(stdout=b'AAA-BBB\n')) as run:
                self.assertEqual(sh2pil_open.ghostty_launch(['pi'], '/tmp', 'chat', 'tab'), 0)
            script = run.call_args.args[0][2]
            # The marker line comes before the title action: the surface, and the leaf in it,
            # start before the AppleScript answer arrives.
            self.assertLess(script.index('do shell script'), script.index('set_tab_title'))
            self.assertIn(str(pathlib.Path(directory)), script)
            self.assertIn('terminal-leaf', script)
            token = script.split('terminal-leaf ')[1].split(' --')[0]
            self.assertIn(token, script)

    def test_a_refused_launch_reports_the_permission(self):
        with patch.object(sh2pil_open, 'ghostty_prefix', return_value=['osascript']), \
                patch.object(sh2pil_open, 'note_failure'), \
                patch.object(sh2pil_open.subprocess, 'run',
                             return_value=result(code=1, stderr=b'-1743')) as run, \
                patch('sys.stderr', new_callable=io.StringIO) as err:
            self.assertEqual(sh2pil_open.ghostty_launch(['pi'], '/tmp', '', 'tab'), 1)
        self.assertIn('Automation', err.getvalue())

    def test_no_state_directory_means_no_marker_not_no_launch(self):
        with patch.object(sh2pil_open, 'TERMINALS_DIR',
                          pathlib.Path('/nonexistent/definitely/not/here')), \
                patch.object(sh2pil_open, 'ghostty_prefix', return_value=['osascript']), \
                patch.object(sh2pil_open.subprocess, 'run',
                             return_value=result(stdout=b'AAA-BBB\n')) as run:
            self.assertEqual(sh2pil_open.ghostty_launch(['pi'], '/tmp', '', 'tab'), 0)
        self.assertNotIn('do shell script', run.call_args.args[0][2])


class GhosttyOwnerLinkTest(unittest.TestCase):
    """The owner answer: the link first, then one unique title, and nothing else."""

    def entry(self, name='chat', pids=(1,)):
        return {'id': 'abc', 'pids': list(pids), 'cwd': '/Users/me/proj', 'name': name}

    def owner(self, rows, terminal='', name='chat'):
        with patch.object(sh2pil_open, 'ghostty_terminals', return_value=rows), \
                patch.object(sh2pil_open, 'ghostty_terminal_id', return_value=terminal):
            return sh2pil_open.ghostty_owner(self.entry(name=name), rows)

    def test_the_marker_names_the_terminal(self):
        found_row, reason, choices = self.owner([row()], terminal='AAA-BBB')
        self.assertIsNotNone(found_row)
        self.assertIn('carries the id', reason)

    def test_one_matching_title_is_enough(self):
        found = [row(identifier='AAA', name='π - chat - proj')]
        found_row, reason, choices = self.owner(found)
        self.assertIsNotNone(found_row)
        self.assertIn('title', reason)

    def test_two_matching_titles_are_no_match(self):
        found = [row(identifier='AAA', name='π - chat - a'),
                 row(identifier='BBB', name='π - chat - b')]
        found_row, reason, choices = self.owner(found)
        self.assertIsNone(found_row)
        self.assertEqual(choices, 2)

    def test_a_chat_with_no_name_is_never_matched_by_title(self):
        found_row, _, choices = self.owner([row(name='π - other - proj')], name='')
        self.assertIsNone(found_row)
        self.assertEqual(choices, 0)

    def test_the_link_beats_a_title(self):
        found = [row(identifier='AAA', name='π - chat - one'),
                 row(identifier='BBB', name='π - chat - two')]
        found_row, reason, _ = self.owner(found, terminal='BBB')
        self.assertEqual(found_row['target'], 'BBB')
        self.assertIn('carries the id', reason)

    def test_a_marker_for_a_surface_that_is_gone_falls_through(self):
        found_row, _, _ = self.owner([row(identifier='AAA', name='π - chat - one')],
                               {'SH2PIL_TERMINAL_ID': 'GONE'})
        self.assertEqual(found_row['target'], 'AAA')


class OwnerWindowGhosttyTest(unittest.TestCase):
    """focus, switch, and retitle use the same rule as the owner column."""

    def entry(self, name='chat'):
        return {'id': 'abc', 'pids': [1], 'cwd': '/Users/me/proj', 'name': name}

    def test_the_marker_places_the_window_without_a_pid(self):
        with patch.object(sh2pil_open, 'session_windows', return_value=[]), \
                patch.object(sh2pil_open, 'ghostty_terminals', return_value=[row()]), \
                patch.object(sh2pil_open, 'ghostty_terminal_id', return_value='AAA-BBB'):
            window, reason, choices = sh2pil_open.owner_window(self.entry())
        self.assertEqual(window['medium'], 'ghostty')
        self.assertIn('carries the id', reason)

    def test_a_unique_title_places_the_window(self):
        with patch.object(sh2pil_open, 'session_windows', return_value=[]), \
                patch.object(sh2pil_open, 'ghostty_terminals',
                             return_value=[row(name='π - chat - proj')]), \
                patch.object(sh2pil_open, 'ghostty_terminal_id', return_value=''):
            window, reason, _ = sh2pil_open.owner_window(self.entry())
        self.assertIsNotNone(window)
        self.assertIn('title', reason)

    def test_an_ambiguous_title_places_nothing(self):
        rows = [row(identifier='AAA', name='π - chat - a'),
                row(identifier='BBB', name='π - chat - b')]
        with patch.object(sh2pil_open, 'session_windows', return_value=[]), \
                patch.object(sh2pil_open, 'ghostty_terminals', return_value=rows), \
                patch.object(sh2pil_open, 'ghostty_terminal_id', return_value=''):
            window, _, choices = sh2pil_open.owner_window(self.entry())
        self.assertIsNone(window)
        self.assertEqual(choices, 2)


class ZmxExactLinkTest(unittest.TestCase):
    """The marker survives a window that has moved on, because zmx records it at session start."""

    def run_with(self, terminal_id, rows):
        def fake_run(arguments):
            if arguments[:1] != ['print-env']:
                return ''
            return terminal_id + '\n' if arguments[-1] == 'SH2PIL_TERMINAL_ID' else ''
        with patch.object(sh2pil_open, 'zmx_run', side_effect=fake_run), \
                patch.object(sh2pil_open, 'ghostty_terminals', return_value=rows):
            return sh2pil_open.zmx_client_window('pi-abc')

    def test_a_recorded_marker_names_the_surface(self):
        window = self.run_with('AAA-BBB', [row()])
        self.assertIsNotNone(window)
        self.assertEqual(window['target'], 'AAA-BBB')
        self.assertIn('zmx pi-abc', window['label'])

    def test_a_surface_that_is_gone_is_not_focused(self):
        self.assertIsNone(self.run_with('AAA-BBB', []))


class KittenPrefixTest(unittest.TestCase):
    """The kitty probe reads this terminal's own input, so Ghostty is never probed."""

    def test_a_ghostty_terminal_is_never_probed(self):
        with patch.dict(sh2pil_open.os.environ, {'TERM_PROGRAM': 'ghostty'}, clear=True), \
                patch.object(sh2pil_open.subprocess, 'run') as run, \
                patch.object(sh2pil_open.pathlib.Path, 'glob', return_value=[]):
            self.assertIsNone(sh2pil_open.kitten_prefix())
        run.assert_not_called()

    def test_a_kitty_socket_is_still_used_under_ghostty(self):
        # A real file, because the socket list is sorted by mtime.
        with tempfile.TemporaryDirectory() as directory:
            socket = pathlib.Path(directory) / 'kitty-123'
            socket.write_text('', encoding='utf-8')
            with patch.dict(sh2pil_open.os.environ, {'TERM_PROGRAM': 'ghostty'}, clear=True), \
                    patch.object(sh2pil_open.pathlib.Path, 'glob',
                                 return_value=[socket]), \
                    patch.object(sh2pil_open.subprocess, 'run', return_value=result()) as run:
                self.assertEqual(sh2pil_open.kitten_prefix(),
                                 ['kitten', '@', '--to', f'unix:{socket}'])
            self.assertEqual(run.call_args.args[0],
                             ['kitten', '@', '--to', f'unix:{socket}', 'ls'])

    def test_elsewhere_the_controlling_terminal_is_probed(self):
        with patch.dict(sh2pil_open.os.environ, {'TERM_PROGRAM': 'xterm-kitty'}, clear=True), \
                patch.object(sh2pil_open.subprocess, 'run', return_value=result()) as run, \
                patch.object(sh2pil_open.pathlib.Path, 'glob', return_value=[]):
            self.assertEqual(sh2pil_open.kitten_prefix(), ['kitten', '@'])
        self.assertEqual(run.call_args.args[0], ['kitten', '@', 'ls'])
