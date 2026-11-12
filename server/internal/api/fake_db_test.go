package api

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// =====================================================================
// 极简 fake database/sql 驱动（测试专用，不引入第三方依赖）
//
// 用途：给 jobs/cron/serverDelete/settings 这些「纯 SQL 编排」逻辑做可复现的
// 单测——断言真正执行了什么 SQL、参数是什么，而不是只看常量字符串。
// 只有本包测试使用；生产代码不感知。
// =====================================================================

type fakeSQLCall struct {
	Query string
	Args  []any
}

type fakeDB struct {
	mu      sync.Mutex
	execs   []fakeSQLCall
	queries []fakeSQLCall
	onExec  func(query string, args []driver.Value) (driver.Result, error)
	onQuery func(query string, args []driver.Value) (driver.Rows, error)
}

func (f *fakeDB) execCalls() []fakeSQLCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeSQLCall, len(f.execs))
	copy(out, f.execs)
	return out
}

func (f *fakeDB) queryCalls() []fakeSQLCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeSQLCall, len(f.queries))
	copy(out, f.queries)
	return out
}

// countExec 统计 SQL 文本包含 substr 的执行次数。
func (f *fakeDB) countExec(substr string) int {
	n := 0
	for _, c := range f.execCalls() {
		if strings.Contains(c.Query, substr) {
			n++
		}
	}
	return n
}

// lastExec 返回最后一条包含 substr 的执行记录。
func (f *fakeDB) lastExec(substr string) (fakeSQLCall, bool) {
	calls := f.execCalls()
	for i := len(calls) - 1; i >= 0; i-- {
		if strings.Contains(calls[i].Query, substr) {
			return calls[i], true
		}
	}
	return fakeSQLCall{}, false
}

func fakeArgsAny(args []driver.Value) []any {
	out := make([]any, len(args))
	for i := range args {
		out[i] = args[i]
	}
	return out
}

func (f *fakeDB) runExec(query string, args []driver.Value) (driver.Result, error) {
	anyArgs := fakeArgsAny(args)
	f.mu.Lock()
	f.execs = append(f.execs, fakeSQLCall{Query: query, Args: anyArgs})
	handler := f.onExec
	f.mu.Unlock()
	if handler != nil {
		return handler(query, args)
	}
	return fakeSQLResult{}, nil
}

func (f *fakeDB) runQuery(query string, args []driver.Value) (driver.Rows, error) {
	anyArgs := fakeArgsAny(args)
	f.mu.Lock()
	f.queries = append(f.queries, fakeSQLCall{Query: query, Args: anyArgs})
	handler := f.onQuery
	f.mu.Unlock()
	if handler != nil {
		return handler(query, args)
	}
	return &fakeRows{}, nil
}

type fakeSQLResult struct {
	lastID   int64
	affected int64
	defaults bool
}

func (r fakeSQLResult) LastInsertId() (int64, error) {
	if r.defaults {
		return 1, nil
	}
	return r.lastID, nil
}

func (r fakeSQLResult) RowsAffected() (int64, error) {
	if r.defaults {
		return 1, nil
	}
	return r.affected, nil
}

func fakeResult(affected, lastID int64) driver.Result {
	return fakeSQLResult{lastID: lastID, affected: affected}
}

// fakeRows 按列 + 行数据返回结果集。
type fakeRows struct {
	cols []string
	data [][]driver.Value
	idx  int
}

func (r *fakeRows) Columns() []string { return r.cols }

func (r *fakeRows) Close() error { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.idx])
	r.idx++
	return nil
}

func fakeRowsSet(cols []string, rows ...[]driver.Value) driver.Rows {
	return &fakeRows{cols: cols, data: rows}
}

// fakeRowFromMap 按给定列名从 map 取一行（缺列返回 nil）。
func fakeRowSet(cols []string, row map[string]any) driver.Rows {
	vals := make([]driver.Value, len(cols))
	for i, c := range cols {
		switch v := row[c].(type) {
		case int64:
			vals[i] = v
		case int:
			vals[i] = int64(v)
		case string:
			vals[i] = v
		case nil:
			vals[i] = nil
		}
	}
	return &fakeRows{cols: cols, data: [][]driver.Value{vals}}
}

type fakeDriver struct{}

func (fakeDriver) Open(dsn string) (driver.Conn, error) {
	v, ok := fakeDBRegistry.Load(dsn)
	if !ok {
		return nil, fmt.Errorf("fake db %q 未注册", dsn)
	}
	return &fakeConn{db: v.(*fakeDB)}, nil
}

type fakeConn struct {
	db *fakeDB
}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	return &fakeStmt{conn: c, query: query}, nil
}

func (c *fakeConn) Close() error { return nil }

func (c *fakeConn) Begin() (driver.Tx, error) { return fakeTx{}, nil }

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeStmt struct {
	conn  *fakeConn
	query string
}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }

func (s *fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.conn.db.runExec(s.query, args)
}

func (s *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.conn.db.runQuery(s.query, args)
}

var (
	fakeDriverRegisterOnce sync.Once
	fakeDBSeq              atomic.Int64
	fakeDBRegistry         sync.Map // dsn -> *fakeDB
)

// newFakeDB 创建一套 fake 数据库；t.Cleanup 时自动清理。
func newFakeDB(t *testing.T) (*fakeDB, *sql.DB) {
	t.Helper()
	fakeDriverRegisterOnce.Do(func() {
		sql.Register("pp_fake", fakeDriver{})
	})
	dsn := "fakedb-" + strconv.FormatInt(fakeDBSeq.Add(1), 10)
	fdb := &fakeDB{}
	fakeDBRegistry.Store(dsn, fdb)
	db, err := sql.Open("pp_fake", dsn)
	if err != nil {
		t.Fatalf("打开 fake DB 失败：%v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		fakeDBRegistry.Delete(dsn)
	})
	return fdb, db
}

// testLogger 丢弃日志，避免测试输出噪声。
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
