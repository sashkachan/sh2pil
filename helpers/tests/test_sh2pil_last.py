"""Tests for `sh2pil-last`, the renderer behind the f3 and f4 bindings and the picker's preview.

The helper is loaded from its source path, so the tests run against the file in this repository
rather than an installed copy.
"""
import importlib.machinery
import importlib.util
import json
import os
import pathlib
import sys
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = pathlib.Path(__file__).resolve().parents[1] / 'sh2pil-last'
# Loading the helper from its source path writes a bytecode cache beside it, which would land in
# the working tree as an untracked directory.  Compile nothing.
sys.dont_write_bytecode = True
loader = importlib.machinery.SourceFileLoader('sh2pil_last', str(SCRIPT))
spec = importlib.util.spec_from_loader(loader.name, loader)
sh2pil_last = importlib.util.module_from_spec(spec)
loader.exec_module(sh2pil_last)


class DisplayPathTest(unittest.TestCase):
    """The path the header names, which is not always the file that was read.

    A transcript on another host is read from a local copy, and the header must still say
    where the transcript really lives, or a reader is sent to a file that is already gone.
    """

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.copy = pathlib.Path(self.tmp.name) / 'tmp-copy.jsonl'
        entries = [{'type': 'session', 'id': 'a', 'cwd': '/home/me/infra',
                    'timestamp': '2026-01-01T00:00:00Z'},
                   {'type': 'message',
                    'message': {'role': 'assistant',
                                'content': [{'type': 'text', 'text': 'answer'}]}}]
        self.copy.write_text(''.join(json.dumps(entry) + '\n' for entry in entries))

    def test_the_header_names_the_file_that_was_read_by_default(self):
        text, count, _ = sh2pil_last.render_full(self.copy)
        self.assertEqual(count, 1)
        self.assertIn(f'- file: {self.copy}', text)

    def test_a_display_path_names_the_transcript_the_reader_is_told(self):
        text, count, _ = sh2pil_last.render_full(self.copy,
                                             '/home/me/.pi/agent/sessions/x/a.jsonl')
        self.assertEqual(count, 1)
        self.assertIn('- file: /home/me/.pi/agent/sessions/x/a.jsonl', text)
        self.assertNotIn(str(self.copy), text)

    def test_the_flag_is_accepted_on_the_command_line(self):
        args = sh2pil_last.parse_args(['--session', str(self.copy), '--full',
                                   '--display-path', '/remote/a.jsonl'])
        self.assertEqual(args.display_path, '/remote/a.jsonl')
        self.assertTrue(args.full)


if __name__ == '__main__':
    unittest.main()


class TerminalWindowTest(unittest.TestCase):
    """Which terminal the window id names, and which id a binding child is in."""

    def test_a_uuid_is_read_from_ghostty(self):
        with patch.object(sh2pil_last, 'ghostty_window',
                          return_value=('/tmp', 'pi - chat - tmp')) as ghostty, \
                patch.object(sh2pil_last, 'kitty_window') as kitty:
            self.assertEqual(sh2pil_last.terminal_window('AAA-BBB'),
                             ('/tmp', 'pi - chat - tmp'))
        ghostty.assert_called_once_with('AAA-BBB')
        kitty.assert_not_called()

    def test_a_number_is_read_from_kitty(self):
        with patch.object(sh2pil_last, 'kitty_window', return_value=('/srv', 'title')) as kitty, \
                patch.object(sh2pil_last, 'ghostty_window') as ghostty:
            self.assertEqual(sh2pil_last.terminal_window('42'), ('/srv', 'title'))
        kitty.assert_called_once_with('42')
        ghostty.assert_not_called()

    def test_a_kitty_child_names_its_window(self):
        with patch.dict(sh2pil_last.os.environ, {'KITTY_WINDOW_ID': '42'}, clear=True):
            self.assertEqual(sh2pil_last.terminal_identifier(), '42')

    def test_inside_ghostty_the_front_surface_is_named(self):
        with patch.dict(sh2pil_last.os.environ, {'TERM_PROGRAM': 'ghostty'}, clear=True), \
                patch.object(sh2pil_last, 'ghostty_front_terminal', return_value='AAA'):
            self.assertEqual(sh2pil_last.terminal_identifier(), 'AAA')

    def test_nothing_known_names_nothing(self):
        with patch.dict(sh2pil_last.os.environ, {}, clear=True):
            self.assertEqual(sh2pil_last.terminal_identifier(), '')
