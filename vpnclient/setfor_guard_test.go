package main

import (
	"os"
	"strings"
	"testing"
)

// 回归守卫：setFor 内部必须调用 s.state.set，绝不能调用自己。
//
// 真实事故（2.22 - 2.26）：批量把 connect 里的 s.state.set 换成 s.setFor(attempt, …)
// 时，替换范围把 setFor 自己的函数体也包含进去了，于是变成
//
//	func (s *uiServer) setFor(...) { s.setFor(attempt, ...) }
//
// 无限递归 → 栈溢出 → runtime.throw → 进程直接消失。
// 用户看到的就是「点连接闪退」，而且没有任何日志。
func TestSetForDoesNotRecurse(t *testing.T) {
	raw, err := os.ReadFile("ui.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	idx := strings.Index(src, "func (s *uiServer) setFor(")
	if idx < 0 {
		t.Fatal("找不到 setFor 定义")
	}
	// 取函数体（到下一个顶层 func 之前）
	rest := src[idx:]
	end := strings.Index(rest[len("func (s *uiServer) setFor("):], "\nfunc ")
	if end < 0 {
		t.Fatal("找不到 setFor 的结束位置")
	}
	body := rest[:len("func (s *uiServer) setFor(")+end]

	if strings.Contains(body, "s.setFor(") {
		t.Fatalf("setFor 内部又调用了自己，会造成无限递归（栈溢出 → 进程闪退）：\n%s", body)
	}
	if !strings.Contains(body, "s.state.set(") {
		t.Fatalf("setFor 应该通过 s.state.set 写状态，实际找不到：\n%s", body)
	}
}

// 每个 s.setFor(attempt, …) 调用点都必须真的在一个「有 attempt 变量」的作用域里。
// 这里只做粗检：connect 函数体内出现的 setFor 调用数量应当 > 0。
func TestSetForUsedInConnect(t *testing.T) {
	raw, err := os.ReadFile("ui.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	cs := strings.Index(src, "func (s *uiServer) connect(")
	if cs < 0 {
		t.Fatal("找不到 connect")
	}
	ce := strings.Index(src[cs:], "\nfunc (s *uiServer) fail(")
	if ce < 0 {
		t.Fatal("找不到 connect 结束")
	}
	body := src[cs : cs+ce]
	if n := strings.Count(body, "s.setFor(attempt,"); n == 0 {
		t.Fatal("connect 里没有任何 setFor 调用，attempt 序号机制可能失效")
	}
	if strings.Contains(body, "s.state.set(") && !strings.Contains(body, "st.attempt = attempt") {
		t.Fatal("connect 里应当先写入 attempt 再使用 setFor")
	}
}