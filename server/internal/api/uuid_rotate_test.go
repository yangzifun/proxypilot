package api

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yzfn/proxypilot/server/internal/auth"
	"github.com/yzfn/proxypilot/server/internal/config"
)

// =====================================================================
//  UUID 更换（轮换）单测
//
//  DB 事务用 fake DB 覆盖「校验 → 事务内迁移 → 提交后节点同步」主链路、
//  409/404/500 分支与失败时不更新 uuid_info；纯函数（UUIDv4、校验、
//  uuid_list JSON 替换、缺表识别）独立覆盖。
// =====================================================================

const (
	testRotateOldUUID = "11111111-1111-4111-8111-111111111111"
	testRotateNewUUID = "22222222-2222-4222-8222-222222222222"
)

func TestGenerateUUIDv4(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		u, err := generateUUIDv4()
		if err != nil {
			t.Fatalf("生成失败：%v", err)
		}
		if !uuidRe.MatchString(u) {
			t.Fatalf("生成的 UUID 不符合格式：%q", u)
		}
		if u[14] != '4' {
			t.Fatalf("版本位不是 4：%q", u)
		}
		switch u[19] {
		case '8', '9', 'a', 'b':
		default:
			t.Fatalf("variant 位不合法：%q", u)
		}
		if seen[u] {
			t.Fatalf("生成重复 UUID：%q", u)
		}
		seen[u] = true
	}
}

func TestValidateUUIDRotateInput(t *testing.T) {
	old := testRotateOldUUID
	newer := testRotateNewUUID
	cases := []struct {
		name     string
		oldRaw   string
		newRaw   string
		confirm  string
		wantCode int
	}{
		{"合法：指定新 UUID", old, newer, "", 0},
		{"合法：大写自动规范化", strings.ToUpper(old), strings.ToUpper(newer), "", 0},
		{"合法：confirm 大小写不敏感", old, newer, strings.ToUpper(old), 0},
		{"旧 UUID 格式无效", "not-a-uuid", newer, "", 400},
		{"新 UUID 格式无效", old, "bad", "", 400},
		{"新旧相同", old, strings.ToUpper(old), "", 400},
		{"confirm 不匹配", old, newer, "33333333-3333-4333-8333-333333333333", 400},
	}
	for _, tc := range cases {
		gotOld, gotNew, err := validateUUIDRotateInput(tc.oldRaw, tc.newRaw, tc.confirm)
		if tc.wantCode != 0 {
			if err == nil || err.Status != tc.wantCode {
				t.Fatalf("%s：期望 %d，得到 %+v", tc.name, tc.wantCode, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s：不应失败：%v", tc.name, err)
		}
		if gotOld != old || gotNew != newer {
			t.Fatalf("%s：返回值 = %q/%q", tc.name, gotOld, gotNew)
		}
	}
	// new 为空 → 自动生成 UUIDv4
	_, gen, err := validateUUIDRotateInput(old, "", "")
	if err != nil || !uuidRe.MatchString(gen) || gen == old {
		t.Fatalf("自动生成新 UUID 失败：%q %v", gen, err)
	}
}

func TestReplaceUUIDListJSON(t *testing.T) {
	old := testRotateOldUUID
	newer := testRotateNewUUID
	// 正常替换（含大小写变体）+ 去重
	raw := `["` + old + `","` + strings.ToUpper(old) + `","` + newer + `","aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"]`
	got, changed, err := replaceUUIDListJSON(raw, old, newer)
	if err != nil || !changed {
		t.Fatalf("应发生替换：changed=%v err=%v", changed, err)
	}
	var list []string
	if err := json.Unmarshal([]byte(got), &list); err != nil {
		t.Fatalf("结果不是合法 JSON：%q", got)
	}
	if len(list) != 2 {
		t.Fatalf("替换+去重后列表 = %v，期望 2 条", list)
	}
	if !strings.EqualFold(list[0], newer) || list[1] != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("替换结果 = %v", list)
	}
	// 未包含旧 UUID：原样返回（保持格式）
	raw2 := `[ "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" ]`
	got2, changed2, err := replaceUUIDListJSON(raw2, old, newer)
	if err != nil || changed2 || got2 != raw2 {
		t.Fatalf("未命中不应改动：%q changed=%v err=%v", got2, changed2, err)
	}
	// 空列表
	if _, _, err := replaceUUIDListJSON("[]", old, newer); err != nil {
		t.Fatalf("空数组报错：%v", err)
	}
	// 非法 JSON 必须报错（不能静默放行，否则旧 UUID 可能残留）
	if _, _, err := replaceUUIDListJSON("{bad", old, newer); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

func TestIsMissingTableError(t *testing.T) {
	if !isMissingTableError(errors.New("Error 1146 (42S02): Table 'proxypilot.mihomo_traffic' doesn't exist")) {
		t.Fatal("1146 should be treated as missing table")
	}
	if !isMissingTableError(errors.New("no such table: vpn_devices")) {
		t.Fatal("sqlite 风格缺表应识别")
	}
	if isMissingTableError(errors.New("Error 1062 (23000): Duplicate entry 'x' for key 'PRIMARY'")) {
		t.Fatal("重复键不是缺表")
	}
	if isMissingTableError(nil) {
		t.Fatal("nil 不是缺表")
	}
}

// 迁移表清单必须覆盖需求列出的所有 uuid 关联表，且不能有重复项。
func TestUUIDRotateTablePlanCoversRequiredTables(t *testing.T) {
	required := []string{
		"config_access_logs", "masque_warp_profiles", "mihomo_traffic", "mihomo_traffic_hourly",
		"mtproto_clients", "node_configs", "node_user_counter", "user_node_recent",
		"user_traffic", "user_traffic_hourly", "uuid_port_map", "uuid_sub_sources",
		"vpn_device_codes", "vpn_devices", "vpn_user_access",
	}
	have := map[string]bool{}
	for _, table := range uuidRotateColumnTables {
		if have[table] {
			t.Fatalf("表清单重复：%s", table)
		}
		have[table] = true
	}
	for _, table := range required {
		if !have[table] {
			t.Fatalf("迁移表清单缺少 %s", table)
		}
	}
}

func TestUUIDRotatePurposeIsolated(t *testing.T) {
	if portalUUIDRotatePurpose == portalLoginPurpose || portalUUIDRotatePurpose == portalEmailPurpose {
		t.Fatalf("换 UUID 的验证码 purpose 必须独立：%q", portalUUIDRotatePurpose)
	}
	if portalUUIDRotatePurpose != "portal_uuid_rotate" {
		t.Fatalf("purpose = %q，前端/运维约定为 portal_uuid_rotate", portalUUIDRotatePurpose)
	}
}

// rotateQueryHandler 构造轮换链路需要的 fake DB 查询响应。
// conflictTable 非空时模拟「新 UUID 已存在于该表」。
func rotateQueryHandler(conflictTable string, oldRow bool) func(string, []driver.Value) (driver.Rows, error) {
	return func(q string, args []driver.Value) (driver.Rows, error) {
		arg := func(i int) string {
			if i >= len(args) || args[i] == nil {
				return ""
			}
			return fmt.Sprint(args[i])
		}
		switch {
		case strings.Contains(q, "FROM uuid_info WHERE uuid = ?") && strings.Contains(q, "FOR UPDATE"):
			if oldRow && arg(0) == testRotateOldUUID {
				return fakeRowsSet([]string{"id"}, []driver.Value{int64(5)}), nil
			}
			return &fakeRows{}, nil
		case strings.Contains(q, "FROM uuid_info WHERE email_norm"):
			return &fakeRows{}, nil
		case strings.Contains(q, "COUNT(*) FROM configs WHERE uuid = ?"): // 旧 uuid 配置数
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(2)}), nil
		case strings.Contains(q, "COUNT(*)"): // 冲突预检 / 其它计数
			if conflictTable != "" && strings.Contains(q, "`"+conflictTable+"`") && arg(0) == testRotateNewUUID {
				return fakeRowsSet([]string{"n"}, []driver.Value{int64(1)}), nil
			}
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(0)}), nil
		case strings.Contains(q, "FROM `singbox_nodes`"), strings.Contains(q, "FROM `mihomo_nodes`"):
			return &fakeRows{}, nil
		}
		return &fakeRows{}, nil
	}
}

func fakeRotateQuery(fdb *fakeDB, conflictTable string, oldRow bool) {
	fdb.onQuery = rotateQueryHandler(conflictTable, oldRow)
}

func newRotateAdminRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/uuids/"+testRotateOldUUID+"/rotate", strings.NewReader(body))
	req.SetPathValue("uuid", testRotateOldUUID)
	return req.WithContext(context.WithValue(req.Context(), claimsKey,
		auth.Claims{Sub: "admin", Role: "admin", Typ: auth.TokenTypeAccess}))
}

// 管理端主链路：事务内迁移 + uuid_info 最后更新 + 审计 + 响应契约。
func TestUUIDRotateAdminHappyPath(t *testing.T) {
	fdb, db := newFakeDB(t)
	fakeRotateQuery(fdb, "", true)
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		return fakeResult(2, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	rec := httptest.NewRecorder()
	body := `{"new_uuid":"` + testRotateNewUUID + `","confirm":"` + testRotateOldUUID + `"}`
	h.uuidRotateAdmin(rec, newRotateAdminRequest(t, body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if out["success"] != true || out["old_uuid"] != testRotateOldUUID || out["new_uuid"] != testRotateNewUUID {
		t.Fatalf("响应 = %#v", out)
	}
	if out["configs_updated"] != float64(2) {
		t.Fatalf("configs_updated = %v，期望 2", out["configs_updated"])
	}
	// tables_updated：configs + vpn_sessions + 15 张列迁移表 + vpn_node_grants + uuid_info
	wantTables := 1 + 1 + len(uuidRotateColumnTables) + 1 + 1
	if out["tables_updated"] != float64(wantTables) {
		t.Fatalf("tables_updated = %v，期望 %d", out["tables_updated"], wantTables)
	}
	if out["nodes_updated"] != float64(0) || out["nodes_deployed"] != float64(0) {
		t.Fatalf("无节点时 nodes_updated/deployed 应为 0：%v/%v", out["nodes_updated"], out["nodes_deployed"])
	}
	sub, _ := out["sub"].(map[string]any)
	if sub == nil || !strings.Contains(fmt.Sprint(sub["raw"]), "/sub/"+testRotateNewUUID) {
		t.Fatalf("sub.raw 不正确：%v", out["sub"])
	}
	if !strings.Contains(fmt.Sprint(sub["batch"]), "/batch-configs/"+testRotateNewUUID) ||
		!strings.Contains(fmt.Sprint(sub["clash"]), "uuid="+testRotateNewUUID) {
		t.Fatalf("sub.batch/clash 不正确：%v", out["sub"])
	}

	// configs 迁移 SQL：uuid 列 + config_data 内嵌替换
	cfgCall, ok := fdb.lastExec("UPDATE configs SET uuid")
	if !ok || !strings.Contains(cfgCall.Query, "REPLACE(config_data") {
		t.Fatalf("configs 迁移 SQL 未替换 config_data：%#v", cfgCall)
	}
	// uuid_info 必须最后更新（按执行顺序在所有表迁移之后）
	calls := fdb.execCalls()
	cfgIdx, infoIdx := -1, -1
	for i, c := range calls {
		switch {
		case strings.Contains(c.Query, "UPDATE configs SET uuid"):
			cfgIdx = i
		case strings.Contains(c.Query, "UPDATE uuid_info SET uuid"):
			infoIdx = i
		}
	}
	if infoIdx <= cfgIdx || infoIdx < 0 {
		t.Fatalf("uuid_info 更新顺序不正确：configs=%d info=%d", cfgIdx, infoIdx)
	}
	// 审计
	if fdb.countExec("INSERT INTO vpn_audit") != 1 {
		t.Fatal("缺少 UUID 轮换审计记录")
	}
}

func TestUUIDRotateAdminBadRequests(t *testing.T) {
	// confirm 不匹配：不访问 DB
	fdb, db := newFakeDB(t)
	h := &handlers{db: db, log: testLogger()}
	rec := httptest.NewRecorder()
	body := `{"new_uuid":"` + testRotateNewUUID + `","confirm":"33333333-3333-4333-8333-333333333333"}`
	h.uuidRotateAdmin(rec, newRotateAdminRequest(t, body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("confirm 不匹配应 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
	if len(fdb.execCalls()) != 0 {
		t.Fatal("参数错误不应执行任何 SQL")
	}
	// 非法 JSON
	rec = httptest.NewRecorder()
	h.uuidRotateAdmin(rec, newRotateAdminRequest(t, "{bad"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON 应 400，得到 %d", rec.Code)
	}
}

func TestUUIDRotateAdminNotFound(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "FROM uuid_info WHERE uuid = ?"):
			return &fakeRows{}, nil // 没有 uuid_info
		case strings.Contains(q, "COUNT(*) FROM configs WHERE uuid = ?"):
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(0)}), nil // 也没有配置
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	rec := httptest.NewRecorder()
	h.uuidRotateAdmin(rec, newRotateAdminRequest(t, `{"new_uuid":"`+testRotateNewUUID+`"}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在的旧 UUID 应 404，得到 %d：%s", rec.Code, rec.Body.String())
	}
}

func TestUUIDRotateAdminConflict(t *testing.T) {
	fdb, db := newFakeDB(t)
	fakeRotateQuery(fdb, "configs", true)
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	h := &handlers{db: db, log: testLogger()}
	rec := httptest.NewRecorder()
	h.uuidRotateAdmin(rec, newRotateAdminRequest(t, `{"new_uuid":"`+testRotateNewUUID+`"}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("新 UUID 已存在应 409，得到 %d：%s", rec.Code, rec.Body.String())
	}
	if fdb.countExec("UPDATE configs SET uuid") != 0 {
		t.Fatal("冲突时不应执行迁移")
	}
}

// 事务内迁移失败 → 500，且不会更新 uuid_info、不会触发节点同步。
func TestRotateUUIDServiceFailureDoesNotTouchUUIDInfo(t *testing.T) {
	fdb, db := newFakeDB(t)
	fakeRotateQuery(fdb, "", true)
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "UPDATE configs SET uuid") {
			return nil, errors.New("disk full")
		}
		return fakeResult(1, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	_, rerr := h.rotateUUIDService(context.Background(), testRotateOldUUID, testRotateNewUUID, "admin")
	if rerr == nil || rerr.Status != http.StatusInternalServerError {
		t.Fatalf("迁移失败应 500，得到 %+v", rerr)
	}
	if fdb.countExec("UPDATE uuid_info SET uuid") != 0 {
		t.Fatal("失败回滚路径不应更新 uuid_info")
	}
	for _, call := range fdb.queryCalls() {
		if strings.Contains(call.Query, "FROM `singbox_nodes`") {
			t.Fatal("失败时不应查询/同步节点")
		}
	}
}

func TestUUIDRotateSubLinks(t *testing.T) {
	sub := uuidRotateSubLinks("https://panel.example.com/", testRotateNewUUID)
	if sub["raw"] != "https://panel.example.com/sub/"+testRotateNewUUID {
		t.Fatalf("raw = %q", sub["raw"])
	}
	if sub["batch"] != "https://panel.example.com/batch-configs/"+testRotateNewUUID {
		t.Fatalf("batch = %q", sub["batch"])
	}
	if sub["clash"] != "https://panel.example.com/clash-config?uuid="+testRotateNewUUID {
		t.Fatalf("clash = %q", sub["clash"])
	}
}

// ---------------- 用户端：必须二次认证，不能只凭 portal token ----------------

func newPortalRotateHandlers(t *testing.T, email string) (*handlers, string) {
	t.Helper()
	h := &handlers{
		cfg: config.Config{JWTSecret: "test-secret", PortalTokenTTL: time.Hour},
		log: testLogger(),
	}
	token, err := auth.IssuePortalTokenTTL(email, h.cfg.JWTSecret, h.cfg.PortalTokenTTL)
	if err != nil {
		t.Fatalf("签发 portal token 失败：%v", err)
	}
	return h, token
}

func portalRotateRequest(t *testing.T, token, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/portal/uuid/rotate", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// 有密码账号只带 token（无密码）→ 400（二次证明缺失，不是会话过期），绝不迁移。
func TestPortalUUIDRotateRequiresPassword(t *testing.T) {
	const email = "rotate-pwd@example.com"
	h, token := newPortalRotateHandlers(t, email)
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatalf("构造密码哈希失败：%v", err)
	}
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM uuid_info WHERE email_norm") {
			return fakeRowsSet(
				[]string{"uuid", "email", "password_hash", "password_must_change"},
				[]driver.Value{testRotateOldUUID, email, hash, int64(0)},
			), nil
		}
		return &fakeRows{}, nil
	}
	h.db = db
	rec := httptest.NewRecorder()
	h.portalUUIDRotate(rec, portalRotateRequest(t, token, `{}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("只凭 token 应 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
	if fdb.countExec("UPDATE configs SET uuid") != 0 {
		t.Fatal("未通过二次认证不应迁移")
	}
}

// 有密码账号密码正确 → 通过二次认证（进入迁移；此处旧 uuid 不存在 → 404）。
func TestPortalUUIDRotatePasswordProofPasses(t *testing.T) {
	const email = "rotate-pwd2@example.com"
	h, token := newPortalRotateHandlers(t, email)
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatalf("构造密码哈希失败：%v", err)
	}
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "FROM uuid_info WHERE email_norm"):
			return fakeRowsSet(
				[]string{"uuid", "email", "password_hash", "password_must_change"},
				[]driver.Value{testRotateOldUUID, email, hash, int64(0)},
			), nil
		case strings.Contains(q, "FROM uuid_info WHERE uuid = ?"):
			return &fakeRows{}, nil
		case strings.Contains(q, "COUNT(*) FROM configs WHERE uuid = ?"):
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(0)}), nil
		}
		return &fakeRows{}, nil
	}
	h.db = db
	rec := httptest.NewRecorder()
	h.portalUUIDRotate(rec, portalRotateRequest(t, token, `{"password":"correct-password"}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("密码正确应进入迁移并因旧 UUID 不存在返回 404，得到 %d：%s", rec.Code, rec.Body.String())
	}
}

// 无密码账号：既不传验证码 → 400；验证码错误 → 400/401（consumeEmailCode 判定）。
func TestPortalUUIDRotateRequiresEmailCode(t *testing.T) {
	const email = "rotate-nocode@example.com"
	h, token := newPortalRotateHandlers(t, email)
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM uuid_info WHERE email_norm") {
			return fakeRowsSet(
				[]string{"uuid", "email", "password_hash", "password_must_change"},
				[]driver.Value{testRotateOldUUID, email, "", int64(0)},
			), nil
		}
		// consumeEmailCode：没有可用验证码
		return &fakeRows{}, nil
	}
	h.db = db
	rec := httptest.NewRecorder()
	h.portalUUIDRotate(rec, portalRotateRequest(t, token, `{}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("无密码且无验证码应 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.portalUUIDRotate(rec, portalRotateRequest(t, token, `{"code":"123456"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("验证码不存在应 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
	if fdb.countExec("UPDATE configs SET uuid") != 0 {
		t.Fatal("验证码不通过不应迁移")
	}
}

// ---------------- 节点授权列表迁移 / 节点同步 ----------------

// 节点 uuid_list JSON 迁移：只有包含旧 UUID 的节点被更新，返回新列表用于下发。
func TestRotateUUIDListTableMigratesNodes(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM `singbox_nodes`") {
			return fakeRowsSet([]string{"id", "uuid_list"},
				[]driver.Value{int64(7), `["` + testRotateOldUUID + `","aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"]`},
				[]driver.Value{int64(8), `["bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"]`},
			), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	h := &handlers{db: db, log: testLogger(), cfg: config.Config{}}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("开启事务失败：%v", err)
	}
	defer tx.Rollback()
	ids, next, err := h.rotateUUIDListTable(context.Background(), tx, "singbox_nodes",
		testRotateOldUUID, testRotateNewUUID, nowMillis())
	if err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	if len(ids) != 1 || ids[0] != 7 {
		t.Fatalf("受影响节点 = %v，期望 [7]", ids)
	}
	if !containsUUIDFold(next[7], testRotateNewUUID) || containsUUIDFold(next[7], testRotateOldUUID) {
		t.Fatalf("节点 7 新列表不正确：%v", next[7])
	}
	call, ok := fdb.lastExec("UPDATE `singbox_nodes` SET uuid_list")
	if !ok {
		t.Fatal("未更新 singbox_nodes.uuid_list")
	}
	raw, _ := call.Args[0].(string)
	if !strings.Contains(strings.ToLower(raw), testRotateNewUUID) || strings.Contains(strings.ToLower(raw), testRotateOldUUID) {
		t.Fatalf("写入的 uuid_list 不正确：%s", raw)
	}
}

// 事务提交后节点同步失败：只汇总 warning，不阻断（deployed=0）。
func TestSyncRotatedUUIDNodesFailureIsWarning(t *testing.T) {
	_, db := newFakeDB(t)
	h := &handlers{db: db, log: testLogger(), cfg: config.Config{}}
	deployed, warns := h.syncRotatedUUIDNodes(context.Background(), []int64{7, 8},
		map[int64][]string{7: {testRotateNewUUID}, 8: {testRotateNewUUID}}, "admin")
	if deployed != 0 {
		t.Fatalf("节点不存在时 deployed = %d，期望 0", deployed)
	}
	if len(warns) != 2 || !strings.Contains(warns[0], "节点 #7") || !strings.Contains(warns[1], "节点 #8") {
		t.Fatalf("warning 汇总不正确：%v", warns)
	}
}

// 用户端验证码路径成功：验证码消费 → 迁移 → 审计 → 响应新订阅链接。
func TestPortalUUIDRotateWithEmailCodeSuccess(t *testing.T) {
	const email = "rotate-code@example.com"
	h, token := newPortalRotateHandlers(t, email)
	const code = "123456"
	codeHash := hashEmailCode(email, code, portalUUIDRotatePurpose)
	fdb, db := newFakeDB(t)
	base := rotateQueryHandler("", true)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "FROM admin_email_codes"):
			return fakeRowsSet([]string{"id", "code_hash", "expires_at", "attempts"},
				[]driver.Value{int64(1), codeHash, nowMillis() + 600_000, int64(0)}), nil
		case strings.Contains(q, "FROM uuid_info WHERE email_norm"):
			return fakeRowsSet(
				[]string{"uuid", "email", "password_hash", "password_must_change"},
				[]driver.Value{testRotateOldUUID, email, "", int64(0)},
			), nil
		}
		return base(q, args)
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	h.db = db
	rec := httptest.NewRecorder()
	h.portalUUIDRotate(rec, portalRotateRequest(t, token, `{"code":"`+code+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("验证码正确应成功，得到 %d：%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if out["old_uuid"] != testRotateOldUUID {
		t.Fatalf("old_uuid = %v", out["old_uuid"])
	}
	newUUID := fmt.Sprint(out["new_uuid"])
	if !uuidRe.MatchString(newUUID) || newUUID == testRotateOldUUID {
		t.Fatalf("new_uuid = %q", newUUID)
	}
	sub, _ := out["sub"].(map[string]any)
	if sub == nil || !strings.Contains(fmt.Sprint(sub["raw"]), "/sub/"+newUUID) {
		t.Fatalf("sub.raw 不正确：%v", out["sub"])
	}
	if fdb.countExec("UPDATE configs SET uuid") != 1 {
		t.Fatal("验证码通过后应执行迁移")
	}
	if fdb.countExec("INSERT INTO vpn_audit") != 1 {
		t.Fatal("用户端轮换缺少审计记录")
	}
}
func TestPortalUUIDRotateSendCodeRejectsPasswordAccount(t *testing.T) {
	const email = "rotate-sendcode@example.com"
	h, token := newPortalRotateHandlers(t, email)
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatalf("构造密码哈希失败：%v", err)
	}
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "FROM uuid_info WHERE LOWER(email)"):
			return fakeRowsSet([]string{"uuid", "email"}, []driver.Value{testRotateOldUUID, email}), nil
		case strings.Contains(q, "FROM uuid_info WHERE email_norm"):
			return fakeRowsSet(
				[]string{"uuid", "email", "password_hash", "password_must_change"},
				[]driver.Value{testRotateOldUUID, email, hash, int64(0)},
			), nil
		}
		return &fakeRows{}, nil
	}
	h.db = db
	req := httptest.NewRequest(http.MethodPost, "/api/portal/uuid/rotate/send-code", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.portalUUIDRotateSendCode(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("有密码账号发码应 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
	if fdb.countExec("INSERT INTO admin_email_codes") != 0 {
		t.Fatal("不应写入验证码")
	}
}
