package postgres

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

func readQuery(rows *sql.Rows) (any, error) {
	var sets []map[string]any
	for {
		set, err := readSet(rows)
		if err != nil {
			return nil, err
		}
		sets = append(sets, set)
		if !rows.NextResultSet() {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(sets) == 1 {
		return sets[0], nil
	}
	return map[string]any{"results": sets}, nil
}

func readSet(rows *sql.Rows) (map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if columns == nil {
		columns = []string{}
	}
	types, _ := rows.ColumnTypes()
	values := make([]any, len(columns))
	targets := make([]any, len(columns))
	for i := range values {
		targets[i] = &values[i]
	}
	table := make([][]any, 0)
	for rows.Next() {
		for i := range values {
			values[i] = nil
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		row := make([]any, len(columns))
		for i, value := range values {
			row[i] = cell(value, columnType(types, i))
		}
		table = append(table, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"columns": columns, "rows": table}, nil
}

func columnType(types []*sql.ColumnType, index int) string {
	if index >= len(types) || types[index] == nil {
		return ""
	}
	return types[index].DatabaseTypeName()
}

func cell(value any, databaseType string) any {
	upper := strings.ToUpper(databaseType)
	switch upper {
	case "BIT":
		if raw, ok := value.([]byte); ok {
			var number uint64
			for _, b := range raw {
				number = number<<8 | uint64(b)
			}
			return number
		}
	case "JSON", "JSONB":
		if raw, ok := value.([]byte); ok {
			var decoded any
			if json.Unmarshal(raw, &decoded) == nil {
				return decoded
			}
		}
		if text, ok := value.(string); ok {
			var decoded any
			if json.Unmarshal([]byte(text), &decoded) == nil {
				return decoded
			}
		}
	}
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		if upper == "BYTEA" || !utf8.Valid(typed) {
			return base64.StdEncoding.EncodeToString(typed)
		}
		return string(typed)
	case time.Time:
		return formatSQLTime(typed, upper)
	default:
		return value
	}
}

func formatSQLTime(value time.Time, databaseType string) string {
	if databaseType == "DATE" {
		return value.Format("2006-01-02")
	}
	if value.Nanosecond() == 0 {
		return value.Format("2006-01-02 15:04:05")
	}
	return value.Format("2006-01-02 15:04:05.000000")
}
