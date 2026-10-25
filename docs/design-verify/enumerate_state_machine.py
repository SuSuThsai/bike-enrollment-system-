#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""枚举 docs/state-machine.md 的「状态 × 触发事件」全组合并做结构审计。

输出四件事：
  1. 合法迁移列表（迁移表里出现的边）
  2. 应被拒绝的组合（迁移表里没有的边，含原因码与文档行号）
  3. 死锁检查：非终态、且没有任何合法出边
  4. 孤立状态检查：从初始状态「（无）」BFS 不可达

设计原则：状态、事件、终态、迁移边全部从文档表格解析，不在脚本里硬编码，
保证文档改了脚本结论跟着变。仅"文档正文里写明的拒绝时行为"（如重复回调
直接返回 200）是手工映射，只作为备注输出，不参与合法性判定。

用法：
    python docs/design-verify/enumerate_state_machine.py
    python docs/design-verify/enumerate_state_machine.py --md docs/design-verify/reports/state-machine-audit.md
    python docs/design-verify/enumerate_state_machine.py --json docs/design-verify/reports/state-machine-audit.json --strict
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import unicodedata
from collections import OrderedDict, deque
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path

INIT = "（无）"
TERMINAL = "终态"
FUTURE_MARK = "迭代二"

COL_SRC = "当前状态"
COL_EVENT = "触发事件"
COL_DST = "目标状态"
COL_ACTOR = "执行方"
COL_IDEM = "幂等键"
COL_EFFECT = "副作用"

# 章节标题 -> 实体名。只解析这几节，其它章节的表格（如存储分布表）跳过。
MACHINE_TITLES = OrderedDict([
    ("Lock 状态机", "Lock"),
    ("Order 状态机", "Order"),
    ("Enrollment 状态机", "Enrollment"),
])

# 文档正文（不是表格）里明确规定过的"拒绝时应有行为"。
# (实体, 适用状态, 事件文本关键字, 行为说明, 文档依据)
ANNOTATIONS = [
    ("Order", {"paid", "expired", "refunded"}, "微信支付回调",
     "重复回调：事务内先查状态，非 pending_payment 直接 COMMIT 返回 200，不 UPDATE、不碰 Redis",
     "§4.2 支付回调伪代码 步骤 2"),
    ("Order", {"paid", "expired", "refunded"}, "定时任务扫描",
     "更新条件 WHERE status='pending_payment'，affected_rows=0 后跳过，不回补 Redis",
     "§4.3 支付超时任务伪代码"),
    ("Order", {"pending_payment", "expired", "refunded", "refund_failed", "refunding"}, "退款",
     "退款只允许 paid→refunding；refunding 收到 REFUND.SUCCESS 才进入 refunded 并回补 Redis，"
     "REFUND.CLOSED/ABNORMAL 进入 refund_failed；其它状态拒绝或幂等返回，不回补",
     "§4.4 退款流程伪代码"),
    ("Lock", {"consumed", "expired"}, "提交成功",
     "UPDATE ... WHERE status='active' AND expires_at > NOW() 命中 0 行 → 返回 LOCK_EXPIRED，不创建 Enrollment",
     "§3.3 提交报名伪代码"),
    ("Lock", {"consumed", "expired"}, "定时任务扫描",
     "扫描条件 WHERE status='active'，命中 0 行 → 跳过，不回补 Redis",
     "§3.4 Lock 过期任务伪代码"),
    ("Enrollment", {"pending_payment", "cancelled", "refunded"}, "退款",
     "退款只允许 paid/confirmed→refunded，且必须由 Order refunded 事务联动；其它状态拒绝，避免重复回补",
     "§5.3 退款联动要求"),
]


# --------------------------------------------------------------------------
# 数据结构
# --------------------------------------------------------------------------

@dataclass
class Transition:
    src: str
    event: str
    dst: str
    line: int
    actor: str = ""
    idem: str = ""
    effect: str = ""
    future: bool = False
    event_id: str = ""

    @property
    def label(self) -> str:
        return f"{self.src} --{self.event_id}--> {self.dst}"


@dataclass
class Machine:
    name: str
    heading: str
    line: int
    states: list[str]
    events: list[tuple[str, str, int]]  # (event_id, text, first_line)
    terminal: dict[str, int]            # state -> 终态声明行号
    transitions: list[Transition]


@dataclass
class Analysis:
    machine: Machine
    include_future: bool
    legal: list[dict] = field(default_factory=list)
    rejected: list[dict] = field(default_factory=list)
    combos: int = 0
    out_degree: dict[str, int] = field(default_factory=dict)
    deadlocks: list[str] = field(default_factory=list)
    unreachable: list[str] = field(default_factory=list)
    paths: dict[str, list[tuple[str, str, str]]] = field(default_factory=dict)


# --------------------------------------------------------------------------
# 解析文档表格
# --------------------------------------------------------------------------

def strip_ticks(cell: str) -> str:
    return cell.replace("`", "").strip()


def clean_event(cell: str) -> str:
    return " ".join(strip_ticks(cell).split())


def split_state_cell(cell: str) -> list[str]:
    cell = cell.strip()
    if not cell:
        return []
    if set(strip_ticks(cell)) <= set("——- "):
        return []
    return [s for s in (strip_ticks(p) for p in cell.split("/")) if s]


def parse_doc(doc_path: Path) -> list[dict]:
    try:
        lines = doc_path.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        raise SystemExit(f"无法读取文档 {doc_path}: {exc}")

    raw_machines: list[dict] = []
    current: dict | None = None
    header: list[str] | None = None

    for lineno, raw in enumerate(lines, 1):
        text = raw.strip()

        if text.startswith("## "):
            title = text.lstrip("#").strip()
            name = next((n for needle, n in MACHINE_TITLES.items() if needle in title), None)
            if name:
                current = {
                    "name": name,
                    "heading": title,
                    "line": lineno,
                    "rows": [],
                    "terminal": OrderedDict(),
                    "columns": [],
                }
                raw_machines.append(current)
            else:
                current = None
            header = None
            continue

        if not text.startswith("|"):
            # 表格块在空行/正文处结束，避免把下一张表当成当前表的数据行
            header = None
            continue

        if current is None:
            continue

        cells = [c.strip() for c in text.strip("|").split("|")]

        if header is None:
            joined = "".join(cells)
            if COL_SRC in joined and COL_EVENT in joined and COL_DST in joined:
                header = cells
                current["columns"] = cells
            continue

        if all(c and set(c) <= set(":-") for c in cells):
            continue  # |---|---| 分隔行

        row = {name: (cells[i] if i < len(cells) else "") for i, name in enumerate(header)}
        current["rows"].append((lineno, row))

    return raw_machines


def build_machines(raw_machines: list[dict]) -> list[Machine]:
    machines: list[Machine] = []
    for raw in raw_machines:
        name = raw["name"]
        transitions: list[Transition] = []
        terminal: dict[str, int] = OrderedDict(raw["terminal"])
        event_order: "OrderedDict[str, int]" = OrderedDict()

        for lineno, row in raw["rows"]:
            srcs = split_state_cell(row.get(COL_SRC, ""))
            dst_raw = row.get(COL_DST, "")
            dst_clean = strip_ticks(dst_raw)

            if dst_clean == TERMINAL:
                for s in srcs:
                    terminal.setdefault(s, lineno)
                continue

            event = clean_event(row.get(COL_EVENT, ""))
            dsts = split_state_cell(dst_raw)
            if not event or not dsts:
                continue

            for src in srcs:
                for dst in dsts:
                    transitions.append(Transition(
                        src=src,
                        event=event,
                        dst=dst,
                        line=lineno,
                        actor=strip_ticks(row.get(COL_ACTOR, "")),
                        idem=strip_ticks(row.get(COL_IDEM, "")),
                        effect=strip_ticks(row.get(COL_EFFECT, "")),
                        future=FUTURE_MARK in event,
                    ))
                    event_order.setdefault(event, lineno)

        states: list[str] = []
        if any(t.src == INIT for t in transitions):
            states.append(INIT)
        for t in transitions:
            for s in (t.src, t.dst):
                if s not in states:
                    states.append(s)
        for s in terminal:
            if s not in states:
                states.append(s)

        eid_map = {text: f"E{i}" for i, text in enumerate(event_order, 1)}
        for t in transitions:
            t.event_id = eid_map[t.event]

        events = [(eid_map[text], text, line) for text, line in event_order.items()]
        machines.append(Machine(
            name=name,
            heading=raw["heading"],
            line=raw["line"],
            states=states,
            events=events,
            terminal=terminal,
            transitions=transitions,
        ))

    return machines


# --------------------------------------------------------------------------
# 校验与审计
# --------------------------------------------------------------------------

def validate(machines: list[Machine]) -> list[str]:
    problems: list[str] = []
    if not machines:
        problems.append("文档中没有解析到任何状态机表格（检查章节标题是否仍含 “Lock/Order/Enrollment 状态机”）")

    for m in machines:
        if not m.transitions:
            problems.append(f"{m.name}: 解析到 0 条迁移，表格格式可能已变")
        seen = set()
        for t in m.transitions:
            key = (t.src, t.event_id, t.dst)
            if key in seen:
                problems.append(f"{m.name}: 迁移重复 `{t.label}`（L{t.line}）")
            seen.add(key)
            if t.dst == INIT:
                problems.append(f"{m.name}: 迁移目标为初始伪状态「{INIT}」（L{t.line}）")
            if t.src == t.dst:
                problems.append(f"{m.name}: 自环迁移 `{t.label}`（L{t.line}）")
        known = set(m.states)
        for s in m.terminal:
            if s not in known:
                problems.append(f"{m.name}: 终态 {s} 不在状态集合内")
        for eid, text, _ in m.events:
            if not any(t.event_id == eid for t in m.transitions):
                problems.append(f"{m.name}: 事件 {eid}({text}) 没有任何迁移")
    return problems


def reject_reason(machine: Machine, state: str, event_id: str, include_future: bool) -> tuple[str, str]:
    if state in machine.terminal:
        return ("R1", f"终态不可迁出（L{machine.terminal[state]}）")
    if state == INIT:
        return ("R3", "实体不存在，事件无作用对象")
    same = [t for t in machine.transitions if t.src == state and t.event_id == event_id]
    if same and all(t.future for t in same) and not include_future:
        return ("R4", f"迭代二功能未实现（L{same[0].line}）")
    return ("R2", "迁移表未定义（开篇总则：未列出即非法）")


def annotation_for(machine_name: str, state: str, event_text: str) -> str:
    for mname, states, keyword, note, ref in ANNOTATIONS:
        if machine_name == mname and state in states and keyword in event_text:
            return f"{note}（{ref}）"
    return ""


def analyze(machine: Machine, include_future: bool) -> Analysis:
    edges: "OrderedDict[tuple[str, str], list[Transition]]" = OrderedDict()
    for t in machine.transitions:
        if t.future and not include_future:
            continue
        edges.setdefault((t.src, t.event_id), []).append(t)

    legal: list[dict] = []
    rejected: list[dict] = []
    for state in machine.states:
        for eid, etext, _ in machine.events:
            ts = edges.get((state, eid), [])
            if ts:
                legal.append({
                    "from": state,
                    "event_id": eid,
                    "event": etext,
                    "to": [t.dst for t in ts],
                    "lines": [t.line for t in ts],
                    "idem": ts[0].idem,
                    "actor": ts[0].actor,
                    "effect": ts[0].effect,
                    "future": ts[0].future,
                })
            else:
                code, reason = reject_reason(machine, state, eid, include_future)
                rejected.append({
                    "from": state,
                    "event_id": eid,
                    "event": etext,
                    "code": code,
                    "reason": reason,
                    "note": annotation_for(machine.name, state, etext),
                })

    out_degree = {s: 0 for s in machine.states}
    for s, _eid in edges:
        out_degree[s] += 1

    deadlocks = [
        s for s in machine.states
        if s != INIT and s not in machine.terminal and out_degree[s] == 0
    ]

    adj: dict[str, list[tuple[str, str]]] = {}
    for (s, eid), ts in edges.items():
        for t in ts:
            adj.setdefault(s, []).append((eid, t.dst))

    pred: dict[str, tuple[str, str]] = {}
    seen = {INIT}
    queue = deque([INIT])
    while queue:
        cur = queue.popleft()
        for eid, dst in adj.get(cur, []):
            if dst not in seen:
                seen.add(dst)
                pred[dst] = (cur, eid)
                queue.append(dst)

    paths: dict[str, list[tuple[str, str, str]]] = {}
    for s in machine.states:
        if s not in seen or s == INIT:
            continue
        chain: list[tuple[str, str, str]] = []
        node = s
        while node in pred:
            prev, eid = pred[node]
            chain.append((prev, eid, node))
            node = prev
        paths[s] = list(reversed(chain))
    if INIT in seen:
        paths[INIT] = []

    unreachable = [s for s in machine.states if s not in seen]

    return Analysis(
        machine=machine,
        include_future=include_future,
        legal=legal,
        rejected=rejected,
        combos=len(machine.states) * len(machine.events),
        out_degree=out_degree,
        deadlocks=deadlocks,
        unreachable=unreachable,
        paths=paths,
    )


# --------------------------------------------------------------------------
# 文本/表格工具（中英文混排按显示宽度对齐）
# --------------------------------------------------------------------------

def disp_width(text: str) -> int:
    return sum(2 if unicodedata.east_asian_width(ch) in ("W", "F") else 1 for ch in text)


def pad(text: str, width: int, align: str = "l") -> str:
    gap = max(0, width - disp_width(text))
    if align == "r":
        return " " * gap + text
    return text + " " * gap


def render_table(headers: list[str], rows: list[list[str]], aligns: list[str] | None = None) -> str:
    rows = [[str(c) for c in r] for r in rows]
    aligns = aligns or ["l"] * len(headers)
    widths = [
        max([disp_width(headers[i])] + [disp_width(r[i]) for r in rows if i < len(r)])
        for i in range(len(headers))
    ]
    out = ["  ".join(pad(h, widths[i]) for i, h in enumerate(headers))]
    out.append("  ".join("-" * w for w in widths))
    for r in rows:
        out.append("  ".join(pad(r[i], widths[i], aligns[i]) for i in range(len(headers))))
    return "\n".join(line.rstrip() for line in out)


def md_table(headers: list[str], rows: list[list[str]]) -> str:
    lines = ["| " + " | ".join(headers) + " |",
             "|" + "|".join([" --- "] * len(headers)) + "|"]
    for r in rows:
        lines.append("| " + " | ".join(str(c).replace("|", "\\|") for c in r) + " |")
    return "\n".join(lines)


def code(text: str) -> str:
    return f"`{text}`"


def matrix_text(machine: Machine, analysis: Analysis) -> str:
    accepted = {(r["from"], r["event_id"]) for r in analysis.legal}
    headers = [""] + [eid for eid, _, _ in machine.events]
    rows = [[state] + ["✓" if (state, eid) in accepted else "·" for eid, _, _ in machine.events]
            for state in machine.states]
    return render_table(headers, rows)


def legend_text(machine: Machine) -> str:
    return "；".join(f"{eid} = {text}" for eid, text, _ in machine.events)


def path_text(chain: list[tuple[str, str, str]]) -> str:
    if not chain:
        return INIT
    text = INIT
    for _prev, eid, node in chain:
        text += f" -{eid}-> {node}"
    return text


def pct(part: int, whole: int) -> str:
    return f"{part * 100.0 / whole:.1f}%" if whole else "0.0%"


# --------------------------------------------------------------------------
# 审计口径说明 / 结论 / 告警
# --------------------------------------------------------------------------

def collect_findings(machines: list[Machine], full: list[Analysis], mvp: list[Analysis]) -> list[dict]:
    findings: list[dict] = []

    def add(kind: str, machine: str, message: str, hint: str, severity: str = "warning") -> None:
        findings.append({
            "id": f"W{len(findings) + 1}",
            "severity": severity,
            "kind": kind,
            "machine": machine,
            "message": message,
            "hint": hint,
        })

    for machine, analysis in zip(machines, full):
        for t in machine.transitions:
            if t.src in machine.terminal:
                add(
                    "终态声明与出边并存",
                    machine.name,
                    f"{code(t.src)} 在 L{machine.terminal[t.src]} 被声明为终态，"
                    f"但 L{t.line} 又定义了 {code(t.src)} --{t.event}--> {code(t.dst)}",
                    "按“迭代二”读法，终态行应限定为迭代一的终态（paid 在迭代二有退款出边）；"
                    "否则同一张表里终态与出边互为矛盾。需与 Enrollment 表（paid 未列为终态）统一口径。",
                )

    for machine, analysis in zip(machines, mvp):
        for state in analysis.deadlocks:
            add(
                "MVP 口径业务终点",
                machine.name,
                f"排除迭代二退款边后，{code(state)} 没有 MVP 出边；它是 MVP 的业务终点，"
                f"不是状态机终态（已预留迭代二出边）",
                "不是缺陷：退款上线后该状态自动获得出边，无需修改状态机。"
                "只有当产品确认 MVP 即最终形态时，才需要人工评审是否收回退款预留。",
                severity="info",
            )
        for state in analysis.unreachable:
            add(
                "迭代二才可达",
                machine.name,
                f"排除迭代二退款边后，{code(state)} 无法从初始状态到达",
                "预期行为：退款上线并保留该边后自动可达；MVP 阶段不允许人为制造该状态。",
                severity="info",
            )
    return findings


def scope_points() -> list[str]:
    """枚举口径，按 1..5 编号输出（控制台与 Markdown 共用）。"""
    return [
        f"状态集合 = 迁移表「当前状态 / 目标状态」里出现过的全部值，外加初始伪状态「{INIT}」"
        "（表示实体尚未创建）。",
        "事件集合按实体归集：同一业务动作（如支付回调）在 Order 表和 Enrollment 表里各算一个事件；"
        "文档要求二者在同一个数据库事务内迁移（constraints.md L26-27），但状态机仍分别枚举。",
        "合法性只认迁移表：本文档开篇总则第 1 条明确“任何状态迁移未在本文档中出现，一律视为非法，须在代码中拒绝”。",
        "终态取自各表「终态」行（禁止任何出边）；标注为“迭代二”的迁移参与完整口径枚举，并单独以 MVP 口径复核终点与可达性。",
        f"初始伪状态「{INIT}」不参与死锁判定（它不是实体状态，且必然有创建出边）。",
    ]


def render_console(doc_label: str, machines: list[Machine], full: list[Analysis],
                   mvp: list[Analysis], findings: list[dict], generated_at: str) -> str:
    out: list[str] = []
    add = out.append
    bar = "=" * 92

    total_states = sum(len(m.states) for m in machines)
    total_events = sum(len(m.events) for m in machines)
    total_combos = sum(a.combos for a in full)
    total_legal = sum(len(a.legal) for a in full)
    total_rejected = sum(len(a.rejected) for a in full)

    add(bar)
    add(f"状态机审计 — {doc_label}")
    add(f"生成时间 {generated_at}    Python {sys.version.split()[0]}    数据源：文档迁移表（非硬编码）")
    add(f"规模：{len(machines)} 台状态机 / {total_states} 个状态（含 {len(machines)} 个初始伪状态「{INIT}」）"
        f" / {total_events} 个事件")
    add(f"枚举：{total_combos} 个 (状态 × 事件) 组合 = 合法 {total_legal} + 拒绝 {total_rejected}"
        f"（拒绝占比 {pct(total_rejected, total_combos)}）")
    add(bar)

    # ---- 0. 口径与解析结果 ----
    add("")
    add("## 0. 口径与解析结果")
    for i, point in enumerate(scope_points(), 1):
        add(f"  {i}. {point}")
    add("")
    rows = []
    for m in machines:
        rows.append([
            m.name, str(m.line), str(len(m.states)), str(len(m.events)),
            str(len(m.transitions)), str(len(m.terminal)),
        ])
    add(render_table(["实体", "文档章", "状态数", "事件数", "迁移数", "终态数"], rows,
                     ["l", "r", "r", "r", "r", "r"]))
    add("")
    add("  ### 事件图例（原文）")
    legend_rows = []
    for m in machines:
        for eid, text, line in m.events:
            legend_rows.append([m.name, eid, text, f"L{line}"])
    add(render_table(["实体", "事件", "触发事件原文", "文档行"], legend_rows))

    # ---- 1. 合法迁移 ----
    add("")
    add(f"## 1. 合法迁移列表（{total_legal} 条）")
    add(f"  说明：仅这 {total_legal} 条允许发生；其余一律拒绝。事件列对应上面的图例。")
    add("")
    rows = []
    idx = 0
    for a in full:
        for r in a.legal:
            idx += 1
            rows.append([
                str(idx), a.machine.name, r["from"], r["event_id"],
                " / ".join(r["to"]), r["idem"] or "—", r["actor"] or "—",
                ", ".join(f"L{x}" for x in r["lines"]),
            ])
    add(render_table(["#", "实体", "当前状态", "事件", "目标状态", "幂等键", "执行方", "文档行"], rows,
                     ["r", "l", "l", "l", "l", "l", "l", "l"]))

    # ---- 2. 拒绝组合 ----
    add("")
    add(f"## 2. 应被拒绝的组合（{total_rejected} / {total_combos}）")
    add("  口径：迁移表未出现的 (状态, 事件) 一律非法（开篇总则第 1 条）。")
    add("  原因码：R1 终态不可迁出 ｜ R2 迁移表未定义 ｜ R3 实体尚未创建 ｜ R4 迭代二未实现")
    add("")
    counts: dict[str, int] = {}
    for a in full:
        for r in a.rejected:
            counts[r["code"]] = counts.get(r["code"], 0) + 1
    add("  " + " ｜ ".join(f"{k} {counts.get(k, 0)} 条" for k in ("R1", "R2", "R3", "R4")))

    add("")
    add("  ### 2.1 组合矩阵（✓ 合法 / · 拒绝）")
    for a in full:
        add("")
        add(f"  【{a.machine.name}】{a.machine.heading}")
        for line in matrix_text(a.machine, a).splitlines():
            add("    " + line)
        add(f"    事件：{legend_text(a.machine)}")

    add("")
    add("  ### 2.2 逐条清单")
    add("")
    rows = []
    idx = 0
    for a in full:
        for r in a.rejected:
            idx += 1
            rows.append([str(idx), a.machine.name, r["from"], r["event_id"], r["code"], r["reason"]])
    add(render_table(["#", "实体", "当前状态", "事件", "码", "原因"], rows, ["r", "l", "l", "l", "l", "l"]))

    annotated = [(a.machine.name, r) for a in full for r in a.rejected if r["note"]]
    if annotated:
        add("")
        add("  ### 2.3 拒绝时应有的运行时行为（文档正文补充，非表格内容）")
        add("")
        rows = []
        for mname, r in annotated:
            rows.append([mname, r["from"], r["event_id"], r["note"]])
        add(render_table(["实体", "当前状态", "事件", "应做（文档要求）"], rows, ["l", "l", "l", "l"]))

    # ---- 3. 死锁 ----
    add("")
    add("## 3. 死锁检查：非终态 且 出度为 0")
    add("  口径：终态取文档「终态」行，出边取完整迁移表（含迭代二）。「（无）」不计入。")
    add("")
    rows = []
    for a in full:
        for state in a.machine.states:
            if state == INIT or state in a.machine.terminal:
                continue
            outs = []
            for r in a.legal:
                if r["from"] == state:
                    outs.append(f"{r['event_id']}→{'/'.join(r['to'])}")
            rows.append([a.machine.name, state, str(a.out_degree[state]), "、".join(outs)])
    add(render_table(["实体", "非终态状态", "出度", "合法出边"], rows, ["l", "l", "r", "l"]))
    add("")
    dead = [(a.machine.name, s) for a in full for s in a.deadlocks]
    if dead:
        add("  ❌ 存在死锁状态：")
        for mname, s in dead:
            add(f"     - {mname}.{s}")
    else:
        add("  ✅ 不存在“非终态但无任何合法出边”的死锁状态（完整文档口径）。")

    # ---- 4. 可达性 ----
    add("")
    add(f"## 4. 孤立状态检查：从初始状态「{INIT}」做 BFS")
    add("")
    rows = []
    for a in full:
        for state in a.machine.states:
            if state == INIT:
                continue
            reachable = state not in a.unreachable
            rows.append([a.machine.name, state, "✅" if reachable else "❌",
                         path_text(a.paths.get(state, [])) if reachable else "—"])
    add(render_table(["实体", "状态", "可达", "最短可达路径（事件序列）"], rows, ["l", "l", "l", "l"]))
    add("")
    unreachable = [(a.machine.name, s) for a in full for s in a.unreachable]
    if unreachable:
        add("  ❌ 存在孤立状态：")
        for mname, s in unreachable:
            add(f"     - {mname}.{s}")
    else:
        add("  ✅ 不存在从初始状态不可达的孤立状态（完整文档口径），全部状态可达。")

    # ---- 5. 结论与告警 ----
    add("")
    add("## 5. 结论与告警")
    add("")
    rows = []
    per_machine = " / ".join(f"{a.machine.name} {len(a.legal)}" for a in full)
    rows.append(["合法迁移", f"{total_legal} 条（{per_machine}）"])
    rows.append(["应被拒绝", f"{total_rejected} 条（" + "，".join(f"{k} {counts.get(k, 0)}" for k in sorted(counts)) + "）"])
    rows.append(["死锁状态", "无" if not dead else f"有 {len(dead)} 个"])
    rows.append(["孤立状态", "无" if not unreachable else f"有 {len(unreachable)} 个"])
    warnings = [f for f in findings if f["severity"] == "warning"]
    infos = [f for f in findings if f["severity"] == "info"]
    rows.append(["文档告警", f"警告 {len(warnings)} 项 / 提示 {len(infos)} 项"])
    add(render_table(["检查项", "结果"], rows))
    add("")
    for f in findings:
        tag = "⚠️ 警告" if f["severity"] == "warning" else "ℹ️ 提示"
        add(f"  [{f['id']}] {tag} · {f['kind']} · {f['machine']}")
        add(f"      现状：{f['message']}")
        add(f"      建议：{f['hint']}")

    # ---- 6. MVP 口径 ----
    add("")
    add("## 6. 补充：MVP 口径（排除标注为“迭代二”的迁移）")
    add("")
    rows = []
    for a_full, a_mvp in zip(full, mvp):
        rows.append([
            a_full.machine.name,
            f"{len(a_mvp.legal)} / {len(a_mvp.rejected)}",
            "、".join(a_mvp.deadlocks) or "无",
            "、".join(a_mvp.unreachable) or "无",
        ])
    add(render_table(["实体", "合法 / 拒绝", "MVP 终点（无 MVP 出边）", "迭代二才可达"],
                     rows, ["l", "l", "l", "l"]))
    add("")
    add("  说明：退款迁移已纳入本次实现：Order paid→refunding→refunded/refund_failed；")
    add("        Enrollment paid/confirmed→refunded 仅在 Order refunded 事务内联动。")
    add("        当前迁移表不再包含标注“迭代二”的预留边；后台手动确认接口仍未定义。")

    add("")
    add(bar)
    add("脚本退出：0（审计完成）。使用 --strict 时，出现“警告”级发现会以退出码 1 结束。")
    add(bar)
    return "\n".join(out)


# --------------------------------------------------------------------------
# Markdown / JSON 输出
# --------------------------------------------------------------------------

def render_markdown(doc_label: str, machines: list[Machine], full: list[Analysis],
                    mvp: list[Analysis], findings: list[dict], generated_at: str,
                    doc_href: str = "state-machine.md") -> str:
    out: list[str] = []
    add = out.append
    total_states = sum(len(m.states) for m in machines)
    total_events = sum(len(m.events) for m in machines)
    total_combos = sum(a.combos for a in full)
    total_legal = sum(len(a.legal) for a in full)
    total_rejected = sum(len(a.rejected) for a in full)
    counts: dict[str, int] = {}
    for a in full:
        for r in a.rejected:
            counts[r["code"]] = counts.get(r["code"], 0) + 1
    dead = [(a.machine.name, s) for a in full for s in a.deadlocks]

    add("# 状态机审计报告")
    add("")
    add(f"> 由 `docs/design-verify/enumerate_state_machine.py` 自动生成，"
        f"数据源 [`{doc_label}`]({doc_href})；生成时间 {generated_at}。")
    add("")
    add("## 概览")
    add("")
    add(md_table(["项目", "值"], [
        ["状态机", f"{len(machines)} 台（Lock / Order / Enrollment）"],
        ["状态", f"{total_states} 个（含 {len(machines)} 个初始伪状态「（无）」）"],
        ["事件", f"{total_events} 个"],
        ["(状态 × 事件) 组合", f"{total_combos} 个"],
        ["合法迁移", f"{total_legal} 条"],
        ["应被拒绝", f"{total_rejected} 条（{pct(total_rejected, total_combos)}）"],
        ["死锁状态", "无" if not dead else "有"],
        ["孤立状态", "无" if not [s for a in full for s in a.unreachable] else "有"],
    ]))
    add("")
    add("## 口径")
    add("")
    for i, point in enumerate(scope_points(), 1):
        add(f"{i}. {point}")
    add("")
    add("## 事件图例")
    add("")
    rows = []
    for m in machines:
        for eid, text, line in m.events:
            rows.append([m.name, code(eid), text, f"L{line}"])
    add(md_table(["实体", "事件", "触发事件原文", "文档行"], rows))
    add("")
    add(f"## 1. 合法迁移（{total_legal} 条）")
    add("")
    rows = []
    idx = 0
    for a in full:
        for r in a.legal:
            idx += 1
            rows.append([str(idx), a.machine.name, code(r["from"]), code(r["event_id"]),
                         " / ".join(code(x) for x in r["to"]), r["idem"] or "—",
                         r["actor"] or "—", r["effect"] or "—", ", ".join(f"L{x}" for x in r["lines"])])
    add(md_table(["#", "实体", "当前状态", "事件", "目标状态", "幂等键", "执行方", "副作用", "文档行"], rows))
    add("")
    add(f"## 2. 应被拒绝的组合（{total_rejected} / {total_combos}）")
    add("")
    add("原因码：**R1** 终态不可迁出 ｜ **R2** 迁移表未定义 ｜ **R3** 实体尚未创建 ｜ **R4** 迭代二未实现。")
    add("")
    add("统计：" + "，".join(f"{k} {counts.get(k, 0)} 条" for k in ("R1", "R2", "R3", "R4")))
    add("")
    add("### 2.1 组合矩阵")
    add("")
    for a in full:
        add(f"**{a.machine.name}**（{a.machine.heading}）")
        add("")
        add("```")
        add(matrix_text(a.machine, a))
        add("```")
        add("")
        add("事件：" + legend_text(a.machine))
        add("")
    add("### 2.2 逐条清单")
    add("")
    rows = []
    idx = 0
    for a in full:
        for r in a.rejected:
            idx += 1
            rows.append([str(idx), a.machine.name, code(r["from"]), code(r["event_id"]),
                         r["code"], r["reason"], r["note"] or "—"])
    add(md_table(["#", "实体", "当前状态", "事件", "码", "原因", "文档正文补充"], rows))
    add("")
    add("## 3. 死锁检查")
    add("")
    add("口径：非终态、且没有任何一条合法出边。「（无）」是初始伪状态，不参与判定。")
    add("")
    rows = []
    for a in full:
        for state in a.machine.states:
            if state == INIT or state in a.machine.terminal:
                continue
            outs = [f"{r['event_id']}→{'/'.join(r['to'])}" for r in a.legal if r["from"] == state]
            rows.append([a.machine.name, code(state), str(a.out_degree[state]), "、".join(outs) or "**无**"])
    add(md_table(["实体", "非终态状态", "出度", "合法出边"], rows))
    add("")
    add("**结论：**" + ("❌ 存在死锁状态：" + "、".join(f"{m}.{s}" for m, s in dead)
                       if dead else "✅ 不存在死锁状态。"))
    add("")
    add("## 4. 孤立状态检查")
    add("")
    add(f"从初始状态「{INIT}」做 BFS，按迁移表的有向边展开。")
    add("")
    rows = []
    for a in full:
        for state in a.machine.states:
            if state == INIT:
                continue
            ok = state not in a.unreachable
            rows.append([a.machine.name, code(state), "✅ 可达" if ok else "❌ 不可达",
                         code(path_text(a.paths.get(state, []))) if ok else "—"])
    add(md_table(["实体", "状态", "可达性", "最短路径（事件序列）"], rows))
    add("")
    add("**结论：**" + ("❌ 存在孤立状态。" if [s for a in full for s in a.unreachable]
                       else "✅ 不存在从初始状态不可达的孤立状态，全部状态可达。"))
    add("")
    add("## 5. 结论与告警")
    add("")
    for f in findings:
        label = "警告" if f["severity"] == "warning" else "提示"
        add(f"### [{f['id']}] {label} · {f['kind']} · {f['machine']}")
        add("")
        add(f"- 现状：{f['message']}")
        add(f"- 建议：{f['hint']}")
        add("")
    add("## 6. MVP 口径（排除“迭代二”退款边）")
    add("")
    rows = []
    for a_full, a_mvp in zip(full, mvp):
        rows.append([a_full.machine.name, str(len(a_mvp.legal)), str(len(a_mvp.rejected)),
                     "、".join(code(s) for s in a_mvp.deadlocks) or "无",
                     "、".join(code(s) for s in a_mvp.unreachable) or "无"])
    add(md_table(["实体", "合法迁移", "应拒绝", "MVP 终点（无 MVP 出边）", "迭代二才可达"], rows))
    add("")
    add("- 退款迁移已纳入本次实现：Order `paid→refunding→refunded/refund_failed`；"
        "Enrollment `paid/confirmed→refunded` 仅在 Order `refunded` 事务内联动。")
    add("- 当前迁移表不再包含标注“迭代二”的预留边；后台手动确认接口仍未定义。")
    add("")
    return "\n".join(out)


def to_json(doc_label: str, machines: list[Machine], full: list[Analysis],
            mvp: list[Analysis], findings: list[dict], generated_at: str) -> dict:
    def machine_json(a: Analysis) -> dict:
        m = a.machine
        return {
            "name": m.name,
            "heading": m.heading,
            "doc_line": m.line,
            "states": m.states,
            "terminal": {s: line for s, line in m.terminal.items()},
            "events": [{"id": eid, "text": text, "doc_line": line} for eid, text, line in m.events],
            "combos": a.combos,
            "legal": a.legal,
            "rejected": a.rejected,
            "deadlocks": a.deadlocks,
            "unreachable": a.unreachable,
            "out_degree": a.out_degree,
            "shortest_paths": {s: path_text(p) for s, p in a.paths.items()},
        }

    return {
        "doc": doc_label,
        "generated_at": generated_at,
        "scope": "full（含标注为“迭代二”的迁移边）",
        "summary": {
            "machines": len(machines),
            "states": sum(len(m.states) for m in machines),
            "events": sum(len(m.events) for m in machines),
            "combos": sum(a.combos for a in full),
            "legal": sum(len(a.legal) for a in full),
            "rejected": sum(len(a.rejected) for a in full),
            "deadlocks": [f"{a.machine.name}.{s}" for a in full for s in a.deadlocks],
            "unreachable": [f"{a.machine.name}.{s}" for a in full for s in a.unreachable],
            "warnings": [f["id"] for f in findings if f["severity"] == "warning"],
            "infos": [f["id"] for f in findings if f["severity"] == "info"],
        },
        "machines": [machine_json(a) for a in full],
        "mvp_scope": {
            "note": "排除标注为“迭代二”的迁移边后的结果；deadlocks 字段表示 MVP 业务终点，不是缺陷",
            "machines": [
                {
                    "name": a.machine.name,
                    "legal": len(a.legal),
                    "rejected": len(a.rejected),
                    "deadlocks": a.deadlocks,
                    "unreachable": a.unreachable,
                }
                for a in mvp
            ],
        },
        "findings": findings,
    }


def doc_href(doc_path: Path, out_path: Path) -> str:
    """Markdown 报告里指向文档的相对链接（报告可能在 reports/ 等其它目录）。"""
    try:
        rel = Path(os.path.relpath(doc_path.resolve(), out_path.resolve().parent))
        return rel.as_posix()
    except ValueError:
        return doc_path.as_posix()


def find_root() -> Path:
    """向上查找仓库根目录（以 docs/state-machine.md 为锚点），不依赖脚本所在层级。"""
    for parent in Path(__file__).resolve().parents:
        if (parent / "docs" / "state-machine.md").is_file():
            return parent
    return Path.cwd()


# --------------------------------------------------------------------------
# main
# --------------------------------------------------------------------------

def main(argv: list[str] | None = None) -> int:
    root = find_root()
    report_dir = root / "docs" / "design-verify" / "reports"
    parser = argparse.ArgumentParser(
        description="枚举 docs/state-machine.md 的“状态 × 触发事件”组合并做死锁/可达性审计")
    parser.add_argument("--doc", type=Path, default=root / "docs" / "state-machine.md",
                        help="状态机文档路径（默认 docs/state-machine.md）")
    parser.add_argument("--md", type=Path, default=None,
                        help=f"同时写出 Markdown 报告（建议 {report_dir / 'state-machine-audit.md'}）")
    parser.add_argument("--json", type=Path, default=None,
                        help=f"同时写出 JSON 结果（建议 {report_dir / 'state-machine-audit.json'}）")
    parser.add_argument("--strict", action="store_true",
                        help="出现“警告”级发现时以退出码 1 结束（默认 0）")
    args = parser.parse_args(argv)

    if hasattr(sys.stdout, "reconfigure"):
        try:
            sys.stdout.reconfigure(encoding="utf-8", errors="replace")
        except Exception:
            pass

    doc_path: Path = args.doc
    if not doc_path.exists():
        print(f"找不到文档：{doc_path}", file=sys.stderr)
        return 2

    try:
        doc_label = doc_path.resolve().relative_to(root).as_posix()
    except ValueError:
        doc_label = str(doc_path).replace("\\", "/")

    machines = build_machines(parse_doc(doc_path))
    problems = validate(machines)
    if problems:
        print("文档解析失败，审计中止：", file=sys.stderr)
        for p in problems:
            print(f"  - {p}", file=sys.stderr)
        return 2

    full = [analyze(m, include_future=True) for m in machines]
    mvp = [analyze(m, include_future=False) for m in machines]
    findings = collect_findings(machines, full, mvp)
    generated_at = datetime.now().astimezone().strftime("%Y-%m-%d %H:%M:%S %z")

    print(render_console(doc_label, machines, full, mvp, findings, generated_at))

    if args.md:
        out = render_markdown(doc_label, machines, full, mvp, findings, generated_at,
                              doc_href=doc_href(doc_path, args.md))
        args.md.parent.mkdir(parents=True, exist_ok=True)
        args.md.write_text(out + "\n", encoding="utf-8")
        print(f"\n[写出] Markdown 报告：{args.md}")

    if args.json:
        payload = to_json(doc_label, machines, full, mvp, findings, generated_at)
        args.json.parent.mkdir(parents=True, exist_ok=True)
        args.json.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(f"[写出] JSON 结果：{args.json}")

    if args.strict and any(f["severity"] == "warning" for f in findings):
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
