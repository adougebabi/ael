#!/usr/bin/env python3
"""Unit tests for the worklog checker only; these do not test AEL V2."""
from pathlib import Path
import tempfile
import unittest
from check_worklog import check

class WorklogChecks(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.path = self.root / 'WORKLOG.md'
        # Test a fixed initial ledger, not the real ledger that changes as work
        # progresses. Each case owns its receipt directory under self.root.
        self.text = '当前任务: V2-00\n\n' + ''.join(
            f'| V2-{i:02d} | 任务 {i} | PENDING | — |\n' for i in range(24)
        )
    def tearDown(self):
        self.tmp.cleanup()
    def run_case(self, text):
        self.path.write_text(text, encoding='utf-8')
        return check(self.path)
    def replace_status(self, text, task, status, receipt='—'):
        lines = text.splitlines()
        for i, line in enumerate(lines):
            if line.startswith(f'| {task} |'):
                cells = [s.strip() for s in line.strip('|').split('|')]
                cells[2], cells[3] = status, receipt
                lines[i] = '| ' + ' | '.join(cells) + ' |'
        return '\n'.join(lines) + '\n'
    def test_initial_valid(self):
        self.assertEqual([], self.run_case(self.text))
    def test_one_active_valid(self):
        self.assertEqual([], self.run_case(self.replace_status(self.text, 'V2-00', 'ACTIVE')))
    def test_two_active_rejected(self):
        text = self.replace_status(self.text, 'V2-00', 'ACTIVE')
        text = self.replace_status(text, 'V2-01', 'ACTIVE')
        self.assertTrue(any('WIP' in x for x in self.run_case(text)))
    def test_skip_rejected(self):
        text = self.replace_status(self.text, 'V2-01', 'ACTIVE')
        self.assertTrue(any('必须保持 PENDING' in x for x in self.run_case(text)))
    def test_missing_receipt_rejected(self):
        text = self.replace_status(self.text, 'V2-00', 'DONE', 'receipts/V2-00.md')
        text = text.replace('当前任务: V2-00', '当前任务: V2-01')
        self.assertTrue(any('缺少回执' in x for x in self.run_case(text)))
    def test_next_after_receipt_valid(self):
        (self.root / 'receipts').mkdir()
        (self.root / 'receipts' / 'V2-00.md').write_text('结论: PASS\n', encoding='utf-8')
        text = self.replace_status(self.text, 'V2-00', 'DONE', 'receipts/V2-00.md')
        text = text.replace('当前任务: V2-00', '当前任务: V2-01')
        self.assertEqual([], self.run_case(text))
    def test_done_pause_requires_receipt_and_pending_successor(self):
        (self.root / 'receipts').mkdir()
        receipt = self.root / 'receipts' / 'V2-00.md'
        receipt.write_text('结论: PASS\n', encoding='utf-8')
        text = self.replace_status(self.text, 'V2-00', 'DONE', 'receipts/V2-00.md') + '暂停于: V2-00\n'
        self.assertEqual([], self.run_case(text))
        active = self.replace_status(text, 'V2-01', 'ACTIVE')
        self.assertTrue(self.run_case(active))
        receipt.unlink()
        self.assertTrue(self.run_case(text))

    def test_wrong_current_rejected(self):
        text = self.text.replace('当前任务: V2-00', '当前任务: V2-02')
        self.assertTrue(any('当前任务应是' in x for x in self.run_case(text)))
    def test_false_complete_rejected(self):
        text = self.text.replace('当前任务: V2-00', '当前任务: COMPLETE')
        self.assertTrue(self.run_case(text))
    def test_blocked_is_valid_only_current(self):
        text = self.replace_status(self.text, 'V2-00', 'BLOCKED')
        self.assertEqual([], self.run_case(text))
        text = self.replace_status(text, 'V2-01', 'VERIFYING')
        self.assertTrue(self.run_case(text))

if __name__ == '__main__':
    unittest.main()
