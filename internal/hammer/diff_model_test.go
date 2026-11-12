package hammer_test

import "testing"

func TestDiffModel(t *testing.T) {
	const ep = `"//aiplatform.googleapis.com/projects/p/locations/us-central1/endpoints/e"`
	runDiffTests(t, []diffTestCase{
		{
			name: "create model",
			to:   `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `);`,
			expected: []string{
				`CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `)`,
			},
		},
		{
			name: "drop model",
			from: `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `);`,
			expected: []string{
				`DROP MODEL m`,
			},
		},
		{
			name: "same model with options in another order",
			from: `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `, default_batch_size = 5);`,
			to:   `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (default_batch_size = 5, endpoint = ` + ep + `);`,
		},
		{
			name: "change an option",
			from: `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `, default_batch_size = 5);`,
			to:   `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `, default_batch_size = 2);`,
			expected: []string{
				`ALTER MODEL m SET OPTIONS (default_batch_size = 2)`,
			},
		},
		{
			name: "remove an option",
			from: `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `, default_batch_size = 5);`,
			to:   `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `);`,
			expected: []string{
				`ALTER MODEL m SET OPTIONS (default_batch_size = null)`,
			},
		},
		{
			name: "change the output columns",
			from: `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `);`,
			to:   `CREATE MODEL m INPUT (x INT64) OUTPUT (y STRING(MAX)) REMOTE OPTIONS (endpoint = ` + ep + `);`,
			expected: []string{
				`CREATE OR REPLACE MODEL m INPUT (x INT64) OUTPUT (y STRING(MAX)) REMOTE OPTIONS (endpoint = ` + ep + `)`,
			},
		},
		{
			name: "change columns and options together",
			from: `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `, default_batch_size = 5);`,
			to:   `CREATE MODEL m INPUT (x INT64, z INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `);`,
			expected: []string{
				`CREATE OR REPLACE MODEL m INPUT (x INT64, z INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `)`,
			},
		},
		{
			name: "a NULL option is the same as an absent one",
			from: `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `);`,
			to:   `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `, default_batch_size = NULL);`,
		},
		{
			name: "set an option to NULL",
			from: `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `, default_batch_size = 5);`,
			to:   `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `, default_batch_size = NULL);`,
			expected: []string{
				`ALTER MODEL m SET OPTIONS (default_batch_size = null)`,
			},
		},
		{
			name: "model names match case-insensitively",
			from: `CREATE MODEL M INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `);`,
			to:   `CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `);`,
		},
		{
			name: "change the options of a model without INPUT and OUTPUT",
			from: `CREATE MODEL m REMOTE OPTIONS (endpoint = ` + ep + `, default_batch_size = 5);`,
			to:   `CREATE MODEL m REMOTE OPTIONS (endpoint = ` + ep + `, default_batch_size = 2);`,
			expected: []string{
				`ALTER MODEL m SET OPTIONS (default_batch_size = 2)`,
			},
		},
		{
			name: "create model before the function that calls ML.PREDICT",
			to: `
CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `);
CREATE FUNCTION f(a INT64) RETURNS INT64 SQL SECURITY INVOKER AS ((SELECT y FROM ML.PREDICT(MODEL m, (SELECT a AS x)) LIMIT 1));
`,
			expected: []string{
				`CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `)`,
				`CREATE FUNCTION f (a INT64) RETURNS INT64 SQL SECURITY INVOKER AS ((SELECT y FROM ML.PREDICT(MODEL m, (SELECT a AS x)) LIMIT 1))`,
			},
		},
		{
			name: "replace a model under the view that calls ML.PREDICT",
			from: `
CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `);
CREATE TABLE t (x INT64) PRIMARY KEY (x);
CREATE VIEW v SQL SECURITY INVOKER AS SELECT y FROM ML.PREDICT(MODEL m, (SELECT t.x FROM t));
`,
			to: `
CREATE MODEL m INPUT (x INT64) OUTPUT (z INT64) REMOTE OPTIONS (endpoint = ` + ep + `);
CREATE TABLE t (x INT64) PRIMARY KEY (x);
CREATE VIEW v SQL SECURITY INVOKER AS SELECT z FROM ML.PREDICT(MODEL m, (SELECT t.x FROM t));
`,
			expected: []string{
				`DROP VIEW v`,
				`CREATE OR REPLACE MODEL m INPUT (x INT64) OUTPUT (z INT64) REMOTE OPTIONS (endpoint = ` + ep + `)`,
				`CREATE VIEW v SQL SECURITY INVOKER AS SELECT z FROM ML.PREDICT(MODEL m, (SELECT t.x FROM t))`,
			},
		},
		{
			name: "replace a model under the function that calls ML.PREDICT",
			from: `
CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `);
CREATE FUNCTION f(a INT64) RETURNS INT64 SQL SECURITY INVOKER AS ((SELECT y FROM ML.PREDICT(MODEL m, (SELECT a AS x)) LIMIT 1));
`,
			to: `
CREATE MODEL m INPUT (x INT64) OUTPUT (y INT64, z INT64) REMOTE OPTIONS (endpoint = ` + ep + `);
CREATE FUNCTION f(a INT64) RETURNS INT64 SQL SECURITY INVOKER AS ((SELECT y FROM ML.PREDICT(MODEL m, (SELECT a AS x)) LIMIT 1));
`,
			expected: []string{
				`DROP FUNCTION f`,
				`CREATE OR REPLACE MODEL m INPUT (x INT64) OUTPUT (y INT64, z INT64) REMOTE OPTIONS (endpoint = ` + ep + `)`,
				`CREATE FUNCTION f (a INT64) RETURNS INT64 SQL SECURITY INVOKER AS ((SELECT y FROM ML.PREDICT(MODEL m, (SELECT a AS x)) LIMIT 1))`,
			},
		},
		{
			name: "create model before and drop model after a view that calls ML.PREDICT",
			from: `
CREATE MODEL mold INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `);
CREATE TABLE t (x INT64) PRIMARY KEY (x);
CREATE VIEW v SQL SECURITY INVOKER AS SELECT y FROM ML.PREDICT(MODEL mold, (SELECT t.x FROM t));
`,
			to: `
CREATE MODEL mnew INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `);
CREATE TABLE t (x INT64) PRIMARY KEY (x);
CREATE VIEW v SQL SECURITY INVOKER AS SELECT y FROM ML.PREDICT(MODEL mnew, (SELECT t.x FROM t));
`,
			expected: []string{
				`CREATE MODEL mnew INPUT (x INT64) OUTPUT (y INT64) REMOTE OPTIONS (endpoint = ` + ep + `)`,
				`CREATE OR REPLACE VIEW v SQL SECURITY INVOKER AS SELECT y FROM ML.PREDICT(MODEL mnew, (SELECT t.x FROM t))`,
				`DROP MODEL mold`,
			},
		},
	})
}
