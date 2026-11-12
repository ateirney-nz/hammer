package hammer_test

import "testing"

func TestDiffVectorIndex(t *testing.T) {
	runDiffTests(t, []diffTestCase{
		{
			name: "create vector index",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			expected: []string{
				`CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = "COSINE")`,
			},
		},
		{
			name: "drop vector index",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
`,
			expected: []string{
				`DROP VECTOR INDEX vi`,
			},
		},
		{
			name: "change vector index options",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'EUCLIDEAN');
`,
			expected: []string{
				`DROP VECTOR INDEX vi`,
				`CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = "EUCLIDEAN")`,
			},
		},
		{
			name: "add vector index stored column",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) STORING (t1_3) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			expected: []string{
				`ALTER VECTOR INDEX vi ADD STORED COLUMN t1_3`,
			},
		},
		{
			name: "drop vector index stored column",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) STORING (t1_3) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			expected: []string{
				`ALTER VECTOR INDEX vi DROP STORED COLUMN t1_3`,
			},
		},
		{
			name: "recreate vector index when its column is recreated",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>4),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			expected: []string{
				`DROP VECTOR INDEX vi`,
				`ALTER TABLE t1 DROP COLUMN t1_2`,
				`ALTER TABLE t1 ADD COLUMN t1_2 ARRAY<FLOAT32>(vector_length => 4)`,
				`CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = "COSINE")`,
			},
		},
		{
			name: "drop vector index before its table",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
`,
			expected: []string{
				`DROP VECTOR INDEX vi`,
				`DROP TABLE t1`,
			},
		},
		{
			name: "create table with vector index",
			from: `
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			expected: []string{
				`CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length => 3),
  t1_3 INT64
) PRIMARY KEY (t1_1)`,
				`CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = "COSINE")`,
			},
		},
		{
			name: "reordered options are not a change",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 STRING(MAX),
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE', tree_depth = 2);
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 STRING(MAX),
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (tree_depth = 2, distance_type = 'COSINE');
`,
			expected: []string{},
		},
		{
			name: "null option is the same as an absent option",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 STRING(MAX),
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 STRING(MAX),
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE', tree_depth = NULL);
`,
			expected: []string{},
		},
		{
			name: "add disable_search alters in place",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 STRING(MAX),
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 STRING(MAX),
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE', disable_search = true);
`,
			expected: []string{
				`ALTER VECTOR INDEX vi SET OPTIONS (disable_search = true)`,
			},
		},
		{
			name: "remove disable_search alters in place",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 STRING(MAX),
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE', disable_search = true);
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 STRING(MAX),
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			expected: []string{
				`ALTER VECTOR INDEX vi SET OPTIONS (disable_search = null)`,
			},
		},
		{
			name: "drop a stored column before dropping its table column",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) STORING (t1_3) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			expected: []string{
				`ALTER VECTOR INDEX vi DROP STORED COLUMN t1_3`,
				`ALTER TABLE t1 DROP COLUMN t1_3`,
			},
		},
		{
			name: "recreate a stored column",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 STRING(MAX),
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) STORING (t1_3) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) STORING (t1_3) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			expected: []string{
				`DROP VECTOR INDEX vi`,
				`ALTER TABLE t1 DROP COLUMN t1_3`,
				`ALTER TABLE t1 ADD COLUMN t1_3 INT64`,
				`CREATE VECTOR INDEX vi ON t1 (t1_2) STORING (t1_3) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = "COSINE")`,
			},
		},
		{
			name: "add a column and store it",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 STRING(MAX),
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 STRING(MAX),
  t1_4 INT64,
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) STORING (t1_4) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			expected: []string{
				`ALTER TABLE t1 ADD COLUMN t1_4 INT64`,
				`ALTER VECTOR INDEX vi ADD STORED COLUMN t1_4`,
			},
		},
		{
			name: "options change and column recreate together drop the index once",
			from: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>3),
  t1_3 STRING(MAX),
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
CREATE TABLE t1 (
  t1_1 INT64 NOT NULL,
  t1_2 ARRAY<FLOAT32>(vector_length=>4),
  t1_3 STRING(MAX),
) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'EUCLIDEAN');
`,
			expected: []string{
				`DROP VECTOR INDEX vi`,
				`ALTER TABLE t1 DROP COLUMN t1_2`,
				`ALTER TABLE t1 ADD COLUMN t1_2 ARRAY<FLOAT32>(vector_length => 4)`,
				`CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = "EUCLIDEAN")`,
			},
		},
		{
			name: "drop a stored column while another stored column is recreated",
			from: `
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 ARRAY<FLOAT32>(vector_length=>3), t1_3 STRING(MAX), t1_4 STRING(MAX)) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) STORING (t1_3, t1_4) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 ARRAY<FLOAT32>(vector_length=>3), t1_4 INT64) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) STORING (t1_4) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			expected: []string{
				`ALTER VECTOR INDEX vi DROP STORED COLUMN t1_3`,
				`DROP VECTOR INDEX vi`,
				`ALTER TABLE t1 DROP COLUMN t1_4`,
				`ALTER TABLE t1 ADD COLUMN t1_4 INT64`,
				`CREATE VECTOR INDEX vi ON t1 (t1_2) STORING (t1_4) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = "COSINE")`,
				`ALTER TABLE t1 DROP COLUMN t1_3`,
			},
		},
		{
			name: "drop a stored column while the embedding column is recreated",
			from: `
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 ARRAY<FLOAT32>(vector_length=>3), t1_3 STRING(MAX)) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) STORING (t1_3) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 ARRAY<FLOAT32>(vector_length=>4)) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			expected: []string{
				`ALTER VECTOR INDEX vi DROP STORED COLUMN t1_3`,
				`DROP VECTOR INDEX vi`,
				`ALTER TABLE t1 DROP COLUMN t1_2`,
				`ALTER TABLE t1 ADD COLUMN t1_2 ARRAY<FLOAT32>(vector_length => 4)`,
				`CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = "COSINE")`,
				`ALTER TABLE t1 DROP COLUMN t1_3`,
			},
		},
		{
			name: "disable_search with an option change recreates once",
			from: `
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 ARRAY<FLOAT32>(vector_length=>3)) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE');
`,
			to: `
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 ARRAY<FLOAT32>(vector_length=>3)) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'EUCLIDEAN', disable_search = true);
`,
			expected: []string{
				`DROP VECTOR INDEX vi`,
				`CREATE VECTOR INDEX vi ON t1 (t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = "EUCLIDEAN", disable_search = true)`,
			},
		},
		{
			name: "disable_search true to false",
			from: `
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 ARRAY<FLOAT32>(vector_length=>3)) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE', disable_search = true);
`,
			to: `
CREATE TABLE t1 (t1_1 INT64 NOT NULL, t1_2 ARRAY<FLOAT32>(vector_length=>3)) PRIMARY KEY(t1_1);
CREATE VECTOR INDEX vi ON t1(t1_2) WHERE t1_2 IS NOT NULL OPTIONS (distance_type = 'COSINE', disable_search = false);
`,
			expected: []string{
				`ALTER VECTOR INDEX vi SET OPTIONS (disable_search = false)`,
			},
		},
	})
}
