#!/usr/bin/env python3
"""把 D1(SQLite) 导出转换为 MySQL 8 的 INSERT 脚本。

用法：
    python3 d1_to_mysql.py --sqlite d1.db --out d1_mysql.sql [--tables configs,servers]
    python3 d1_to_mysql.py --d1-sql d1.sql --out d1_mysql.sql   # 直接用 wrangler d1 export 的 SQL

规则：
  · 只输出 INSERT，不建表（建表用 migrations/0001_init.sql）
  · 标识符全部加反引号（`key` 是 MySQL 保留字）
  · 时间戳列统一成 13 位毫秒：秒级值（< 1e11）自动 ×1000
  · 空串保留为 ''；NULL 输出 NULL；非法 JSON 不处理（交给数据库）
  · 目标端多出的生成列（email_norm）自动跳过
"""
from __future__ import annotations

import argparse
import pathlib
import sqlite3
import sys
from typing import Any, Iterable

TS_COLUMNS = {
    "last_accessed",
    "agent_last_seen",
    "agent_last_poll_at",
    "agent_key_created_at",
    "agent_key_used_at",
    "last_login_at",
    "last_used_at",
    "last_notify_at",
}
TS_SUFFIX = "_at"
SKIP_TARGET_ONLY = {"email_norm"}
# 目标 schema 不再创建的遗留表：如果照搬 INSERT，导入会因 “table doesn't exist” 失败
LEGACY_TABLES = {"cfips", "cf_domains", "edgeone_domains", "auto_update_settings"}
SKIP_TABLES = {"sqlite_sequence"} | LEGACY_TABLES


def is_timestamp(col: str) -> bool:
    if col in TS_COLUMNS:
        return True
    return col.endswith(TS_SUFFIX)


def norm_ts(value: Any) -> Any:
    if not isinstance(value, int):
        return value
    if value > 0 and value < 100_000_000_000:  # 秒级
        return value * 1000
    return value


def sql_literal(value: Any) -> str:
    if value is None:
        return "NULL"
    if isinstance(value, bool):
        return "1" if value else "0"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, float):
        if value != value or value in (float("inf"), float("-inf")):
            return "NULL"
        return repr(value)
    if isinstance(value, (bytes, bytearray)):
        return "X'" + bytes(value).hex() + "'"
    s = str(value)
    s = s.replace("\\", "\\\\").replace("'", "\\'")
    s = s.replace("\x00", "\\0").replace("\r", "\\r").replace("\n", "\\n")
    return "'" + s + "'"


def quote_ident(name: str) -> str:
    return "`" + name.replace("`", "``") + "`"


def table_columns(conn: sqlite3.Connection, table: str) -> list[str]:
    rows = conn.execute(f"PRAGMA table_info({quote_ident_sqlite(table)})").fetchall()
    return [r[1] for r in rows]


def quote_ident_sqlite(name: str) -> str:
    return '"' + name.replace('"', '""') + '"'


def iter_inserts(conn: sqlite3.Connection, table: str, batch_size: int = 200) -> Iterable[str]:
    cols = [c for c in table_columns(conn, table) if c not in SKIP_TARGET_ONLY]
    if not cols:
        return
    collist = ", ".join(quote_ident(c) for c in cols)
    select = ", ".join(quote_ident_sqlite(c) for c in cols)
    cur = conn.execute(f"SELECT {select} FROM {quote_ident_sqlite(table)}")
    batch: list[str] = []
    total = 0
    for row in cur:
        values = []
        for col, val in zip(cols, row):
            if is_timestamp(col):
                val = norm_ts(val)
            values.append(sql_literal(val))
        batch.append("(" + ", ".join(values) + ")")
        total += 1
        if len(batch) >= batch_size:
            yield f"INSERT INTO {quote_ident(table)} ({collist}) VALUES\n  " + ",\n  ".join(batch) + ";\n"
            batch = []
    if batch:
        yield f"INSERT INTO {quote_ident(table)} ({collist}) VALUES\n  " + ",\n  ".join(batch) + ";\n"


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description="D1(SQLite) → MySQL INSERT 转换")
    src = ap.add_mutually_exclusive_group(required=True)
    src.add_argument("--sqlite", help="D1 导出后还原的 sqlite 文件")
    src.add_argument("--d1-sql", help="wrangler d1 export 生成的 .sql（内部会载入临时 SQLite）")
    ap.add_argument("--out", required=True, help="输出的 .sql 文件")
    ap.add_argument("--tables", default="", help="逗号分隔；默认全部业务表")
    ap.add_argument("--truncate", action="store_true", help="每表导入前先 TRUNCATE（用于整库重导）")
    args = ap.parse_args(argv)

    if args.d1_sql:
        import tempfile
        sql_text = pathlib.Path(args.d1_sql).read_text(encoding="utf-8", errors="replace")
        tmp = tempfile.NamedTemporaryFile(prefix="pp-d1-", suffix=".db", delete=False)
        tmp.close()
        conn = sqlite3.connect(tmp.name)
        try:
            conn.executescript(sql_text)
        except sqlite3.Error as exc:
            print(f"载入 D1 SQL 失败：{exc}", file=sys.stderr)
            return 2
    else:
        conn = sqlite3.connect(args.sqlite)
    conn.text_factory = lambda b: b.decode("utf-8", "replace")

    all_tables = [
        r[0]
        for r in conn.execute(
            "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name"
        ).fetchall()
        if not r[0].startswith("sqlite_") and r[0] not in SKIP_TABLES
    ]
    wanted = [t.strip() for t in args.tables.split(",") if t.strip()]
    tables = [t for t in all_tables if not wanted or t in wanted]
    missing = [t for t in wanted if t not in all_tables]
    if missing:
        print(f"警告：源库中不存在这些表，已跳过：{', '.join(missing)}", file=sys.stderr)

    with open(args.out, "w", encoding="utf-8", newline="\n") as fh:
        fh.write("-- 由 d1_to_mysql.py 生成\n")
        fh.write("SET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS=0;\n\n")
        for table in tables:
            try:
                count = conn.execute(f"SELECT COUNT(*) FROM {quote_ident_sqlite(table)}").fetchone()[0]
            except sqlite3.Error as exc:
                print(f"跳过 {table}: {exc}", file=sys.stderr)
                continue
            fh.write(f"-- {table}: {count} 行\n")
            if args.truncate:
                fh.write(f"TRUNCATE TABLE {quote_ident(table)};\n")
            fh.write("START TRANSACTION;\n")
            for stmt in iter_inserts(conn, table):
                fh.write(stmt)
            fh.write("COMMIT;\n\n")
            print(f"{table:24s} {count:8d} 行")
        fh.write("SET FOREIGN_KEY_CHECKS=1;\n")
    conn.close()
    print(f"已写入 {args.out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
