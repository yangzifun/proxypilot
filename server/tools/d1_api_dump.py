#!/usr/bin/env python3
"""通过 Cloudflare D1 HTTP API 只读导出数据，生成 MySQL INSERT。

为什么需要它：`wrangler d1 export` 需要 D1 Export 专属权限；只有 D1 Read 时，
可以用本脚本按表 SELECT（keyset 分页），完全不写线上数据。

用法：
    export CF_TOKEN=...
    mysql -N -B -e "SELECT table_name,column_name FROM information_schema.columns
        WHERE table_schema='proxypilot_migrate' ORDER BY table_name,ordinal_position" \
        > target-columns.tsv
    python3 d1_api_dump.py --account <account_id> --database <d1_id> \
        --target-columns target-columns.tsv --out d1-import.sql
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

TS_COLUMNS = {"last_accessed"}
SKIP = {"_cf_KV", "sqlite_sequence", "cfips", "cf_domains", "edgeone_domains",
        "auto_update_settings", "aliyun_esa_domains", "ip_geo", "schema_migrations"}


def qident(name: str) -> str:
    return '"' + name.replace('"', '""') + '"'


def mysql_ident(name: str) -> str:
    return "`" + name.replace("`", "``") + "`"


def lit(value) -> str:
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
    s = str(value)
    s = s.replace("\\", "\\\\").replace("'", "\\'")
    s = s.replace("\x00", "\\0").replace("\r", "\\r").replace("\n", "\\n")
    return "'" + s + "'"


def norm_ts(col: str, value):
    if isinstance(value, int) and (col in TS_COLUMNS or col.endswith("_at")):
        if 0 < value < 100_000_000_000:
            return value * 1000
    return value


def load_target(path: Path) -> dict[str, list[str]]:
    out: dict[str, list[str]] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        table, col = line.split("\t", 1)
        out.setdefault(table, []).append(col)
    return out


class D1:
    def __init__(self, account: str, database: str):
        self.token = os.environ.get("CF_TOKEN") or os.environ.get("CLOUDFLARE_API_TOKEN", "")
        if not self.token:
            raise SystemExit("缺少 CF_TOKEN / CLOUDFLARE_API_TOKEN")
        self.url = (f"https://api.cloudflare.com/client/v4/accounts/{account}"
                    f"/d1/database/{database}/query")

    def query(self, sql: str) -> list[dict]:
        body = json.dumps({"sql": sql}).encode()
        for attempt in range(4):
            req = urllib.request.Request(self.url, data=body, method="POST", headers={
                "Authorization": "Bearer " + self.token,
                "Content-Type": "application/json",
            })
            try:
                with urllib.request.urlopen(req, timeout=120) as r:
                    data = json.loads(r.read().decode())
                if not data.get("success"):
                    raise RuntimeError(str(data.get("errors")))
                return data["result"][0].get("results", [])
            except (urllib.error.HTTPError, urllib.error.URLError, TimeoutError) as exc:
                if attempt == 3:
                    raise
                print(f"  retry {attempt + 1}: {exc}", file=sys.stderr)
                time.sleep(2 * (attempt + 1))
        return []


def main() -> int:
    ap = argparse.ArgumentParser(description="D1 HTTP API → MySQL INSERT（只读）")
    ap.add_argument("--account", required=True)
    ap.add_argument("--database", required=True)
    ap.add_argument("--target-columns", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--batch", type=int, default=200)
    args = ap.parse_args()

    d1 = D1(args.account, args.database)
    target = load_target(Path(args.target_columns))
    src_tables = {r["name"] for r in d1.query(
        "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")}
    tables = sorted((set(target) & src_tables) - SKIP)

    total_rows = 0
    with open(args.out, "w", encoding="utf-8", newline="\n") as fh:
        fh.write("-- D1 HTTP API -> MySQL INSERT（只读导出）\nSET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS=0;\n\n")
        for table in tables:
            src_cols = {r["name"] for r in d1.query(f"PRAGMA table_info({qident(table)})")}
            cols = [c for c in target[table] if c in src_cols]
            if not cols:
                print(f"{table:26s} 跳过（无共同列）", file=sys.stderr)
                continue
            sel = ", ".join(qident(c) for c in cols)
            fh.write(f"-- {table}\nTRUNCATE TABLE {mysql_ident(table)};\n")
            has_rowid = True
            try:
                d1.query(f"SELECT rowid FROM {qident(table)} LIMIT 1")
            except Exception:
                has_rowid = False
            row_count, last_rid, offset = 0, 0, 0
            while True:
                if has_rowid:
                    rows = d1.query(f"SELECT rowid AS __rid, {sel} FROM {qident(table)} "
                                    f"WHERE rowid > {last_rid} ORDER BY rowid LIMIT {args.batch}")
                else:
                    rows = d1.query(f"SELECT {sel} FROM {qident(table)} LIMIT {args.batch} OFFSET {offset}")
                if not rows:
                    break
                values = []
                for row in rows:
                    values.append("(" + ", ".join(lit(norm_ts(c, row.get(c))) for c in cols) + ")")
                    if has_rowid:
                        last_rid = int(row.get("__rid") or last_rid)
                fh.write("INSERT INTO " + mysql_ident(table) + " (" +
                         ", ".join(mysql_ident(c) for c in cols) + ") VALUES\n  " +
                         ",\n  ".join(values) + ";\n")
                row_count += len(rows)
                total_rows += len(rows)
                offset += len(rows)
            print(f"{table:26s} {row_count:8d} 行")
            fh.write("\n")
        fh.write("SET FOREIGN_KEY_CHECKS=1;\n")
    print(f"完成：{len(tables)} 张表，{total_rows} 行 -> {args.out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
