#!/usr/bin/env python3
"""Check only serial worklog structure and receipt presence; never certify code/tests."""
from __future__ import annotations
import argparse
from pathlib import Path
import re
import sys

STATES = {"PENDING", "ACTIVE", "VERIFYING", "DONE", "BLOCKED"}
IN_PROGRESS = {"ACTIVE", "VERIFYING", "BLOCKED"}
TASK_IDS = [f"V2-{i:02d}" for i in range(24)]

def check(path: Path) -> list[str]:
    errors: list[str] = []
    text = path.read_text(encoding="utf-8")
    current = re.search(r"^当前任务:\s*(V2-\d{2}|COMPLETE)\s*$", text, re.M)
    if not current:
        errors.append("缺少唯一的 '当前任务: V2-XX' 或 '当前任务: COMPLETE'。")
    if len(re.findall(r"^当前任务:", text, re.M)) != 1:
        errors.append("当前任务标识必须恰好一条。")
    rows: dict[str, str] = {}
    for line in text.splitlines():
        cells = [x.strip() for x in line.strip().strip("|").split("|")]
        if not cells or not re.fullmatch(r"V2-\d{2}", cells[0]):
            continue
        task_id = cells[0]
        if task_id in rows:
            errors.append(f"{task_id} 重复。")
        if len(cells) != 4:
            errors.append(f"{task_id} 必须有 ID、任务、状态、回执四列。")
            continue
        rows[task_id] = cells[2]
        if cells[2] not in STATES:
            errors.append(f"{task_id} 非法状态：{cells[2]}")
        if cells[2] == "DONE":
            expected = f"receipts/{task_id}.md"
            if cells[3] != expected:
                errors.append(f"{task_id} DONE 的回执应为 {expected}。")
                continue
            receipt = path.parent / expected
            if not receipt.is_file():
                errors.append(f"{task_id} 缺少回执：{expected}")
            else:
                data = receipt.read_text(encoding="utf-8")
                if not re.search(r"^结论:\s*PASS\s*$", data, re.M):
                    errors.append(f"{task_id} 回执未明确写 '结论: PASS'。")
    if list(rows) != TASK_IDS:
        errors.append("任务表必须完整且依序包含 V2-00 至 V2-23。")
    if sum(s in IN_PROGRESS for s in rows.values()) > 1:
        errors.append("WIP 超限：ACTIVE / VERIFYING / BLOCKED 合计最多一个。")
    first_open = next((t for t in TASK_IDS if rows.get(t) != "DONE"), None)
    expected_current = first_open or "COMPLETE"
    pauses = re.findall(r"^暂停于:\s*(V2-\d{2})\s*$", text, re.M)
    if pauses:
        paused = pauses[0]
        previous = TASK_IDS[TASK_IDS.index(first_open)-1] if first_open and TASK_IDS.index(first_open)>0 else None
        if len(pauses) != 1 or paused != previous or rows.get(paused) != "DONE" or rows.get(first_open) != "PENDING":
            errors.append("暂停位置必须为最后一个连续 DONE，且下一项保持 PENDING。")
        else:
            expected_current = paused
    if current and current.group(1) != expected_current:
        errors.append(f"当前任务应是 {expected_current}，不是 {current.group(1)}。")
    if first_open:
        start = TASK_IDS.index(first_open)
        for task_id in TASK_IDS[start + 1:]:
            if rows.get(task_id) != "PENDING":
                errors.append(f"{first_open} 尚未完成，后续 {task_id} 必须保持 PENDING。")
    return errors

def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("worklog", nargs="?", type=Path,
                        default=Path(__file__).resolve().parents[1] / "WORKLOG.md")
    args = parser.parse_args()
    try:
        errors = check(args.worklog)
    except (OSError, UnicodeError) as exc:
        print(f"无法读取实施账本：{exc}", file=sys.stderr)
        return 2
    if errors:
        for error in errors:
            print(f"错误：{error}", file=sys.stderr)
        return 1
    print("实施账本顺序与 WIP 检查通过。此结果不代表任何代码或功能验收通过。")
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
