package db

import "testing"

func TestSplitStatements(t *testing.T) {
	src := `
-- leading comment; with semicolon
CREATE TABLE t (
  id BIGINT PRIMARY KEY,
  note VARCHAR(10) DEFAULT 'a;b'
);
/* block ; comment */
INSERT INTO t VALUES (1, 'x;y');
SET NAMES utf8mb4;
`
	got := SplitStatements(src)
	want := []string{
		"CREATE TABLE t (\n  id BIGINT PRIMARY KEY,\n  note VARCHAR(10) DEFAULT 'a;b'\n)",
		"INSERT INTO t VALUES (1, 'x;y')",
		"SET NAMES utf8mb4",
	}
	if len(got) != len(want) {
		t.Fatalf("语句数量 = %d, 期望 %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 条 =\n%q\n期望\n%q", i+1, got[i], want[i])
		}
	}
}

func TestSplitStatementsBacktick(t *testing.T) {
	got := SplitStatements("ALTER TABLE `t` ADD `key` VARCHAR(10);")
	if len(got) != 1 || got[0] != "ALTER TABLE `t` ADD `key` VARCHAR(10)" {
		t.Fatalf("unexpected: %#v", got)
	}
}
