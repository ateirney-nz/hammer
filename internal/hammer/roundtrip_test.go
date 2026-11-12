package hammer_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	database "cloud.google.com/go/spanner/admin/database/apiv1"
	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	instance "cloud.google.com/go/spanner/admin/instance/apiv1"
	"cloud.google.com/go/spanner/admin/instance/apiv1/instancepb"
	"github.com/cloudspannerecosystem/memefish/ast"
	"google.golang.org/grpc/codes"

	"github.com/daichirata/hammer/internal/hammer"
)

const emulatorInstance = "projects/test-project/instances/test-instance"

var (
	roundTripOnce     sync.Once
	roundTripInstance string
	roundTripAdmin    *database.DatabaseAdminClient
	roundTripErr      error
	roundTripEmulator bool
	databaseSeq       atomic.Int64
	databasePrefix    = "rt" + strings.ToLower(rand.Text()[:8])

	// knownRoundTripFailures are directions whose generated DDL does not round-trip today on any
	// target. Each fix removes its entries.
	knownRoundTripFailures = map[string]string{
		"TestDiffCheckConstraint/unnamed_check_constraint_is_not_dropped_before_the_column_it_references/round_trip/forward":          "Spanner rejects dropping a column an unnamed CHECK constraint references",
		"TestDiffCheckConstraint/unnamed_check_constraint_is_not_dropped_when_recreating_the_column_it_references/round_trip/forward": "Spanner rejects dropping a column an unnamed CHECK constraint references",
		"TestDiffCheckConstraint/unnamed_check_constraint_is_not_dropped_when_recreating_the_column_it_references/round_trip/reverse": "Spanner rejects dropping a column an unnamed CHECK constraint references",
		"TestDiffCheckConstraint/re-add_check_constraint_when_the_table_name_differs_only_in_case/round_trip/forward":                 "hammer writes the target's case of the table name, but Spanner DDL names are case-sensitive",
		"TestDiffCheckConstraint/re-add_check_constraint_when_the_table_name_differs_only_in_case/round_trip/reverse":                 "hammer writes the target's case of the table name, but Spanner DDL names are case-sensitive",
		"TestDiffColumn/change_column_(STRING_to_BYTES_used_by_generated_column_stays_drop_and_add)/round_trip/forward":               "Spanner rejects dropping a column a generated column references",
		"TestDiffColumn/change_column_(STRING_to_BYTES_used_by_generated_column_stays_drop_and_add)/round_trip/reverse":               "Spanner rejects dropping a column a generated column references",
	}

	// emulatorKnownRoundTripFailures are known failures on the emulator only.
	emulatorKnownRoundTripFailures = map[string]string{
		"TestDiffFunction/schema_names_are_case-sensitive/round_trip/forward":                                        "the emulator treats schema names case-insensitively",
		"TestDiffFunction/schema_names_are_case-sensitive/round_trip/reverse":                                        "the emulator treats schema names case-insensitively",
		"TestDiffAlterDatabase/remove_an_option_the_generator_has_no_special_case_for/round_trip/forward":            "the emulator rejects default_leader = null",
		"TestDiffColumn/add_hidden_attribute_to_column/round_trip/forward":                                           "the emulator rejects HIDDEN in ALTER COLUMN",
		"TestDiffColumn/add_hidden_attribute_to_column/round_trip/reverse":                                           "the emulator keeps HIDDEN when ALTER COLUMN omits it",
		"TestDiffColumn/remove_hidden_attribute_from_column/round_trip/forward":                                      "the emulator keeps HIDDEN when ALTER COLUMN omits it",
		"TestDiffColumn/remove_hidden_attribute_from_column/round_trip/reverse":                                      "the emulator rejects HIDDEN in ALTER COLUMN",
		"TestDiffLocalityGroup/create_locality_group_before_the_table_and_column_that_use_it/round_trip/forward":     "the emulator reads table and column locality groups back empty and rejects moving back to the default one",
		"TestDiffLocalityGroup/create_locality_group_before_the_table_and_column_that_use_it/round_trip/reverse":     "the emulator reads table and column locality groups back empty and rejects moving back to the default one",
		"TestDiffLocalityGroup/drop_locality_group_after_the_table_and_column_that_used_it/round_trip/forward":       "the emulator reads table and column locality groups back empty and rejects moving back to the default one",
		"TestDiffLocalityGroup/drop_locality_group_after_the_table_and_column_that_used_it/round_trip/reverse":       "the emulator reads table and column locality groups back empty and rejects moving back to the default one",
		"TestDiffLocalityGroup/move_a_table_and_a_column_between_locality_groups/round_trip/forward":                 "the emulator reads table and column locality groups back empty and rejects moving back to the default one",
		"TestDiffLocalityGroup/move_a_table_and_a_column_between_locality_groups/round_trip/reverse":                 "the emulator reads table and column locality groups back empty and rejects moving back to the default one",
		"TestDiffLocalityGroup/locality_group_on_a_timestamp_column_keeps_allow_commit_timestamp/round_trip/forward": "the emulator reads table and column locality groups back empty and rejects moving back to the default one",
		"TestDiffLocalityGroup/locality_group_on_a_timestamp_column_keeps_allow_commit_timestamp/round_trip/reverse": "the emulator reads table and column locality groups back empty and rejects moving back to the default one",
		"TestDiffLocalityGroup/drop_a_table_and_its_locality_group/round_trip/forward":                               "the emulator fails the batch with an internal RET_CHECK error (schema_graph_editor.cc:120)",
		"TestDiffLocalityGroup/drop_a_table_and_its_locality_group/round_trip/reverse":                               "the emulator reads table and column locality groups back empty and rejects moving back to the default one",
		"TestDiffLocalityGroup/move_an_index_between_locality_groups_rebuilds_it/round_trip/forward":                 "the emulator reads index locality groups back empty",
		"TestDiffLocalityGroup/move_an_index_between_locality_groups_rebuilds_it/round_trip/reverse":                 "the emulator fails DROP INDEX plus CREATE INDEX of one index in a batch with an internal RET_CHECK error (schema_graph_editor.cc:120)",
		"TestDiffLocalityGroup/allow_commit_timestamp_change_after_a_hidden_change/round_trip/forward":               "the emulator rejects HIDDEN in ALTER COLUMN",
		"TestDiffLocalityGroup/allow_commit_timestamp_change_after_a_hidden_change/round_trip/reverse":               "the emulator keeps HIDDEN when ALTER COLUMN omits it",
		"TestDiffLocalityGroup/remove_a_locality_group_option_resets_it/round_trip/forward":                          "the emulator prints a reset locality group option as a trailing comma memefish cannot parse",
		"TestDiffLocalityGroup/alter_the_default_locality_group/round_trip/reverse":                                  "the emulator prints a reset locality group option as a trailing comma memefish cannot parse",
		"TestDiffLocalityGroup/removing_the_default_locality_group_alteration_resets_its_options/round_trip/forward": "the emulator prints a reset locality group option as a trailing comma memefish cannot parse",
	}

	// invalidSchemas are cases whose from or to schema Spanner rejects on any target, so a round trip
	// cannot run. Any other rejected schema fails the case.
	invalidSchemas = map[string]string{
		"TestDiffSearchIndex/search_index_follows_ORDER_BY_column_drop+create":                                "search index column has an invalid type",
		"TestDiffSequence/removing_sequence_kind_keeps_the_existing_kind":                                     "sequence kind is missing",
		"TestDiffSequence/sequence_kind_is_case-insensitive":                                                  "sequence kind is missing",
		"TestDiffColumn/add_hidden_column":                                                                    "generated column expression does not resolve",
		"TestDiffColumn/add_hidden_column_different_pos":                                                      "generated column expression does not resolve",
		"TestDiffColumn/identity_column_empty_parameter_list_is_semantically_equivalent":                      "identity column needs a sequence kind",
		"TestDiffChangeStream/drop_change_stream_with_watch_columns":                                          "change stream lists a primary key column",
		"TestDiffChangeStream/alter_change_stream_watch_none_to_table":                                        "change stream lists a primary key column",
		"TestDiffChangeStream/alter_change_stream_watch_all_to_table":                                         "change stream lists a primary key column",
		"TestDiffChangeStream/alter_change_stream_watch_table_to_none":                                        "change stream lists a primary key column",
		"TestDiffChangeStream/alter_change_stream_watch_table_to_all":                                         "change stream lists a primary key column",
		"TestDiffChangeStream/alter_change_stream_watch_table_to_other_table":                                 "change stream lists a primary key column",
		"TestDiffChangeStream/alter_change_stream_watch_column_to_same_table":                                 "change stream lists a primary key column",
		"TestDiffChangeStream/alter_change_stream_watch_table_to_same_table":                                  "change stream lists a primary key column",
		"TestDiffChangeStream/change_stream_composite:_create,_alter_FOR,_alter_OPTIONS,_drop_in_single_diff": "more than 3 change streams track one table",
		"TestDiffForeignKey/create_table_with_constraint":                                                     "references an object the schema does not define",
		"TestDiffForeignKey/add_named_constraint":                                                             "references an object the schema does not define",
		"TestDiffForeignKey/add_unnamed_constraint":                                                           "references an object the schema does not define",
		"TestDiffForeignKey/update_named_constraint":                                                          "references an object the schema does not define",
		"TestDiffForeignKey/update_unnamed_constraint":                                                        "references an object the schema does not define",
		"TestDiffForeignKey/drop_named_constraint":                                                            "references an object the schema does not define",
		"TestDiffForeignKey/drop_unnamed_constraint":                                                          "references an object the schema does not define",
		"TestDiffForeignKey/update_constraint_referencing_new_column":                                         "references an object the schema does not define",
		"TestDiffForeignKey/drop_constraint_referencing_dropped_column":                                       "references an object the schema does not define",
		"TestDiffForeignKey/add_foreign_key_with_ALTER_TABLE":                                                 "references an object the schema does not define",
		"TestDiffForeignKey/whitespace-only_difference_is_no-op":                                              "references an object the schema does not define",
		"TestDiffSchema/create_schema_before_schema-qualified_table,_index_and_view":                          "view column is not qualified by its table",
		"TestDiffSchema/drop_schema_after_its_qualified_table,_index_and_view":                                "view column is not qualified by its table",
		"TestDiffSearchIndex/add_search_index":                                                                "generated column expression does not resolve",
		"TestDiffSearchIndex/add_advanced_search_index":                                                       "generated column expression does not resolve",
		"TestDiffSearchIndex/alter_search_index":                                                              "generated column expression does not resolve",
		"TestDiffSearchIndex/drop_search_index":                                                               "generated column expression does not resolve",
		"TestDiffSequence/create_sequence_before_table_that_uses_it":                                          "references an object the schema does not define",
		"TestDiffColumn/drop_NOT_NULL_when_FK_on_another_table_is_declared_before_the_column_owner":           "references an object the schema does not define",
		"TestDiffInterleave/change_parent_primary_key_type_recreates_interleaved_children":                    "child key does not match its parent",
		"TestDiffIdentifier/named_schema":                                                                     "references an object the schema does not define",
		"TestDiffView/drop_view":                                                                              "view uses SELECT *, which Spanner does not allow",
		"TestDiffView/create_view":                                                                            "view uses SELECT *, which Spanner does not allow",
		"TestDiffView/replace_view":                                                                           "view column is not qualified by its table",
		"TestDiffView/view_without_changes":                                                                   "view uses SELECT *, which Spanner does not allow",
		"TestDiffView/drop_view_before_table_it_references":                                                   "view uses SELECT *, which Spanner does not allow",
		"TestDiffView/drop_view_before_column_it_references":                                                  "view column is not qualified by its table",
		"TestDiffView/drop_views_in_reverse_definition_order":                                                 "view column is not qualified by its table",
		"TestDiffVectorIndex/null_option_is_the_same_as_an_absent_option":                                     "Spanner rejects a NULL vector index option",
		"TestDiffFunction/create_view_calling_a_function_declared_after_it":                                   "the view is declared before the function it calls",
		"TestDiffFunction/function_calling_a_function_declared_after_it":                                      "the function is declared before the function it calls",
		"TestDiffView/create_a_view_after_the_view_it_selects_from":                                           "view is declared before the view it selects from",
	}

	// realKnownRoundTripFailures are known failures on real Spanner only.
	realKnownRoundTripFailures = map[string]string{
		"TestDiffAlterDatabase/update_database_options/round_trip/forward":                                                                  "enable_key_visualizer = null reads back differently; investigate",
		"TestDiffCheckConstraint/set_NOT_NULL_on_a_column_referenced_by_a_check_constraint_fills_NULLs_with_its_DEFAULT/round_trip/forward": "Spanner rejects changing the default of a column a CHECK constraint references",
	}

	// emulatorInvalidSchemas are cases whose schema only the emulator rejects.
	emulatorInvalidSchemas = map[string]string{
		"TestDiffLocalityGroup/locality_group_names_in_table_options_are_case-insensitive":                           "the emulator rejects locality_group = 'default' (Locality group not found)",
		"TestDiffLocalityGroup/an_index_in_the_default_locality_group_is_the_same_as_one_with_no_locality_group":     "the emulator rejects locality_group = 'default' (Locality group not found)",
		"TestDiffFunction/change_the_body_of_a_function_without_RETURNS_under_a_view_that_calls_it":                  "the emulator cannot store a function without RETURNS",
		"TestDiffFunction/create_function_before_the_generated_column_that_calls_it":                                 "the emulator rejects a function in a stored generated column",
		"TestDiffFunction/drop_function_after_the_generated_column_that_called_it":                                   "the emulator rejects a function in a stored generated column",
		"TestDiffFunction/create_function_before_the_check_constraint_that_calls_it":                                 "the emulator rejects a function in a CHECK constraint",
		"TestDiffFunction/drop_function_after_the_check_constraint_that_called_it":                                   "the emulator rejects a function in a CHECK constraint",
		"TestDiffCheckConstraint/recreating_a_column_does_not_re-add_a_same-named_check_constraint_on_another_table": "two constraints share a name",
		"TestDiffModel/change_the_options_of_a_model_without_INPUT_and_OUTPUT":                                       "the emulator has no schema discovery for a model without INPUT and OUTPUT",
		"TestDiffChangeStream/change_stream_OPTIONS_value_change_is_still_detected":                                  "change stream retention period is out of range",
		"TestDiffAlterDatabase/remove_one_option_and_keep_another":                                                   "the emulator does not support this database option",
		"TestDiffAlterDatabase/whitespace-only_difference_is_no-op":                                                  "the emulator does not support this database option",
		"TestDiffAlterDatabase/remove_database_options_with_null":                                                    "the emulator does not support this database option",
		"TestDiffAlterDatabase/set_database_options_from_empty":                                                      "the emulator does not support this database option",
		"TestDiffAlterDatabase/update_database_options":                                                              "the emulator does not support this database option",
		"TestDiffAlterDatabase/update_to_specify_only_optimizer_version":                                             "the emulator does not support this database option",
		"TestDiffAlterDatabase/update_to_specify_only_version_retention_period":                                      "the emulator does not support this database option",
		"TestDiffAlterDatabase/update_to_specify_only_enable_key_visualizer":                                         "the emulator does not support this database option",
		"TestDiffLocalityGroup/a_table_in_the_default_locality_group_is_the_same_as_one_with_no_locality_group":      "the emulator cannot find the default locality group",
	}
)

// roundTrip applies the diff in each direction to a live database and checks that the database
// then diffs to nothing against the target, so DDL that Spanner rejects, or reads back in a form
// hammer does not treat as equal, fails the case. Equality is hammer's own Diff, so a difference
// Diff ignores (column order, identifier case, an unnamed CHECK left in place) still passes.
// It runs only when SPANNER_EMULATOR_HOST or HAMMER_TEST_INSTANCE (projects/p/instances/i on real Spanner) is set; a real instance creates
// two databases per case, so it needs a -timeout well past the default. Case lists name the targets
// they hold for: unsuffixed for every target, emulator* or real* for one.
func roundTrip(t *testing.T, from, to string, option *hammer.DDLOption) {
	t.Helper()
	inst, admin := roundTripTarget(t)
	if inst == "" {
		return
	}
	t.Run("round trip", func(t *testing.T) {
		name := strings.TrimSuffix(t.Name(), "/round_trip")
		if reason, ok := invalidSchemas[name]; ok {
			t.Skip("Spanner rejects the schema: " + reason)
		}
		if roundTripEmulator {
			if reason, ok := emulatorInvalidSchemas[name]; ok {
				t.Skip("the emulator rejects the schema: " + reason)
			}
			if reason := emulatorUnsupported(t, option, from, to); reason != "" {
				t.Skip(reason)
			}
		}
		// Both schemas must be valid on the target, or a rejected diff says nothing about hammer.
		fromClient := createDatabase(t, inst, admin, from, option)
		toClient := createDatabase(t, inst, admin, to, option)
		t.Run("forward", func(t *testing.T) { applyAndCompare(t, fromClient, from, to, option) })
		t.Run("reverse", func(t *testing.T) { applyAndCompare(t, toClient, to, from, option) })
	})
}

func applyAndCompare(t *testing.T, client *hammer.Client, from, to string, option *hammer.DDLOption) {
	target := realKnownRoundTripFailures
	if roundTripEmulator {
		target = emulatorKnownRoundTripFailures
	}
	for _, known := range []map[string]string{knownRoundTripFailures, target} {
		if reason, ok := known[t.Name()]; ok {
			t.Skip("known failure: " + reason)
		}
	}
	ctx := context.Background()
	want := parse(t, to, option)
	ddl, err := hammer.Diff(parse(t, from, option), want)
	if err != nil {
		t.Fatalf("failed to diff: %v", err)
	}
	if err := retryQuota(func() error { return client.ApplyDatabaseDDL(ctx, ddl) }); err != nil {
		t.Fatalf("Spanner rejected the diff:\n%s\nerror: %v", strings.Join(convertStrings(ddl), ";\n"), err)
	}

	var schema string
	if err := retryQuota(func() (err error) { schema, err = client.GetDatabaseDDL(ctx); return }); err != nil {
		t.Fatalf("failed to read schema back: %v", err)
	}
	rest, err := hammer.Diff(parse(t, schema, option), want)
	if err != nil {
		t.Fatalf("failed to diff schema read back: %v\n%s", err, schema)
	}
	if len(rest.List) != 0 {
		t.Errorf("schema read back differs from target after applying:\n%s\nremaining:%s", strings.Join(convertStrings(ddl), ";\n"), formatDDLMismatch(nil, convertStrings(rest)))
	}
}

// retryQuota retries f with backoff while Spanner's admin request quota is exhausted. A diff of
// several batches (only when it contains DML) may re-apply earlier batches, which then fail visibly.
func retryQuota(f func() error) error {
	delay := 2 * time.Second
	for attempt := 0; ; attempt++ {
		err := f()
		if attempt == 6 || spanner.ErrCode(err) != codes.ResourceExhausted {
			return err
		}
		time.Sleep(delay)
		delay *= 2
	}
}

func parse(t *testing.T, schema string, option *hammer.DDLOption) hammer.DDL {
	t.Helper()
	ddl, err := StringSource(schema).DDL(context.Background(), option)
	if err != nil {
		t.Fatalf("failed to parse: %v\n%s", err, schema)
	}
	return ddl
}

// emulatorUnsupported names a feature in the schemas that the emulator cannot round-trip:
// it rejects queues, and accepts roles and grants but leaves them out of GetDatabaseDdl.
func emulatorUnsupported(t *testing.T, option *hammer.DDLOption, schemas ...string) string {
	for _, schema := range schemas {
		for _, stmt := range parse(t, schema, option).List {
			switch stmt := stmt.(type) {
			case *ast.CreateQueue:
				return "the emulator does not support queues"
			case *ast.CreateVectorIndex:
				if slices.ContainsFunc(stmt.Options.Records, func(o *ast.OptionsDef) bool { return o.Name.Name == "disable_search" }) {
					return "the emulator does not support the vector index option disable_search"
				}
			case *ast.CreateRole, *ast.Grant:
				return "the emulator does not return roles and grants from GetDatabaseDdl"
			}
		}
	}
	return ""
}

func createDatabase(t *testing.T, inst string, admin *database.DatabaseAdminClient, schema string, option *hammer.DDLOption) *hammer.Client {
	ctx := context.Background()
	name := fmt.Sprintf("%s-%d", databasePrefix, databaseSeq.Add(1))
	client, err := hammer.NewClient(ctx, fmt.Sprintf("spanner://%s/databases/%s", inst, name))
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	ddl := parse(t, schema, option)
	if err := retryQuota(func() error { return client.CreateDatabase(ctx, ddl) }); err != nil {
		t.Fatalf("Spanner rejected the schema: %v", err)
	}
	t.Cleanup(func() {
		if err := admin.DropDatabase(ctx, &databasepb.DropDatabaseRequest{Database: inst + "/databases/" + name}); err != nil {
			t.Logf("failed to drop %s: %v", name, err)
		}
	})
	return client
}

func roundTripTarget(t *testing.T) (string, *database.DatabaseAdminClient) {
	roundTripOnce.Do(func() {
		roundTripInstance = os.Getenv("HAMMER_TEST_INSTANCE")
		if roundTripInstance == "" {
			if os.Getenv("SPANNER_EMULATOR_HOST") == "" {
				return
			}
			roundTripInstance, roundTripEmulator = emulatorInstance, true
			if roundTripErr = createEmulatorInstance(); roundTripErr != nil {
				return
			}
		}
		roundTripAdmin, roundTripErr = database.NewDatabaseAdminClient(context.Background())
	})
	if roundTripErr != nil {
		t.Fatalf("round trip setup failed: %v", roundTripErr)
	}
	return roundTripInstance, roundTripAdmin
}

func createEmulatorInstance() error {
	ctx := context.Background()
	admin, err := instance.NewInstanceAdminClient(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()
	op, err := admin.CreateInstance(ctx, &instancepb.CreateInstanceRequest{
		Parent:     "projects/test-project",
		InstanceId: "test-instance",
		Instance: &instancepb.Instance{
			Config:      "projects/test-project/instanceConfigs/emulator-config",
			DisplayName: "hammer round trip",
			NodeCount:   1,
		},
	})
	if spanner.ErrCode(err) == codes.AlreadyExists {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = op.Wait(ctx)
	return err
}
