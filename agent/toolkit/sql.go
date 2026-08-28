package toolkit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/jd-opensource/joytoken-sdk-go/agent"
)

// SQLConfig configures the local sql_query tool. Credentials never reach the
// model: the host opens *sql.DB once at configuration time (with its own DSN
// and password) and injects it here. At run time the model only produces SQL
// text, which this tool executes against the pre-established connection.
type SQLConfig struct {
	// DB is the pre-opened database handle. Required.
	DB *sql.DB
	// ReadOnly, when true, rejects any statement that is not a read-only query
	// (SELECT / WITH / EXPLAIN / SHOW / PRAGMA). This is the recommended mode:
	// pair it with PermissionAuto. To allow writes, set ReadOnly=false and wrap
	// the tool with PermissionAsk so the host approves each mutation.
	ReadOnly bool
	// MaxRows caps the number of rows returned. Zero means DefaultSQLMaxRows.
	MaxRows int
}

// DefaultSQLMaxRows bounds the rows returned by a single query.
const DefaultSQLMaxRows = 200

// SQLQuery returns a local tool that runs SQL against a host-provided database
// handle. The model supplies only SQL text; it never sees connection details.
//
// Register it under PermissionAuto when ReadOnly is true. When writes are
// allowed, register it under PermissionAsk so the host approves each call.
func SQLQuery(config SQLConfig) agent.AgentTool {
	maxRows := config.MaxRows
	if maxRows <= 0 {
		maxRows = DefaultSQLMaxRows
	}

	return agent.AgentTool{
		Name:        "sql_query",
		Description: "Execute a SQL statement against the configured database and return the rows. Provide a single statement.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sql": map[string]any{
					"type":        "string",
					"description": "A single SQL statement to execute.",
				},
			},
			"required": []string{"sql"},
		},
		Execute: func(ctx context.Context, input any, _ agent.ToolExecutionContext) (any, error) {
			statement, err := stringArg(input, "sql")
			if err != nil {
				return nil, err
			}
			statement = strings.TrimSpace(statement)
			if statement == "" {
				return nil, fmt.Errorf("sql_query: empty statement")
			}
			if config.DB == nil {
				return nil, fmt.Errorf("sql_query: database handle is not configured")
			}
			readOnly := isReadOnlyStatement(statement)
			if config.ReadOnly && !readOnly {
				return nil, fmt.Errorf("sql_query: only read-only statements are allowed")
			}

			if readOnly {
				return runQuery(ctx, config.DB, statement, maxRows)
			}
			return runExec(ctx, config.DB, statement)
		},
	}
}

// runQuery executes a read-only statement and returns rows as maps.
func runQuery(ctx context.Context, db *sql.DB, statement string, maxRows int) (any, error) {
	rows, err := db.QueryContext(ctx, statement)
	if err != nil {
		return nil, fmt.Errorf("sql_query: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("sql_query: %w", err)
	}

	result := make([]map[string]any, 0)
	truncated := false
	for rows.Next() {
		if len(result) >= maxRows {
			truncated = true
			break
		}
		holders := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range holders {
			pointers[i] = &holders[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, fmt.Errorf("sql_query: %w", err)
		}
		row := make(map[string]any, len(columns))
		for i, name := range columns {
			row[name] = normalizeValue(holders[i])
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sql_query: %w", err)
	}
	return map[string]any{
		"columns":   columns,
		"rows":      result,
		"row_count": len(result),
		"truncated": truncated,
	}, nil
}

// runExec executes a mutating statement and returns affected-row metadata.
func runExec(ctx context.Context, db *sql.DB, statement string) (any, error) {
	res, err := db.ExecContext(ctx, statement)
	if err != nil {
		return nil, fmt.Errorf("sql_query: %w", err)
	}
	affected, _ := res.RowsAffected()
	lastID, _ := res.LastInsertId()
	return map[string]any{
		"rows_affected":  affected,
		"last_insert_id": lastID,
		"read_only":      false,
	}, nil
}

// isReadOnlyStatement reports whether a statement only reads data. It inspects
// the leading keyword after stripping leading comments and whitespace.
func isReadOnlyStatement(statement string) bool {
	s := strings.ToLower(strings.TrimSpace(statement))
	// Strip any leading comments (line "--" and block "/* */") so a statement
	// cannot smuggle a write past the read-only check by prefixing a comment,
	// e.g. "/*x*/DELETE ..." or "-- x\nDELETE ...".
	for {
		switch {
		case strings.HasPrefix(s, "--"):
			idx := strings.IndexByte(s, '\n')
			if idx < 0 {
				return false
			}
			s = strings.TrimSpace(s[idx+1:])
		case strings.HasPrefix(s, "/*"):
			idx := strings.Index(s, "*/")
			if idx < 0 {
				return false
			}
			s = strings.TrimSpace(s[idx+2:])
		default:
			goto classify
		}
	}
classify:
	switch {
	case strings.HasPrefix(s, "select"),
		strings.HasPrefix(s, "with"),
		strings.HasPrefix(s, "explain"),
		strings.HasPrefix(s, "show"),
		strings.HasPrefix(s, "pragma"):
		return true
	default:
		return false
	}
}

// normalizeValue converts driver byte slices to strings so results serialize
// cleanly to JSON, leaving other types untouched.
func normalizeValue(value any) any {
	if b, ok := value.([]byte); ok {
		return string(b)
	}
	return value
}
