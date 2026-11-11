package vpn

import "testing"

// 级联删除的语义：删节点必须同时清掉它的授权与会话。
//
// 这是「节点删了但授权还在」那个 bug 的回归守卫。真实 SQL 需要数据库，
// 这里只断言语句内容与顺序 —— 子表必须先于主表，否则会留孤儿行。
func TestDeleteNodeCascadeSQL(t *testing.T) {
	want := []string{
		"vpn_node_grants",
		"vpn_sessions",
		"vpn_nodes",
	}
	got := deleteNodeStatements()
	if len(got) != len(want) {
		t.Fatalf("级联语句数量 = %d，期望 %d", len(got), len(want))
	}
	for i, table := range want {
		if !contains(got[i], table) {
			t.Fatalf("第 %d 条语句缺少表 %s：%s", i, table, got[i])
		}
	}
	// 主表必须最后删
	if !contains(got[len(got)-1], "DELETE FROM vpn_nodes") {
		t.Fatalf("最后一条必须删主表 vpn_nodes，实际：%s", got[len(got)-1])
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
