"""Tests for prompt-backed detached Pi launches."""
import argparse
import contextlib
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
sys.dont_write_bytecode = True
loader = importlib.machinery.SourceFileLoader('sh2pil_open_detached', str(SCRIPT))
spec = importlib.util.spec_from_loader(loader.name, loader)
sh2pil_open = importlib.util.module_from_spec(spec)
loader.exec_module(sh2pil_open)


class DetachedPiNewTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = pathlib.Path(self.tmp.name) / 'project with spaces'
        self.root.mkdir()
        self.message_file = pathlib.Path(self.tmp.name) / 'initial prompt.txt'
        self.message = "Review this safely: ' ; $(touch /tmp/not-a-command)\n"
        self.message_file.write_text(self.message, encoding='utf-8')
        self.feature_title = 'Prompt-backed detached Pi launch'
        self.args = argparse.Namespace(detached=True, json=True,
                                       message_file=str(self.message_file),
                                       feature_title=self.feature_title,
                                       place='tab', emit_cmd=False, label='')

    def invoke(self, cwd=None, **args):
        stdout, stderr = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            code = sh2pil_open.detached_pi_new(str(cwd or self.root),
                                               argparse.Namespace(**(vars(self.args) | args)))
        return code, stdout.getvalue(), stderr.getvalue()

    def test_start_uses_zmx_run_with_the_prompt_file_as_data_and_sets_labels(self):
        completed = subprocess.CompletedProcess([], 0, stdout='', stderr='')
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/opt/bin/zmx'),
              patch.object(sh2pil_open.secrets, 'token_hex', return_value='0123456789'),
              patch.object(sh2pil_open.subprocess, 'run', side_effect=[
                  subprocess.CompletedProcess([], 0, stdout='old-session\n', stderr=''),
                  completed, completed]) as run,
              patch.dict(sh2pil_open.os.environ, {'ZMX_SESSION': 'parent'})):
            code, output, error = self.invoke()
        self.assertEqual(code, 0)
        self.assertEqual(error, '')
        result = json.loads(output)
        self.assertEqual(result, {'name': 'pi-Prompt-backed-detached-0123456789',
                                  'attach_command': '/opt/bin/zmx attach pi-Prompt-backed-detached-0123456789',
                                  'status': 'started'})
        self.assertLessEqual(len(result['name'].encode('utf-8')), 38)
        launch = run.call_args_list[1]
        self.assertEqual(launch.args[0], [
            '/opt/bin/zmx', 'run', result['name'], '-d', 'pi', '--name', self.feature_title,
            '--', f'@{self.message_file.resolve()}',
            "The attached file contains the user's initial request. Read it and act on its contents now."])
        self.assertEqual(launch.kwargs['cwd'], str(self.root.resolve()))
        self.assertEqual(launch.kwargs['env']['SH2PIL_ZMX'], '/opt/bin/zmx')
        self.assertEqual(launch.kwargs['env']['PIB_ZMX'], '/opt/bin/zmx')
        self.assertNotIn('ZMX_SESSION', launch.kwargs['env'])
        self.assertEqual(run.call_args_list[2].args[0], [
            '/opt/bin/zmx', 'set', result['name'], 'project=project-with-spaces'])
        self.assertNotIn(self.message, output + error)
        self.assertNotIn(self.message, repr(launch.args[0]))

    def test_existing_name_collision_is_avoided(self):
        completed = subprocess.CompletedProcess([], 0, stdout='', stderr='')
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open.secrets, 'token_hex', return_value='0123456789'),
              patch.object(sh2pil_open.subprocess, 'run', side_effect=[
                  subprocess.CompletedProcess([], 0,
                      stdout='pi-Prompt-backed-detached-0123456789\n', stderr=''),
                  completed, completed]) as run):
            code, output, _ = self.invoke()
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(output)['name'], 'pi-Prompt-backed-detached-0123456789-2')
        self.assertLessEqual(len(json.loads(output)['name'].encode('utf-8')), 38)
        self.assertEqual(run.call_args_list[1].args[0][2],
                         'pi-Prompt-backed-detached-0123456789-2')

    def test_long_title_gets_a_bounded_safe_slug_but_reaches_pi_unchanged(self):
        title = "Review; $(touch /tmp/nope) " + 'x' * 200
        completed = subprocess.CompletedProcess([], 0, stdout='', stderr='')
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open.secrets, 'token_hex', return_value='abcdef1234'),
              patch.object(sh2pil_open.subprocess, 'run', side_effect=[
                  subprocess.CompletedProcess([], 0, stdout='', stderr=''), completed, completed]) as run):
            code, output, error = self.invoke(feature_title=title)
        self.assertEqual(code, 0)
        self.assertEqual(error, '')
        result = json.loads(output)
        self.assertLessEqual(len(result['name'].encode('utf-8')), 38)
        self.assertTrue(result['name'].startswith('pi-Review-touch-tmp-nope'))
        argv = run.call_args_list[1].args[0]
        self.assertEqual(argv[6], title)
        self.assertEqual(argv[8], f'@{self.message_file.resolve()}')
        self.assertEqual(argv[9], sh2pil_open.DETACHED_PI_INITIAL_INSTRUCTION)
        self.assertNotIn(self.message, repr(argv))
        self.assertNotIn(title, output + error)

    def test_missing_zmx_and_invalid_cwd_fail_without_launch(self):
        with patch.object(sh2pil_open, 'zmx_binary', return_value=''):
            code, _, error = self.invoke()
        self.assertEqual(code, 1)
        self.assertIn('requires zmx', error)
        with patch.object(sh2pil_open, 'zmx_binary') as binary:
            code, _, error = self.invoke(cwd=self.root / 'missing')
        self.assertEqual(code, 1)
        self.assertIn('not a directory', error)
        binary.assert_not_called()

    def test_empty_invalid_and_missing_message_files_fail_without_launch(self):
        for value, expected in [(b' \n', 'empty'), (b'\xff', 'UTF-8')]:
            self.message_file.write_bytes(value)
            with patch.object(sh2pil_open, 'zmx_binary') as binary:
                code, _, error = self.invoke()
            self.assertEqual(code, 1)
            self.assertIn(expected, error)
            binary.assert_not_called()
        code, _, error = self.invoke(message_file=str(self.message_file / 'missing'))
        self.assertEqual(code, 1)
        self.assertIn('UTF-8', error)

    def test_incompatible_placement_and_missing_required_flags_fail(self):
        for args in ({'place': 'current'}, {'emit_cmd': True}, {'label': 'title'},
                     {'json': False}, {'detached': False}, {'message_file': ''},
                     {'feature_title': ''}):
            with self.subTest(args=args):
                code, _, error = self.invoke(**args)
                self.assertEqual(code, 1)
                if args.get('json') is False or args.get('detached') is False or args.get('message_file') == '':
                    self.assertIn('must be used together', error)
                elif args.get('feature_title') == '':
                    self.assertIn('--feature-title must not be empty', error)
                else:
                    self.assertIn('cannot be combined', error)

    def test_uncertain_zmx_start_returns_session_name_without_retry(self):
        with (patch.object(sh2pil_open, 'zmx_binary', return_value='/bin/zmx'),
              patch.object(sh2pil_open.secrets, 'token_hex', return_value='0123456789'),
              patch.object(sh2pil_open.subprocess, 'run', side_effect=[
                  subprocess.CompletedProcess([], 0, stdout='', stderr=''),
                  subprocess.CompletedProcess([], 1, stdout='', stderr=self.message)]) as run):
            code, output, error = self.invoke()
        self.assertEqual(code, 1)
        self.assertEqual(json.loads(output)['status'], 'uncertain')
        self.assertIn('inspect zmx session', error)
        self.assertNotIn(self.message, output + error)
        self.assertEqual(run.call_count, 2)

    def test_feature_title_alone_does_not_silently_fall_through_to_regular_new(self):
        args = argparse.Namespace(cwd=str(self.root), detached=False, message_file=None,
                                 json=False, feature_title='feature', place='tab',
                                 emit_cmd=False, label='')
        with patch.object(sh2pil_open, 'detached_pi_new', return_value=1) as detached:
            self.assertEqual(sh2pil_open.open_terminal('new', args), 1)
        detached.assert_called_once()

    def test_parser_accepts_opt_in_flags_but_keeps_default_new(self):
        args = sh2pil_open.parse_args(['new', str(self.root), '--message-file',
                                       str(self.message_file), '--detached', '--json',
                                       '--feature-title', self.feature_title])
        self.assertEqual(args.action, 'new')
        self.assertTrue(args.detached)
        self.assertTrue(args.json)
        default = sh2pil_open.parse_args(['new', str(self.root)])
        self.assertFalse(default.detached)
        self.assertFalse(default.json)
        self.assertIsNone(default.message_file)
        self.assertIsNone(default.feature_title)


if __name__ == '__main__':
    unittest.main()
