package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNextPollAndLastPoll(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	if got := st.NextPoll(); got != 0 {
		t.Fatalf("初始应为 0：%d", got)
	}
	if err := st.SetNextPoll(120); err != nil {
		t.Fatal(err)
	}
	if got := st.NextPoll(); got != 120 {
		t.Fatalf("next_poll=%d", got)
	}
	if err := st.SetLastPoll(1700000000); err != nil {
		t.Fatal(err)
	}
	if got := st.LastPoll(); got != 1700000000 {
		t.Fatalf("last_poll=%d", got)
	}
	// 非法内容容错
	if err := os.WriteFile(filepath.Join(st.Dir, "next_poll"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := st.NextPoll(); got != 0 {
		t.Fatalf("非法内容应返回 0：%d", got)
	}
}

func TestAppliedLifecycle(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	if got := st.Applied(); len(got) != 0 {
		t.Fatalf("初始应为空：%v", got)
	}
	if err := st.SetApplied(3, 5); err != nil {
		t.Fatal(err)
	}
	if err := st.SetApplied(4, 1); err != nil {
		t.Fatal(err)
	}
	got := st.Applied()
	if len(got) != 2 || got[0].ConfigID != 3 || got[0].Version != 5 || got[1].ConfigID != 4 || got[1].Version != 1 {
		t.Fatalf("applied=%+v", got)
	}
	// 同一 config_id 只保留最新一行（顺序不敏感，与 shell agent 的 grep -v + 追加一致）
	if err := st.SetApplied(3, 9); err != nil {
		t.Fatal(err)
	}
	got = st.Applied()
	if len(got) != 2 {
		t.Fatalf("去重失败：%+v", got)
	}
	versionOf := map[int]int{}
	for _, item := range got {
		versionOf[item.ConfigID] = item.Version
	}
	if versionOf[3] != 9 || versionOf[4] != 1 {
		t.Fatalf("去重失败：%+v", got)
	}
}

func TestAppliedIgnoresInvalidLines(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	text := "3 5\nnot-a-number 1\n\n7\n3 2\n"
	if err := os.WriteFile(filepath.Join(st.Dir, "applied"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	got := st.Applied()
	if len(got) != 1 || got[0].ConfigID != 3 || got[0].Version != 5 {
		t.Fatalf("非法行未过滤或未去重：%+v", got)
	}
}

func TestEmptyDirIsNoop(t *testing.T) {
	st := Store{}
	if err := st.SetApplied(1, 1); err != nil {
		t.Fatal(err)
	}
	if len(st.Applied()) != 0 || st.NextPoll() != 0 {
		t.Fatalf("空目录不应读写")
	}
	if err := st.Ensure(); err != nil {
		t.Fatal(err)
	}
}
