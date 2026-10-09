"""Tests for `pi-last`, the renderer behind the f3 and f4 bindings and the picker's preview.

The helper is loaded from its source path, so the tests run against the file chezmoi manages
rather than an installed copy.
"""
import importlib.machinery
import importlib.util
import json
import pathlib
import sys
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).resolve().parents[1] / 'pi-last'
# Loading the helper from its source path writes a bytecode cache beside it, which would land
# in the chezmoi source tree and be picked up there as a managed file.  Compile nothing.
sys.dont_write_bytecode = True
loader = importlib.machinery.SourceFileLoader('pi_last', str(SCRIPT))
spec = importlib.util.spec_from_loader(loader.name, loader)
pi_last = importlib.util.module_from_spec(spec)
loader.exec_module(pi_last)


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
        text, count, _ = pi_last.render_full(self.copy)
        self.assertEqual(count, 1)
        self.assertIn(f'- file: {self.copy}', text)

    def test_a_display_path_names_the_transcript_the_reader_is_told(self):
        text, count, _ = pi_last.render_full(self.copy,
                                             '/home/me/.pi/agent/sessions/x/a.jsonl')
        self.assertEqual(count, 1)
        self.assertIn('- file: /home/me/.pi/agent/sessions/x/a.jsonl', text)
        self.assertNotIn(str(self.copy), text)

    def test_the_flag_is_accepted_on_the_command_line(self):
        args = pi_last.parse_args(['--session', str(self.copy), '--full',
                                   '--display-path', '/remote/a.jsonl'])
        self.assertEqual(args.display_path, '/remote/a.jsonl')
        self.assertTrue(args.full)


if __name__ == '__main__':
    unittest.main()
