package toolkit

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"sync"
	"testing"

	"github.com/jd-opensource/joytoken-sdk-go/agent"
)

func TestIsReadOnlyStatement(t *testing.T) {
	readOnly := []string{
		"SELECT 1",
		"  select * from t",
		"WITH x AS (SELECT 1) SELECT * FROM x",
		"EXPLAIN SELECT 1",
		"SHOW TABLES",
		"PRAGMA table_info(t)",
		"-- comment\nSELECT 1",
		"/* block */ SELECT 1",
		"/* a */ -- b\nSELECT 1",
		"-- a\n/* b */SELECT 1",
	}
	for _, s := range readOnly {
		if !isReadOnlyStatement(s) {
			t.Errorf("expected read-only: %q", s)
		}
	}
	writes := []string{
		"INSERT INTO t VALUES (1)",
		"UPDATE t SET x=1",
		"DELETE FROM t",
		"DROP TABLE t",
		"CREATE TABLE t (id int)",
		"-- only comment",
		"/*x*/DELETE FROM t",
		"/* c */ UPDATE t SET x=1",
		"/* unterminated SELECT 1",
		"-- c\n/*y*/DROP TABLE t",
	}
	for _, s := range writes {
		if isReadOnlyStatement(s) {
			t.Errorf("expected NOT read-only: %q", s)
		}
	}
}

func TestSQLQueryReadOnlyModeRejectsWrite(t *testing.T) {
	db := openFakeDB(t)
	tool := SQLQuery(SQLConfig{DB: db, ReadOnly: true})
	if _, err := tool.Execute(context.Background(), map[string]any{"sql": "DELETE FROM t"}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected read-only mode to reject a write")
	}
}

func TestSQLQueryReturnsRows(t *testing.T) {
	db := openFakeDB(t)
	tool := SQLQuery(SQLConfig{DB: db, ReadOnly: true})
	out, err := tool.Execute(context.Background(), map[string]any{"sql": "SELECT id, name FROM t"}, agent.ToolExecutionContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := out.(map[string]any)
	if result["row_count"].(int) != 2 {
		t.Fatalf("expected 2 rows, got %v", result["row_count"])
	}
	rows := result["rows"].([]map[string]any)
	if rows[0]["name"] != "alice" {
		t.Fatalf("expected first name alice, got %v", rows[0]["name"])
	}
}

func TestSQLQueryRequiresDB(t *testing.T) {
	tool := SQLQuery(SQLConfig{})
	if _, err := tool.Execute(context.Background(), map[string]any{"sql": "SELECT 1"}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected error when DB is nil")
	}
}

func TestSQLQueryEmptyStatement(t *testing.T) {
	db := openFakeDB(t)
	tool := SQLQuery(SQLConfig{DB: db, ReadOnly: true})
	if _, err := tool.Execute(context.Background(), map[string]any{"sql": "  "}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected error for empty statement")
	}
}

// --- minimal in-memory fake driver (no third-party dependency) ---

func openFakeDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("toolkit-fake", "")
	if err != nil {
		t.Fatalf("open fake db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func init() {
	sql.Register("toolkit-fake", fakeDriver{})
}

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(query string) (driver.Stmt, error) { return fakeStmt{query: query}, nil }
func (fakeConn) Close() error                              { return nil }
func (fakeConn) Begin() (driver.Tx, error)                 { return nil, io.EOF }

type fakeStmt struct{ query string }

func (fakeStmt) Close() error  { return nil }
func (fakeStmt) NumInput() int { return 0 }

func (s fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return fakeResult{}, nil
}

func (s fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	return &fakeRows{
		columns: []string{"id", "name"},
		data: [][]driver.Value{
			{int64(1), "alice"},
			{int64(2), "bob"},
		},
	}, nil
}

type fakeResult struct{}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (fakeResult) RowsAffected() (int64, error) { return 1, nil }

type fakeRows struct {
	columns []string
	data    [][]driver.Value
	pos     int
	mu      sync.Mutex
}

func (r *fakeRows) Columns() []string { return r.columns }
func (r *fakeRows) Close() error      { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pos >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.pos])
	r.pos++
	return nil
}
