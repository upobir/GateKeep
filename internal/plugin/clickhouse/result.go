package clickhouse

import (
	"database/sql"
	"encoding/base64"
	"math/big"
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

// cell turns a driver value into something json.Marshal writes faithfully.
// The driver already unwraps Nullable and decodes JSON, Map, Tuple and UUID.
// The fixes below are only for values json would otherwise mangle.
func cell(value any, databaseType string) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		// String and FixedString may hold binary data.
		if !utf8.ValidString(typed) {
			return base64.StdEncoding.EncodeToString([]byte(typed))
		}
		return typed
	case []uint8:
		// Array(UInt8). json would write []byte as base64.
		numbers := make([]uint16, len(typed))
		for i, b := range typed {
			numbers[i] = uint16(b)
		}
		return numbers
	case big.Int:
		// Int128 and wider. Only *big.Int marshals as a number.
		return &typed
	case time.Time:
		return formatSQLTime(typed, databaseType)
	default:
		return value
	}
}

// dateTypes are the column types whose time.Time has no time of day. The driver
// scans Date and DateTime alike, so its type name is the only way to tell.
var dateTypes = map[string]bool{
	"Date": true, "Date32": true, "Nullable(Date)": true, "Nullable(Date32)": true,
}

func formatSQLTime(value time.Time, databaseType string) string {
	if dateTypes[databaseType] {
		return value.Format("2006-01-02")
	}
	if value.Nanosecond() == 0 {
		return value.Format("2006-01-02 15:04:05")
	}
	return value.Format("2006-01-02 15:04:05.000000")
}
