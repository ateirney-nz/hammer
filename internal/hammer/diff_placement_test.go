package hammer_test

import "testing"

func TestDiffPlacement(t *testing.T) {
	const p = `CREATE PLACEMENT p OPTIONS (instance_partition = 'ip');`
	const users = `CREATE TABLE Users (Id INT64 NOT NULL, Loc STRING(MAX) NOT NULL PLACEMENT KEY) PRIMARY KEY (Id);`
	const plain = `CREATE TABLE Users (Id INT64 NOT NULL, Loc STRING(MAX) NOT NULL) PRIMARY KEY (Id);`
	const usersDDL = "CREATE TABLE Users (\n  Id INT64 NOT NULL,\n  Loc STRING(MAX) NOT NULL PLACEMENT KEY\n) PRIMARY KEY (Id)"
	const plainDDL = "CREATE TABLE Users (\n  Id INT64 NOT NULL,\n  Loc STRING(MAX) NOT NULL\n) PRIMARY KEY (Id)"
	runDiffTests(t, []diffTestCase{
		{
			name:     "create placement before the table that uses it",
			to:       users + p,
			expected: []string{`CREATE PLACEMENT p OPTIONS (instance_partition = "ip") `, usersDDL},
		},
		{
			name:     "drop placement after its table",
			from:     users + p,
			to:       ``,
			expected: []string{`DROP TABLE Users`, `DROP PLACEMENT p`},
		},
		{
			name: "same placement",
			from: p,
			to:   `CREATE PLACEMENT p OPTIONS (instance_partition = 'ip');`,
		},
		{
			name:    "change placement options",
			from:    p,
			to:      `CREATE PLACEMENT p OPTIONS (instance_partition = 'ip2');`,
			wantErr: "create a new placement, move the rows to it, then drop the old one",
		},
		{
			name:     "placement names are case-sensitive",
			from:     p,
			to:       `CREATE PLACEMENT P OPTIONS (instance_partition = 'ip');`,
			expected: []string{`CREATE PLACEMENT P OPTIONS (instance_partition = "ip") `, `DROP PLACEMENT p`},
		},
		{
			name: "placement key column name is case-insensitive",
			from: users,
			to:   `CREATE TABLE Users (Id INT64 NOT NULL, loc STRING(MAX) NOT NULL PLACEMENT KEY) PRIMARY KEY (Id);`,
		},
		{
			name:     "add a placement key recreates the table",
			from:     plain,
			to:       users,
			expected: []string{`DROP TABLE Users`, usersDDL},
		},
		{
			name:     "remove a placement key recreates the table",
			from:     users,
			to:       plain,
			expected: []string{`DROP TABLE Users`, plainDDL},
		},
		{
			name:     "move a placement key recreates the table",
			from:     users,
			to:       `CREATE TABLE Users (Id INT64 NOT NULL, Loc STRING(MAX) NOT NULL, Loc2 STRING(MAX) NOT NULL PLACEMENT KEY) PRIMARY KEY (Id);`,
			expected: []string{`DROP TABLE Users`, "CREATE TABLE Users (\n  Id INT64 NOT NULL,\n  Loc STRING(MAX) NOT NULL,\n  Loc2 STRING(MAX) NOT NULL PLACEMENT KEY\n) PRIMARY KEY (Id)"},
		},
		{
			name: "same placement key",
			from: users,
			to:   users,
		},
	})
}
