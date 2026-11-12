package hammer_test

import (
	"context"
	"slices"
	"testing"

	"github.com/daichirata/hammer/internal/hammer"
)

func TestDiffDataLosses(t *testing.T) {
	const table = "CREATE TABLE t1 (a INT64 NOT NULL, b INT64, c STRING(10)) PRIMARY KEY(a);\n"
	const rdp = "CREATE TABLE t2 (a INT64 NOT NULL, ts TIMESTAMP) PRIMARY KEY(a)"
	const queue = "CREATE QUEUE q (id INT64 NOT NULL, ts TIMESTAMP) PRIMARY KEY(id)"
	tests := []struct {
		name     string
		from, to string
		want     []string
	}{
		{"drop table", table, "", []string{"DROP TABLE t1"}},
		{"drop column", table, "CREATE TABLE t1 (a INT64 NOT NULL, b INT64) PRIMARY KEY(a);", []string{"ALTER TABLE t1 DROP COLUMN c"}},
		{"column type change", table, "CREATE TABLE t1 (a INT64 NOT NULL, b STRING(5), c STRING(10)) PRIMARY KEY(a);", []string{"ALTER TABLE t1 DROP COLUMN b"}},
		{"drop sequence", "CREATE SEQUENCE s OPTIONS (sequence_kind = 'bit_reversed_positive');", "", []string{"DROP SEQUENCE s"}},
		{"drop queue", "CREATE QUEUE q (id INT64 NOT NULL) PRIMARY KEY(id);", "", []string{"DROP QUEUE q"}},
		{"primary key change recreates table", table, "CREATE TABLE t1 (a INT64 NOT NULL, b INT64, c STRING(10)) PRIMARY KEY(a, b);", []string{"DROP TABLE t1"}},
		{"add row deletion policy", rdp + ";", rdp + ", ROW DELETION POLICY (OLDER_THAN(ts, INTERVAL 1 DAY));", []string{"ALTER TABLE t2 ADD ROW DELETION POLICY ( OLDER_THAN ( ts, INTERVAL 1 DAY ))"}},
		{"replace row deletion policy", rdp + ", ROW DELETION POLICY (OLDER_THAN(ts, INTERVAL 1 DAY));", rdp + ", ROW DELETION POLICY (OLDER_THAN(ts, INTERVAL 2 DAY));", []string{"ALTER TABLE t2 REPLACE ROW DELETION POLICY ( OLDER_THAN ( ts, INTERVAL 2 DAY ))"}},
		{"drop row deletion policy", rdp + ", ROW DELETION POLICY (OLDER_THAN(ts, INTERVAL 1 DAY));", rdp + ";", nil},
		{"add queue row deletion policy", queue, queue + ", ROW DELETION POLICY (OLDER_THAN(ts, INTERVAL 1 DAY));", []string{"ALTER QUEUE q ADD ROW DELETION POLICY ( OLDER_THAN ( ts, INTERVAL 1 DAY ))"}},
		{
			"derived objects are not data loss",
			table + "CREATE INDEX i ON t1(b);\nCREATE VIEW v SQL SECURITY INVOKER AS SELECT a FROM t1;\nCREATE ROLE r;\nCREATE CHANGE STREAM cs FOR t1;",
			table, nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			opt := &hammer.DDLOption{}
			from, err := StringSource(tt.from).DDL(ctx, opt)
			if err != nil {
				t.Fatal(err)
			}
			to, err := StringSource(tt.to).DDL(ctx, opt)
			if err != nil {
				t.Fatal(err)
			}
			ddl, err := hammer.Diff(from, to)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, l := range ddl.DataLosses() {
				if l.Reason == "" {
					t.Errorf("%s: empty reason", l.SQL())
				}
				got = append(got, l.SQL())
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
