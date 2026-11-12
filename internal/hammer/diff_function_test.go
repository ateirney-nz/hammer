package hammer_test

import "testing"

const functionTable = `CREATE TABLE t1 (t1_1 INT64 NOT NULL) PRIMARY KEY(t1_1);`

func TestDiffFunction(t *testing.T) {
	runDiffTests(t, []diffTestCase{
		{
			name: "create function",
			from: `CREATE SCHEMA s;`,
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
`,
			expected: []string{
				`CREATE FUNCTION s.f1 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1)`,
			},
		},
		{
			name: "create schema with its function",
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
`,
			expected: []string{
				`CREATE SCHEMA s`,
				`CREATE FUNCTION s.f1 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1)`,
			},
		},
		{
			name: "drop function",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
`,
			to: `CREATE SCHEMA s;`,
			expected: []string{
				`DROP FUNCTION s.f1`,
			},
		},
		{
			name: "drop schema with its function",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
`,
			expected: []string{
				`DROP FUNCTION s.f1`,
				`DROP SCHEMA s`,
			},
		},
		{
			name: "same function",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
`,
			to: `
CREATE SCHEMA s;
CREATE OR REPLACE FUNCTION s.F1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x  +  1);
`,
		},
		{
			name: "schema names are case-sensitive",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
`,
			to: `
CREATE SCHEMA S;
CREATE FUNCTION S.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
`,
			expected: []string{
				`CREATE SCHEMA S`,
				`CREATE FUNCTION S.f1 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1)`,
				`DROP FUNCTION s.f1`,
				`DROP SCHEMA s`,
			},
		},
		{
			name: "recreate callers in dependency order",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE FUNCTION s.f2(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (s.f1(x) * 2);
CREATE FUNCTION s.f3(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (s.f2(x) + 1);
`,
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64));
CREATE FUNCTION s.f2(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (CAST(s.f1(x) AS INT64) * 2);
CREATE FUNCTION s.f3(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (s.f2(x) + 1);
`,
			expected: []string{
				`DROP FUNCTION s.f3`,
				`DROP FUNCTION s.f2`,
				`CREATE OR REPLACE FUNCTION s.f1 (x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64))`,
				`CREATE FUNCTION s.f2 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (CAST(s.f1(x) AS INT64) * 2)`,
				`CREATE FUNCTION s.f3 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (s.f2(x) + 1)`,
			},
		},
		{
			name: "recreate a function called by a column default",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE FUNCTION s.f2(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (s.f1(x) * 2);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 INT64 DEFAULT (s.f2(1))) PRIMARY KEY(t1_1);
`,
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64));
CREATE FUNCTION s.f2(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (CAST(s.f1(x) AS INT64) * 2);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 INT64 DEFAULT (s.f2(1))) PRIMARY KEY(t1_1);
`,
			wantErr: "cannot change function s.f2: the default of column t1.t1_2 calls it; if that column or its table is dropped in the same diff, split the change into two migrations",
		},
		{
			name: "recreate a function called by a generated column",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE FUNCTION s.f2(x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (s.f1(x) * 2);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 INT64 AS (s.f2(t1_1)) STORED) PRIMARY KEY(t1_1);
`,
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64));
CREATE FUNCTION s.f2(x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (s.f1(x) * 2);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 INT64 AS (s.f2(t1_1)) STORED) PRIMARY KEY(t1_1);
`,
			wantErr: "cannot change function s.f2: generated column t1.t1_2 calls it",
		},
		{
			name: "replace function body",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
`,
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 2);
`,
			expected: []string{
				`CREATE OR REPLACE FUNCTION s.f1 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 2)`,
			},
		},
		{
			name: "replace function body under a view that calls it",
			from: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
`,
			to: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 2);
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
`,
			expected: []string{
				`CREATE OR REPLACE FUNCTION s.f1 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 2)`,
			},
		},
		{
			name: "change return type under a view that calls it",
			from: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
`,
			to: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64));
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
`,
			expected: []string{
				`DROP VIEW v1`,
				`CREATE OR REPLACE FUNCTION s.f1 (x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64))`,
				`CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1`,
			},
		},
		{
			name: "change arguments under a view that calls it",
			from: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
`,
			to: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64, y INT64 DEFAULT 1) RETURNS INT64 SQL SECURITY INVOKER AS (x + y);
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
`,
			expected: []string{
				`DROP VIEW v1`,
				`CREATE OR REPLACE FUNCTION s.f1 (x INT64, y INT64 DEFAULT 1) RETURNS INT64 SQL SECURITY INVOKER AS (x + y)`,
				`CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1`,
			},
		},
		{
			name: "change arguments and the calling view together",
			from: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
`,
			to: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x STRING) RETURNS INT64 SQL SECURITY INVOKER AS (LENGTH(x));
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(CAST(t1.t1_1 AS STRING)) AS c FROM t1;
`,
			expected: []string{
				`DROP VIEW v1`,
				`CREATE OR REPLACE FUNCTION s.f1 (x STRING) RETURNS INT64 SQL SECURITY INVOKER AS (LENGTH(x))`,
				`CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(CAST(t1.t1_1 AS STRING)) AS c FROM t1`,
			},
		},
		{
			name: "change return type under a view of a view that calls it",
			from: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
CREATE VIEW v2 SQL SECURITY INVOKER AS SELECT v1.c FROM v1;
`,
			to: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64));
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
CREATE VIEW v2 SQL SECURITY INVOKER AS SELECT v1.c FROM v1;
`,
			expected: []string{
				`DROP VIEW v2`,
				`DROP VIEW v1`,
				`CREATE OR REPLACE FUNCTION s.f1 (x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64))`,
				`CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1`,
				`CREATE VIEW v2 SQL SECURITY INVOKER AS SELECT v1.c FROM v1`,
			},
		},
		{
			name: "change return type while the calling view is dropped",
			from: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
`,
			to: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64));
`,
			expected: []string{
				`DROP VIEW v1`,
				`CREATE OR REPLACE FUNCTION s.f1 (x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64))`,
			},
		},
		{
			name: "change return type under a function and a view that call it",
			from: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE FUNCTION s.f2(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (s.f1(x) * 2);
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f2(t1.t1_1) AS c FROM t1;
`,
			to: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64));
CREATE FUNCTION s.f2(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (CAST(s.f1(x) AS INT64) * 2);
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f2(t1.t1_1) AS c FROM t1;
`,
			expected: []string{
				`DROP VIEW v1`,
				`DROP FUNCTION s.f2`,
				`CREATE OR REPLACE FUNCTION s.f1 (x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64))`,
				`CREATE FUNCTION s.f2 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (CAST(s.f1(x) AS INT64) * 2)`,
				`CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f2(t1.t1_1) AS c FROM t1`,
			},
		},
		{
			name: "change return type under a function that is dropped",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE FUNCTION s.f2(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (s.f1(x) * 2);
`,
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64));
`,
			expected: []string{
				`DROP FUNCTION s.f2`,
				`CREATE OR REPLACE FUNCTION s.f1 (x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64))`,
			},
		},
		{
			name: "change the body of a function without RETURNS under a view that calls it",
			from: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) SQL SECURITY INVOKER AS (x + 1);
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
`,
			to: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) SQL SECURITY INVOKER AS (CAST(x AS STRING));
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
`,
			expected: []string{
				`DROP VIEW v1`,
				`CREATE OR REPLACE FUNCTION s.f1 (x INT64) SQL SECURITY INVOKER AS (CAST(x AS STRING))`,
				`CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1`,
			},
		},
		{
			name: "change return type under a view calling it in another case",
			from: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.F1(t1.t1_1) AS c FROM t1;
`,
			to: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64));
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.F1(t1.t1_1) AS c FROM t1;
`,
			expected: []string{
				`DROP VIEW v1`,
				`CREATE OR REPLACE FUNCTION s.f1 (x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64))`,
				`CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.F1(t1.t1_1) AS c FROM t1`,
			},
		},
		{
			name: "change return type under a view of a schema-qualified view that calls it",
			from: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE VIEW s.v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
CREATE VIEW v2 SQL SECURITY INVOKER AS SELECT v1.c FROM s.v1 AS v1;
`,
			to: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64));
CREATE VIEW s.v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
CREATE VIEW v2 SQL SECURITY INVOKER AS SELECT v1.c FROM s.v1 AS v1;
`,
			expected: []string{
				`DROP VIEW v2`,
				`DROP VIEW s.v1`,
				`CREATE OR REPLACE FUNCTION s.f1 (x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64))`,
				`CREATE VIEW s.v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1`,
				`CREATE VIEW v2 SQL SECURITY INVOKER AS SELECT v1.c FROM s.v1 AS v1`,
			},
		},
		{
			name: "create function before the column default that calls it",
			from: functionTable + `CREATE SCHEMA s;`,
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 INT64 DEFAULT (s.f1(1))) PRIMARY KEY(t1_1);
`,
			expected: []string{
				`CREATE FUNCTION s.f1 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1)`,
				`ALTER TABLE t1 ADD COLUMN t1_2 INT64 DEFAULT (s.f1(1))`,
			},
		},
		{
			name: "drop function after the column default that called it",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 INT64 DEFAULT (s.f1(1))) PRIMARY KEY(t1_1);
`,
			to: functionTable + `CREATE SCHEMA s;`,
			expected: []string{
				`ALTER TABLE t1 DROP COLUMN t1_2`,
				`DROP FUNCTION s.f1`,
			},
		},
		{
			name: "replace function body under a column default",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 INT64 DEFAULT (s.f1(1))) PRIMARY KEY(t1_1);
`,
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 2);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 INT64 DEFAULT (s.f1(1))) PRIMARY KEY(t1_1);
`,
			expected: []string{
				`CREATE OR REPLACE FUNCTION s.f1 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 2)`,
			},
		},
		{
			name: "change signature under a column default",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 INT64 DEFAULT (s.f1(1))) PRIMARY KEY(t1_1);
`,
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS FLOAT64 SQL SECURITY INVOKER AS (CAST(x AS FLOAT64));
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 FLOAT64 DEFAULT (s.f1(1))) PRIMARY KEY(t1_1);
`,
			wantErr: "cannot change function s.f1: the default of column t1.t1_2 calls it",
		},
		{
			name: "create function before the generated column that calls it",
			from: functionTable + `CREATE SCHEMA s;`,
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 INT64 AS (s.f1(t1_1)) STORED) PRIMARY KEY(t1_1);
`,
			expected: []string{
				`CREATE FUNCTION s.f1 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1)`,
				`ALTER TABLE t1 ADD COLUMN t1_2 INT64 AS (s.f1(t1_1)) STORED`,
			},
		},
		{
			name: "drop function after the generated column that called it",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 INT64 AS (s.f1(t1_1)) STORED) PRIMARY KEY(t1_1);
`,
			to: functionTable + `CREATE SCHEMA s;`,
			expected: []string{
				`ALTER TABLE t1 DROP COLUMN t1_2`,
				`DROP FUNCTION s.f1`,
			},
		},
		{
			name: "replace function body under a generated column",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 INT64 AS (s.f1(t1_1)) STORED) PRIMARY KEY(t1_1);
`,
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 2);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 INT64 AS (s.f1(t1_1)) STORED) PRIMARY KEY(t1_1);
`,
			wantErr: "cannot change function s.f1: generated column t1.t1_2 calls it",
		},
		{
			name: "create function before the check constraint that calls it",
			from: functionTable + `CREATE SCHEMA s;`,
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, CONSTRAINT ck CHECK (s.f1(t1_1) > 0)) PRIMARY KEY(t1_1);
`,
			expected: []string{
				`CREATE FUNCTION s.f1 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1)`,
				`ALTER TABLE t1 ADD CONSTRAINT ck CHECK (s.f1(t1_1) > 0)`,
			},
		},
		{
			name: "drop function after the check constraint that called it",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, CONSTRAINT ck CHECK (s.f1(t1_1) > 0)) PRIMARY KEY(t1_1);
`,
			to: functionTable + `CREATE SCHEMA s;`,
			expected: []string{
				`ALTER TABLE t1 DROP CONSTRAINT ck`,
				`DROP FUNCTION s.f1`,
			},
		},
		{
			name: "replace function body under a check constraint",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, CONSTRAINT ck CHECK (s.f1(t1_1) > 0)) PRIMARY KEY(t1_1);
`,
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 2);
CREATE TABLE t1 (t1_1 INT64 NOT NULL, CONSTRAINT ck CHECK (s.f1(t1_1) > 0)) PRIMARY KEY(t1_1);
`,
			wantErr: "cannot change function s.f1: a CHECK constraint on table t1 calls it",
		},
		{
			name: "create view calling a function declared after it",
			from: functionTable + `CREATE SCHEMA s;`,
			to: functionTable + `
CREATE SCHEMA s;
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
`,
			expected: []string{
				`CREATE FUNCTION s.f1 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1)`,
				`CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1`,
			},
		},
		{
			name: "drop view calling a function before the function",
			from: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
`,
			to: functionTable,
			expected: []string{
				`DROP VIEW v1`,
				`DROP FUNCTION s.f1`,
				`DROP SCHEMA s`,
			},
		},
		{
			name: "view stops calling a function that is dropped",
			from: functionTable + `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT s.f1(t1.t1_1) AS c FROM t1;
`,
			to: functionTable + `
CREATE SCHEMA s;
CREATE VIEW v1 SQL SECURITY INVOKER AS SELECT t1.t1_1 AS c FROM t1;
`,
			expected: []string{
				`CREATE OR REPLACE VIEW v1 SQL SECURITY INVOKER AS SELECT t1.t1_1 AS c FROM t1`,
				`DROP FUNCTION s.f1`,
			},
		},
		{
			name: "function calling a function declared after it",
			from: `CREATE SCHEMA s;`,
			to: `
CREATE SCHEMA s;
CREATE FUNCTION s.f2(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (s.f1(x) * 2);
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
`,
			expected: []string{
				`CREATE FUNCTION s.f1 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1)`,
				`CREATE FUNCTION s.f2 (x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (s.f1(x) * 2)`,
			},
		},
		{
			name: "drop function calling a function before the called one",
			from: `
CREATE SCHEMA s;
CREATE FUNCTION s.f1(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (x + 1);
CREATE FUNCTION s.f2(x INT64) RETURNS INT64 SQL SECURITY INVOKER AS (s.f1(x) * 2);
`,
			to: `CREATE SCHEMA s;`,
			expected: []string{
				`DROP FUNCTION s.f2`,
				`DROP FUNCTION s.f1`,
			},
		},
	})
}
