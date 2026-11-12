package hammer_test

import "testing"

const graphTables = `
CREATE TABLE n (id INT64 NOT NULL, name STRING(MAX)) PRIMARY KEY (id);
CREATE TABLE e (id INT64 NOT NULL, dst INT64 NOT NULL) PRIMARY KEY (id, dst);
`

const graphShorthand = `CREATE PROPERTY GRAPH g NODE TABLES (n) EDGE TABLES (e SOURCE KEY (id) REFERENCES n (id) DESTINATION KEY (dst) REFERENCES n (id));`

func TestDiffPropertyGraph(t *testing.T) {
	runDiffTests(t, []diffTestCase{
		{
			name: "create graph",
			from: graphTables,
			to:   graphTables + graphShorthand,
			expected: []string{
				`CREATE PROPERTY GRAPH g NODE TABLES (n) EDGE TABLES (e SOURCE KEY (id) REFERENCES n (id) DESTINATION KEY (dst) REFERENCES n (id))`,
			},
		},
		{
			name: "drop graph before its tables",
			from: graphTables + graphShorthand,
			to:   ``,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`DROP TABLE n`,
				`DROP TABLE e`,
			},
		},
		{
			name: "shorthand equals the expanded readback",
			from: graphTables + graphShorthand,
			to:   graphTables + `CREATE PROPERTY GRAPH g NODE TABLES( n KEY(id) LABEL n PROPERTIES( id, name) ) EDGE TABLES( e KEY(id, dst) SOURCE KEY(id) REFERENCES n(id) DESTINATION KEY(dst) REFERENCES n(id) LABEL e PROPERTIES( dst, id) );`,
		},
		{
			name: "change graph",
			from: graphTables + graphShorthand,
			to:   graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			expected: []string{
				`CREATE OR REPLACE PROPERTY GRAPH g NODE TABLES (n)`,
			},
		},
		{
			name: "graph with all columns is rebuilt when a column is added",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			to: `
CREATE TABLE n (id INT64 NOT NULL, name STRING(MAX), age INT64) PRIMARY KEY (id);
CREATE TABLE e (id INT64 NOT NULL, dst INT64 NOT NULL) PRIMARY KEY (id, dst);
CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			expected: []string{
				`ALTER TABLE n ADD COLUMN age INT64`,
				`CREATE OR REPLACE PROPERTY GRAPH g NODE TABLES (n)`,
			},
		},
		{
			name: "graph is dropped before a column it uses",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) PROPERTIES (id, name));`,
			to: `
CREATE TABLE n (id INT64 NOT NULL) PRIMARY KEY (id);
CREATE TABLE e (id INT64 NOT NULL, dst INT64 NOT NULL) PRIMARY KEY (id, dst);
CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`ALTER TABLE n DROP COLUMN name`,
				`CREATE PROPERTY GRAPH g NODE TABLES (n)`,
			},
		},
		{
			name: "graph is rebuilt when its table is recreated",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id));`,
			to: `
CREATE TABLE n (id INT64 NOT NULL, name STRING(MAX)) PRIMARY KEY (name);
CREATE TABLE e (id INT64 NOT NULL, dst INT64 NOT NULL) PRIMARY KEY (id, dst);
CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id));`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`DROP TABLE n`,
				"CREATE TABLE n (\n  id INT64 NOT NULL,\n  name STRING(MAX)\n) PRIMARY KEY (name)",
				`CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id))`,
			},
		},
		{
			name: "graph is rebuilt around a column that changes type",
			from: `CREATE TABLE n (id INT64 NOT NULL, v INT64) PRIMARY KEY (id); CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			to:   `CREATE TABLE n (id INT64 NOT NULL, v BOOL) PRIMARY KEY (id); CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`ALTER TABLE n DROP COLUMN v`,
				`ALTER TABLE n ADD COLUMN v BOOL`,
				`CREATE PROPERTY GRAPH g NODE TABLES (n)`,
			},
		},
		{
			name: "graph is rebuilt around a column that changes type back",
			from: `CREATE TABLE n (id INT64 NOT NULL, v BOOL) PRIMARY KEY (id); CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			to:   `CREATE TABLE n (id INT64 NOT NULL, v INT64) PRIMARY KEY (id); CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`ALTER TABLE n DROP COLUMN v`,
				`ALTER TABLE n ADD COLUMN v INT64`,
				`CREATE PROPERTY GRAPH g NODE TABLES (n)`,
			},
		},
		{
			name: "graph on an interleaved child is rebuilt when the parent is recreated",
			from: graphParent + `CREATE PROPERTY GRAPH g NODE TABLES (c);`,
			to:   graphParentDefault + `CREATE PROPERTY GRAPH g NODE TABLES (c);`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`DROP TABLE c`,
				`DROP TABLE p`,
				"CREATE TABLE p (\n  pid INT64 NOT NULL DEFAULT (0)\n) PRIMARY KEY (pid)",
				"CREATE TABLE c (\n  pid INT64 NOT NULL,\n  cid INT64 NOT NULL\n) PRIMARY KEY (pid, cid),\n  INTERLEAVE IN PARENT p",
				`CREATE PROPERTY GRAPH g NODE TABLES (c)`,
			},
		},
		{
			name: "graph on an interleaved child is rebuilt when the parent is recreated back",
			from: graphParentDefault + `CREATE PROPERTY GRAPH g NODE TABLES (c);`,
			to:   graphParent + `CREATE PROPERTY GRAPH g NODE TABLES (c);`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`DROP TABLE c`,
				`DROP TABLE p`,
				"CREATE TABLE p (\n  pid INT64 NOT NULL\n) PRIMARY KEY (pid)",
				"CREATE TABLE c (\n  pid INT64 NOT NULL,\n  cid INT64 NOT NULL\n) PRIMARY KEY (pid, cid),\n  INTERLEAVE IN PARENT p",
				`CREATE PROPERTY GRAPH g NODE TABLES (c)`,
			},
		},
		{
			name: "graph is rebuilt when its table loses its interleave",
			from: graphParent + `CREATE PROPERTY GRAPH g NODE TABLES (c);`,
			to: `
CREATE TABLE p (pid INT64 NOT NULL) PRIMARY KEY (pid);
CREATE TABLE c (pid INT64 NOT NULL, cid INT64 NOT NULL) PRIMARY KEY (pid, cid);
CREATE PROPERTY GRAPH g NODE TABLES (c);`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`DROP TABLE c`,
				"CREATE TABLE c (\n  pid INT64 NOT NULL,\n  cid INT64 NOT NULL\n) PRIMARY KEY (pid, cid)",
				`CREATE PROPERTY GRAPH g NODE TABLES (c)`,
			},
		},
		{
			name: "graph over a view is dropped before the view",
			from: graphView + `CREATE PROPERTY GRAPH g NODE TABLES (v KEY (id));`,
			to:   `CREATE TABLE t (id INT64 NOT NULL, name STRING(MAX)) PRIMARY KEY (id);`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`DROP VIEW v`,
			},
		},
		{
			name: "graph over a view is dropped before the view is recreated",
			from: graphTables + `
CREATE FUNCTION f(x STRING) RETURNS STRING SQL SECURITY INVOKER AS (x);
CREATE VIEW v SQL SECURITY INVOKER AS SELECT n.id, f(n.name) AS fname FROM n;
CREATE PROPERTY GRAPH g NODE TABLES (v KEY (id));`,
			to: graphTables + `
CREATE FUNCTION f(x STRING, y STRING) RETURNS STRING SQL SECURITY INVOKER AS (x);
CREATE VIEW v SQL SECURITY INVOKER AS SELECT n.id, f(n.name, n.name) AS fname FROM n;
CREATE PROPERTY GRAPH g NODE TABLES (v KEY (id));`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`DROP VIEW v`,
				`CREATE OR REPLACE FUNCTION f (x STRING, y STRING) RETURNS STRING SQL SECURITY INVOKER AS (x)`,
				`CREATE VIEW v SQL SECURITY INVOKER AS SELECT n.id, f(n.name, n.name) AS fname FROM n`,
				`CREATE PROPERTY GRAPH g NODE TABLES (v KEY (id))`,
			},
		},
		{
			name: "graph is dropped before a view it reads is replaced",
			from: graphView + `CREATE PROPERTY GRAPH g NODE TABLES (v KEY (id));`,
			to: `
CREATE TABLE t (id INT64 NOT NULL, name STRING(MAX)) PRIMARY KEY (id);
CREATE VIEW v SQL SECURITY INVOKER AS SELECT t.id FROM t;
CREATE PROPERTY GRAPH g NODE TABLES (v KEY (id));`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`CREATE OR REPLACE VIEW v SQL SECURITY INVOKER AS SELECT t.id FROM t`,
				`CREATE PROPERTY GRAPH g NODE TABLES (v KEY (id))`,
			},
		},
		{
			name: "graph is dropped before a view it reads is replaced back",
			from: `
CREATE TABLE t (id INT64 NOT NULL, name STRING(MAX)) PRIMARY KEY (id);
CREATE VIEW v SQL SECURITY INVOKER AS SELECT t.id FROM t;
CREATE PROPERTY GRAPH g NODE TABLES (v KEY (id));`,
			to: graphView + `CREATE PROPERTY GRAPH g NODE TABLES (v KEY (id));`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`CREATE OR REPLACE VIEW v SQL SECURITY INVOKER AS SELECT t.id, t.name FROM t`,
				`CREATE PROPERTY GRAPH g NODE TABLES (v KEY (id))`,
			},
		},
		{
			name: "graph is dropped before a column it reads changes type in place",
			from: `CREATE TABLE n (id INT64 NOT NULL, v STRING(MAX)) PRIMARY KEY (id); CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			to:   `CREATE TABLE n (id INT64 NOT NULL, v BYTES(MAX)) PRIMARY KEY (id); CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`ALTER TABLE n ALTER COLUMN v BYTES(MAX)`,
				`CREATE PROPERTY GRAPH g NODE TABLES (n)`,
			},
		},
		{
			name: "graph is dropped before a column it reads changes type in place back",
			from: `CREATE TABLE n (id INT64 NOT NULL, v BYTES(MAX)) PRIMARY KEY (id); CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			to:   `CREATE TABLE n (id INT64 NOT NULL, v STRING(MAX)) PRIMARY KEY (id); CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`ALTER TABLE n ALTER COLUMN v STRING(MAX)`,
				`CREATE PROPERTY GRAPH g NODE TABLES (n)`,
			},
		},
		{
			name: "graph is dropped before its explicit key column is dropped",
			from: `CREATE TABLE n (id INT64 NOT NULL, k INT64 NOT NULL) PRIMARY KEY (id); CREATE PROPERTY GRAPH g NODE TABLES (n KEY (k) PROPERTIES (id));`,
			to:   `CREATE TABLE n (id INT64 NOT NULL) PRIMARY KEY (id); CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) PROPERTIES (id));`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`ALTER TABLE n DROP COLUMN k`,
				`CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) PROPERTIES (id))`,
			},
		},
		{
			name: "graph is dropped before its dynamic label column is dropped",
			from: graphDynamic + `CREATE PROPERTY GRAPH g NODE TABLES (d KEY (id) LABEL d PROPERTIES (id) DYNAMIC LABEL (lbl));`,
			to:   `CREATE TABLE d (id INT64 NOT NULL, props JSON) PRIMARY KEY (id); CREATE PROPERTY GRAPH g NODE TABLES (d KEY (id) LABEL d PROPERTIES (id));`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`ALTER TABLE d DROP COLUMN lbl`,
				`CREATE PROPERTY GRAPH g NODE TABLES (d KEY (id) LABEL d PROPERTIES (id))`,
			},
		},
		{
			name: "graph is dropped before its dynamic properties column is dropped",
			from: graphDynamic + `CREATE PROPERTY GRAPH g NODE TABLES (d KEY (id) LABEL d PROPERTIES (id) DYNAMIC PROPERTIES (props));`,
			to:   `CREATE TABLE d (id INT64 NOT NULL, lbl STRING(MAX)) PRIMARY KEY (id); CREATE PROPERTY GRAPH g NODE TABLES (d KEY (id) LABEL d PROPERTIES (id));`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`ALTER TABLE d DROP COLUMN props`,
				`CREATE PROPERTY GRAPH g NODE TABLES (d KEY (id) LABEL d PROPERTIES (id))`,
			},
		},
		{
			name: "graph with upper-case names is rebuilt when its table is recreated",
			from: `CREATE TABLE N (id INT64 NOT NULL, name STRING(MAX)) PRIMARY KEY (id); CREATE PROPERTY GRAPH G NODE TABLES (N);`,
			to:   `CREATE TABLE N (id INT64 NOT NULL, name STRING(MAX)) PRIMARY KEY (name); CREATE PROPERTY GRAPH G NODE TABLES (N);`,
			expected: []string{
				`DROP PROPERTY GRAPH G`,
				`DROP TABLE N`,
				"CREATE TABLE N (\n  id INT64 NOT NULL,\n  name STRING(MAX)\n) PRIMARY KEY (name)",
				`CREATE PROPERTY GRAPH G NODE TABLES (N)`,
			},
		},
		{
			name: "graph is rebuilt when a function its property calls through another function changes signature",
			from: graphTables + `
CREATE FUNCTION f(x STRING) RETURNS STRING SQL SECURITY INVOKER AS (x);
CREATE FUNCTION h(x STRING) RETURNS STRING SQL SECURITY INVOKER AS (f(x));
CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) PROPERTIES (h(name) AS hname));`,
			to: graphTables + `
CREATE FUNCTION f(x STRING, y STRING) RETURNS STRING SQL SECURITY INVOKER AS (x);
CREATE FUNCTION h(x STRING) RETURNS STRING SQL SECURITY INVOKER AS (f(x, x));
CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) PROPERTIES (h(name) AS hname));`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`DROP FUNCTION h`,
				`CREATE OR REPLACE FUNCTION f (x STRING, y STRING) RETURNS STRING SQL SECURITY INVOKER AS (x)`,
				`CREATE FUNCTION h (x STRING) RETURNS STRING SQL SECURITY INVOKER AS (f(x, x))`,
				`CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) PROPERTIES (h(name) AS hname))`,
			},
		},
		{
			name: "graph is dropped before the view over a replaced model is recreated",
			from: `
CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = "//aiplatform.googleapis.com/projects/p/locations/us-central1/endpoints/e");
CREATE TABLE t (x INT64) PRIMARY KEY (x);
CREATE VIEW v SQL SECURITY INVOKER AS SELECT y FROM ML.PREDICT(MODEL m, (SELECT t.x FROM t));
CREATE PROPERTY GRAPH g NODE TABLES (v KEY (y));`,
			to: `
CREATE MODEL m INPUT (x INT64) OUTPUT (z INT64) REMOTE OPTIONS (endpoint = "//aiplatform.googleapis.com/projects/p/locations/us-central1/endpoints/e");
CREATE TABLE t (x INT64) PRIMARY KEY (x);
CREATE VIEW v SQL SECURITY INVOKER AS SELECT z FROM ML.PREDICT(MODEL m, (SELECT t.x FROM t));
CREATE PROPERTY GRAPH g NODE TABLES (v KEY (z));`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`DROP VIEW v`,
				`CREATE OR REPLACE MODEL m INPUT (x INT64) OUTPUT (z INT64) REMOTE OPTIONS (endpoint = "//aiplatform.googleapis.com/projects/p/locations/us-central1/endpoints/e")`,
				`CREATE VIEW v SQL SECURITY INVOKER AS SELECT z FROM ML.PREDICT(MODEL m, (SELECT t.x FROM t))`,
				`CREATE PROPERTY GRAPH g NODE TABLES (v KEY (z))`,
			},
		},
		{
			name: "graph is rebuilt when its table is recreated for a new placement key",
			from: `
CREATE PLACEMENT p OPTIONS (instance_partition = 'ip');
CREATE TABLE n (id INT64 NOT NULL, loc STRING(MAX) NOT NULL PLACEMENT KEY) PRIMARY KEY (id);
CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			to: `
CREATE PLACEMENT p OPTIONS (instance_partition = 'ip');
CREATE TABLE n (id INT64 NOT NULL, loc STRING(MAX) NOT NULL, loc2 STRING(MAX) NOT NULL PLACEMENT KEY) PRIMARY KEY (id);
CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`DROP TABLE n`,
				"CREATE TABLE n (\n  id INT64 NOT NULL,\n  loc STRING(MAX) NOT NULL,\n  loc2 STRING(MAX) NOT NULL PLACEMENT KEY\n) PRIMARY KEY (id)",
				`CREATE PROPERTY GRAPH g NODE TABLES (n)`,
			},
		},
		{
			name: "graph over a view equals the expanded readback",
			from: graphView + `CREATE PROPERTY GRAPH g NODE TABLES (v KEY (id));`,
			to:   graphView + `CREATE PROPERTY GRAPH g NODE TABLES (v KEY (id) LABEL v PROPERTIES (id, name));`,
		},
		{
			name: "graph is rebuilt when a function its property calls is recreated",
			from: graphTables + `
CREATE FUNCTION f(x STRING) RETURNS STRING SQL SECURITY INVOKER AS (x);
CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) PROPERTIES (f(name) AS fname));`,
			to: graphTables + `
CREATE FUNCTION f(x STRING, y STRING) RETURNS STRING SQL SECURITY INVOKER AS (x);
CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) PROPERTIES (f(name, name) AS fname));`,
			expected: []string{
				`DROP PROPERTY GRAPH g`,
				`CREATE OR REPLACE FUNCTION f (x STRING, y STRING) RETURNS STRING SQL SECURITY INVOKER AS (x)`,
				`CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) PROPERTIES (f(name, name) AS fname))`,
			},
		},
		{
			name: "element order does not matter",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n AS a, e AS b);`,
			to:   graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (e AS b, n AS a);`,
		},
		{
			name: "replacing a table with a graph of the same name drops the table first",
			from: graphTables + `CREATE TABLE g (id INT64 NOT NULL) PRIMARY KEY (id);`,
			to:   graphTables + graphShorthand,
			expected: []string{
				`DROP TABLE g`,
				`CREATE PROPERTY GRAPH g NODE TABLES (n) EDGE TABLES (e SOURCE KEY (id) REFERENCES n (id) DESTINATION KEY (dst) REFERENCES n (id))`,
			},
		},
		{
			name: "ALL COLUMNS EXCEPT equals the remaining columns",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) PROPERTIES ARE ALL COLUMNS EXCEPT (name));`,
			to:   graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) PROPERTIES (id));`,
		},
		{
			name: "element alias change rebuilds the graph",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n AS a);`,
			to:   graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n AS b);`,
			expected: []string{
				`CREATE OR REPLACE PROPERTY GRAPH g NODE TABLES (n AS b)`,
			},
		},
		{
			name: "alias is the default label",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n AS a);`,
			to:   graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n AS a LABEL a PROPERTIES (id, name));`,
		},
		{
			name: "label order does not matter",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n LABEL a PROPERTIES (id) LABEL b PROPERTIES (name));`,
			to:   graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n LABEL b PROPERTIES (name) LABEL a PROPERTIES (id));`,
		},
		{
			name: "label properties change rebuilds the graph",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n LABEL a PROPERTIES (id));`,
			to:   graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n LABEL a PROPERTIES (name));`,
			expected: []string{
				`CREATE OR REPLACE PROPERTY GRAPH g NODE TABLES (n LABEL a PROPERTIES (name))`,
			},
		},
		{
			name: "NO PROPERTIES differs from ALL COLUMNS",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) LABEL n NO PROPERTIES);`,
			to:   graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) LABEL n);`,
			expected: []string{
				`CREATE OR REPLACE PROPERTY GRAPH g NODE TABLES (n KEY (id) LABEL n)`,
			},
		},
		{
			name: "graph without properties is kept when a column is dropped",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) LABEL n NO PROPERTIES);`,
			to: `
CREATE TABLE n (id INT64 NOT NULL) PRIMARY KEY (id);
CREATE TABLE e (id INT64 NOT NULL, dst INT64 NOT NULL) PRIMARY KEY (id, dst);
CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) LABEL n NO PROPERTIES);`,
			expected: []string{
				`ALTER TABLE n DROP COLUMN name`,
			},
		},
		{
			name: "derived property alias change rebuilds the graph",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) PROPERTIES (UPPER(name) AS uname));`,
			to:   graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id) PROPERTIES (UPPER(name) AS shout));`,
			expected: []string{
				`CREATE OR REPLACE PROPERTY GRAPH g NODE TABLES (n KEY (id) PROPERTIES (UPPER(name) AS shout))`,
			},
		},
		{
			name: "dynamic label change rebuilds the graph",
			from: graphDynamic + `CREATE PROPERTY GRAPH g NODE TABLES (d KEY (id));`,
			to:   graphDynamic + `CREATE PROPERTY GRAPH g NODE TABLES (d KEY (id) DYNAMIC LABEL (lbl));`,
			expected: []string{
				`CREATE OR REPLACE PROPERTY GRAPH g NODE TABLES (d KEY (id) DYNAMIC LABEL (lbl))`,
			},
		},
		{
			name: "dynamic properties change rebuilds the graph",
			from: graphDynamic + `CREATE PROPERTY GRAPH g NODE TABLES (d KEY (id));`,
			to:   graphDynamic + `CREATE PROPERTY GRAPH g NODE TABLES (d KEY (id) DYNAMIC PROPERTIES (props));`,
			expected: []string{
				`CREATE OR REPLACE PROPERTY GRAPH g NODE TABLES (d KEY (id) DYNAMIC PROPERTIES (props))`,
			},
		},
		{
			name: "graph options change rebuilds the graph",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n);`,
			to:   graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n) OPTIONS (validate_element_key_uniqueness = false);`,
			expected: []string{
				`CREATE OR REPLACE PROPERTY GRAPH g NODE TABLES (n) OPTIONS (validate_element_key_uniqueness = false)`,
			},
		},
		{
			name: "names are case-insensitive",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n LABEL Person PROPERTIES (id));`,
			to:   graphTables + `CREATE PROPERTY GRAPH G NODE TABLES (N LABEL person PROPERTIES (ID));`,
		},
		{
			name: "REFERENCES defaults to the node key",
			from: graphTables + `CREATE PROPERTY GRAPH g NODE TABLES (n) EDGE TABLES (e SOURCE KEY (id) REFERENCES n DESTINATION KEY (dst) REFERENCES n);`,
			to:   graphTables + graphShorthand,
		},
		{
			name: "REFERENCES columns change rebuilds the graph",
			from: graphKeys + `CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id, k)) EDGE TABLES (e SOURCE KEY (id, k) REFERENCES n (id, k) DESTINATION KEY (dst, k) REFERENCES n (id, k));`,
			to:   graphKeys + `CREATE PROPERTY GRAPH g NODE TABLES (n KEY (id, k)) EDGE TABLES (e SOURCE KEY (id, k) REFERENCES n (k, id) DESTINATION KEY (dst, k) REFERENCES n (id, k));`,
			expected: []string{
				`CREATE OR REPLACE PROPERTY GRAPH g NODE TABLES (n KEY (id, k)) EDGE TABLES (e SOURCE KEY (id, k) REFERENCES n (k, id) DESTINATION KEY (dst, k) REFERENCES n (id, k))`,
			},
		},
	})
}

const graphParent = `
CREATE TABLE p (pid INT64 NOT NULL) PRIMARY KEY (pid);
CREATE TABLE c (pid INT64 NOT NULL, cid INT64 NOT NULL) PRIMARY KEY (pid, cid), INTERLEAVE IN PARENT p;
`

const graphParentDefault = `
CREATE TABLE p (pid INT64 NOT NULL DEFAULT (0)) PRIMARY KEY (pid);
CREATE TABLE c (pid INT64 NOT NULL, cid INT64 NOT NULL) PRIMARY KEY (pid, cid), INTERLEAVE IN PARENT p;
`

const graphView = `
CREATE TABLE t (id INT64 NOT NULL, name STRING(MAX)) PRIMARY KEY (id);
CREATE VIEW v SQL SECURITY INVOKER AS SELECT t.id, t.name FROM t;
`

const graphDynamic = `CREATE TABLE d (id INT64 NOT NULL, lbl STRING(MAX), props JSON) PRIMARY KEY (id);`

const graphKeys = `
CREATE TABLE n (id INT64 NOT NULL, k INT64 NOT NULL) PRIMARY KEY (id, k);
CREATE TABLE e (id INT64 NOT NULL, dst INT64 NOT NULL, k INT64 NOT NULL) PRIMARY KEY (id, dst, k);
`
