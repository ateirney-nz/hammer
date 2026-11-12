package hammer_test

import "testing"

func TestDiffLocalityGroup(t *testing.T) {
	runDiffTests(t, []diffTestCase{
		{
			name: "create locality group",
			from: ``,
			to: `
CREATE LOCALITY GROUP spill OPTIONS (storage = 'ssd', ssd_to_hdd_spill_timespan = '10d');
`,
			expected: []string{
				`CREATE LOCALITY GROUP spill OPTIONS (storage = "ssd", ssd_to_hdd_spill_timespan = "10d")`,
			},
		},
		{
			name: "drop locality group",
			from: `
CREATE LOCALITY GROUP spill OPTIONS (storage = 'ssd');
`,
			to: ``,
			expected: []string{
				`DROP LOCALITY GROUP spill`,
			},
		},
		{
			name: "same locality group",
			from: `
CREATE LOCALITY GROUP spill OPTIONS (storage = 'ssd', ssd_to_hdd_spill_timespan = '10d');
`,
			to: `
CREATE LOCALITY GROUP spill OPTIONS (ssd_to_hdd_spill_timespan = '10d', storage = 'ssd');
`,
			expected: []string{},
		},
		{
			name: "alter locality group options",
			from: `
CREATE LOCALITY GROUP spill OPTIONS (storage = 'ssd', ssd_to_hdd_spill_timespan = '10d');
`,
			to: `
CREATE LOCALITY GROUP spill OPTIONS (storage = 'ssd', ssd_to_hdd_spill_timespan = '30d');
`,
			expected: []string{
				`ALTER LOCALITY GROUP spill SET OPTIONS (storage = "ssd", ssd_to_hdd_spill_timespan = "30d")`,
			},
		},
		{
			name: "remove a locality group option resets it",
			from: `
CREATE LOCALITY GROUP spill OPTIONS (storage = 'ssd', ssd_to_hdd_spill_timespan = '10d');
`,
			to: `
CREATE LOCALITY GROUP spill OPTIONS (storage = 'ssd');
`,
			expected: []string{
				`ALTER LOCALITY GROUP spill SET OPTIONS (storage = "ssd", ssd_to_hdd_spill_timespan = null)`,
			},
		},
		{
			name: "alter the default locality group",
			from: ``,
			to: `
ALTER LOCALITY GROUP ` + "`default`" + ` SET OPTIONS (storage = 'ssd', ssd_to_hdd_spill_timespan = '10d');
`,
			expected: []string{
				"ALTER LOCALITY GROUP `default` SET OPTIONS (storage = \"ssd\", ssd_to_hdd_spill_timespan = \"10d\")",
			},
		},
		{
			name: "change the default locality group options",
			from: `
ALTER LOCALITY GROUP ` + "`default`" + ` SET OPTIONS (storage = 'ssd', ssd_to_hdd_spill_timespan = '10d');
`,
			to: `
ALTER LOCALITY GROUP ` + "`default`" + ` SET OPTIONS (storage = 'ssd', ssd_to_hdd_spill_timespan = '30d');
`,
			expected: []string{
				"ALTER LOCALITY GROUP `default` SET OPTIONS (storage = \"ssd\", ssd_to_hdd_spill_timespan = \"30d\")",
			},
		},
		{
			name: "removing the default locality group alteration resets its options",
			from: `
ALTER LOCALITY GROUP ` + "`default`" + ` SET OPTIONS (storage = 'ssd', ssd_to_hdd_spill_timespan = '10d');
`,
			to: ``,
			expected: []string{
				"ALTER LOCALITY GROUP `default` SET OPTIONS (storage = null, ssd_to_hdd_spill_timespan = null)",
			},
		},
		{
			name: "create locality group before the table and column that use it",
			from: `
CREATE TABLE t1 (id INT64 NOT NULL, c STRING(MAX)) PRIMARY KEY(id);
`,
			to: `
CREATE LOCALITY GROUP spill OPTIONS (storage = 'ssd');
CREATE TABLE t1 (id INT64 NOT NULL, c STRING(MAX) OPTIONS (locality_group = 'spill')) PRIMARY KEY(id), OPTIONS (locality_group = 'spill');
CREATE TABLE t2 (id INT64 NOT NULL) PRIMARY KEY(id), OPTIONS (locality_group = 'spill');
`,
			expected: []string{
				`CREATE LOCALITY GROUP spill OPTIONS (storage = "ssd")`,
				`ALTER TABLE t1 SET OPTIONS (locality_group = "spill")`,
				`ALTER TABLE t1 ALTER COLUMN c SET OPTIONS (locality_group = "spill")`,
				"CREATE TABLE t2 (\n  id INT64 NOT NULL\n) PRIMARY KEY (id), OPTIONS (locality_group = \"spill\")",
			},
		},
		{
			name: "drop locality group after the table and column that used it",
			from: `
CREATE LOCALITY GROUP spill OPTIONS (storage = 'ssd');
CREATE TABLE t1 (id INT64 NOT NULL, c STRING(MAX) OPTIONS (locality_group = 'spill')) PRIMARY KEY(id), OPTIONS (locality_group = 'spill');
`,
			to: `
CREATE TABLE t1 (id INT64 NOT NULL, c STRING(MAX)) PRIMARY KEY(id);
`,
			expected: []string{
				`ALTER TABLE t1 SET OPTIONS (locality_group = "default")`,
				`ALTER TABLE t1 ALTER COLUMN c SET OPTIONS (locality_group = "default")`,
				`DROP LOCALITY GROUP spill`,
			},
		},
		{
			name: "move a table and a column between locality groups",
			from: `
CREATE LOCALITY GROUP a OPTIONS (storage = 'ssd');
CREATE LOCALITY GROUP b OPTIONS (storage = 'hdd');
CREATE TABLE t1 (id INT64 NOT NULL, c STRING(MAX) OPTIONS (locality_group = 'a')) PRIMARY KEY(id), OPTIONS (locality_group = 'a');
`,
			to: `
CREATE LOCALITY GROUP b OPTIONS (storage = 'hdd');
CREATE TABLE t1 (id INT64 NOT NULL, c STRING(MAX) OPTIONS (locality_group = 'b')) PRIMARY KEY(id), OPTIONS (locality_group = 'b');
`,
			expected: []string{
				`ALTER TABLE t1 SET OPTIONS (locality_group = "b")`,
				`ALTER TABLE t1 ALTER COLUMN c SET OPTIONS (locality_group = "b")`,
				`DROP LOCALITY GROUP a`,
			},
		},
		{
			name: "a table in the default locality group is the same as one with no locality group",
			from: `
CREATE TABLE t1 (id INT64 NOT NULL, c STRING(MAX) OPTIONS (locality_group = 'default')) PRIMARY KEY(id), OPTIONS (locality_group = 'default');
`,
			to: `
CREATE TABLE t1 (id INT64 NOT NULL, c STRING(MAX)) PRIMARY KEY(id);
`,
			expected: []string{},
		},
		{
			name: "locality group on a timestamp column keeps allow_commit_timestamp",
			from: `
CREATE LOCALITY GROUP a OPTIONS (storage = 'ssd');
CREATE TABLE t1 (id INT64 NOT NULL, ts TIMESTAMP OPTIONS (allow_commit_timestamp = true)) PRIMARY KEY(id);
`,
			to: `
CREATE LOCALITY GROUP a OPTIONS (storage = 'ssd');
CREATE TABLE t1 (id INT64 NOT NULL, ts TIMESTAMP OPTIONS (allow_commit_timestamp = true, locality_group = 'a')) PRIMARY KEY(id);
`,
			expected: []string{
				`ALTER TABLE t1 ALTER COLUMN ts SET OPTIONS (locality_group = "a")`,
			},
		},
		{
			name: "drop a table and its locality group",
			from: `
CREATE LOCALITY GROUP a OPTIONS (storage = 'ssd');
CREATE TABLE t1 (id INT64 NOT NULL) PRIMARY KEY(id), OPTIONS (locality_group = 'a');
`,
			to: ``,
			expected: []string{
				`DROP TABLE t1`,
				`DROP LOCALITY GROUP a`,
			},
		},
		{
			name: "altering a locality group other than the default is rejected",
			from: ``,
			to: `
ALTER LOCALITY GROUP spill SET OPTIONS (storage = 'ssd');
`,
			wantErr: "unexpected ddl statement",
		},
		{
			name: "altering the default locality group twice is rejected",
			from: ``,
			to: `
ALTER LOCALITY GROUP ` + "`default`" + ` SET OPTIONS (storage = 'ssd');
ALTER LOCALITY GROUP ` + "`default`" + ` SET OPTIONS (storage = 'hdd');
`,
			wantErr: "unexpected ddl statement",
		},
		{
			name: "locality group names in table options are case-insensitive",
			from: `
CREATE LOCALITY GROUP spill OPTIONS (storage = 'ssd');
CREATE TABLE t1 (id INT64 NOT NULL, c STRING(MAX) OPTIONS (locality_group = 'Spill')) PRIMARY KEY(id), OPTIONS (locality_group = 'DEFAULT');
`,
			to: `
CREATE LOCALITY GROUP spill OPTIONS (storage = 'ssd');
CREATE TABLE t1 (id INT64 NOT NULL, c STRING(MAX) OPTIONS (locality_group = 'spill')) PRIMARY KEY(id);
`,
			expected: []string{},
		},
		{
			name: "an index in the default locality group is the same as one with no locality group",
			from: `
CREATE TABLE t1 (id INT64 NOT NULL, c STRING(MAX), c_tokens TOKENLIST AS (TOKENIZE_FULLTEXT(c)) HIDDEN) PRIMARY KEY(id);
CREATE INDEX idx ON t1(c) OPTIONS (locality_group = 'default');
CREATE SEARCH INDEX sidx ON t1(c_tokens);
`,
			to: `
CREATE TABLE t1 (id INT64 NOT NULL, c STRING(MAX), c_tokens TOKENLIST AS (TOKENIZE_FULLTEXT(c)) HIDDEN) PRIMARY KEY(id);
CREATE INDEX idx ON t1(c);
CREATE SEARCH INDEX sidx ON t1(c_tokens) OPTIONS (locality_group = 'default');
`,
			expected: []string{},
		},
		{
			name: "move an index between locality groups rebuilds it",
			from: `
CREATE LOCALITY GROUP a OPTIONS (storage = 'ssd');
CREATE TABLE t1 (id INT64 NOT NULL, c STRING(MAX)) PRIMARY KEY(id);
CREATE INDEX idx ON t1(c);
`,
			to: `
CREATE LOCALITY GROUP a OPTIONS (storage = 'ssd');
CREATE TABLE t1 (id INT64 NOT NULL, c STRING(MAX)) PRIMARY KEY(id);
CREATE INDEX idx ON t1(c) OPTIONS (locality_group = 'a');
`,
			expected: []string{
				`DROP INDEX idx`,
				`CREATE INDEX idx ON t1(c) OPTIONS (locality_group = "a")`,
			},
		},
		{
			name: "allow_commit_timestamp change after a hidden change",
			from: `
CREATE TABLE t1 (id INT64 NOT NULL, ts TIMESTAMP) PRIMARY KEY(id);
`,
			to: `
CREATE TABLE t1 (id INT64 NOT NULL, ts TIMESTAMP HIDDEN OPTIONS (allow_commit_timestamp = true)) PRIMARY KEY(id);
`,
			expected: []string{
				`ALTER TABLE t1 ALTER COLUMN ts TIMESTAMP HIDDEN`,
				`ALTER TABLE t1 ALTER COLUMN ts SET OPTIONS (allow_commit_timestamp = true)`,
			},
		},
		{
			name: "allow_commit_timestamp change on a primary key column is altered in place",
			from: `
CREATE TABLE t1 (ts TIMESTAMP NOT NULL) PRIMARY KEY(ts);
`,
			to: `
CREATE TABLE t1 (ts TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true)) PRIMARY KEY(ts);
`,
			expected: []string{
				`ALTER TABLE t1 ALTER COLUMN ts SET OPTIONS (allow_commit_timestamp = true)`,
			},
		},
	})
}
