package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/daichirata/hammer/internal/hammer"
)

var (
	applyExample = `
* Apply local schema file
  hammer apply spanner://projects/projectId/instances/instanceId/databases/databaseName /path/to/file

* Apply schema from standard input
  cat /path/to/file | hammer apply spanner://projects/projectId/instances/instanceId/databases/databaseName -`

	applyCmd = &cobra.Command{
		Use:     "apply DATABASE SOURCE",
		Short:   "Apply schema",
		Example: applyExample,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("must specify 2 arguments")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			databaseURI := args[0]
			sourceURI := args[1]

			ignoreAlterDatabase, err := cmd.Flags().GetBool("ignore-alter-database")
			if err != nil {
				return err
			}
			ignoreChangeStreams, err := cmd.Flags().GetBool("ignore-change-streams")
			if err != nil {
				return err
			}
			ignoreModels, err := cmd.Flags().GetBool("ignore-models")
			if err != nil {
				return err
			}
			ignoreProtoBundles, err := cmd.Flags().GetBool("ignore-proto-bundles")
			if err != nil {
				return err
			}
			descriptors, err := cmd.Flags().GetString("proto-descriptors")
			if err != nil {
				return err
			}
			derive, err := cmd.Flags().GetBool("derive-proto-bundle")
			if err != nil {
				return err
			}
			ddlOption := &hammer.DDLOption{
				IgnoreAlterDatabase: ignoreAlterDatabase,
				IgnoreChangeStreams: ignoreChangeStreams,
				IgnoreModels:        ignoreModels,
				IgnoreProtoBundles:  ignoreProtoBundles,
			}

			if hammer.Scheme(databaseURI) != "spanner" {
				return fmt.Errorf("DATABASE must be a spanner URI")
			}
			database, err := hammer.NewSpannerSource(ctx, databaseURI)
			if err != nil {
				return err
			}
			source, err := hammer.NewSource(ctx, sourceURI)
			if err != nil {
				return err
			}

			databaseDDL, err := database.DDL(ctx, ddlOption)
			if err != nil {
				return err
			}
			sourceDDL, err := readDDL(ctx, source, "proto-descriptors", descriptors, derive, ddlOption)
			if err != nil {
				return err
			}

			ddl, err := hammer.Diff(databaseDDL, sourceDDL)
			if err != nil {
				return err
			}
			if len(ddl.Notes) > 0 {
				return fmt.Errorf("refusing to apply a diff that skipped comparisons, pass --proto-descriptors:\n  %s", strings.Join(ddl.Notes, "\n  "))
			}
			if len(ddl.List) == 0 {
				return nil
			}

			allowDataLoss, err := cmd.Flags().GetBool("allow-data-loss")
			if err != nil {
				return err
			}
			if losses := ddl.DataLosses(); len(losses) > 0 && !allowDataLoss {
				var b strings.Builder
				for _, l := range losses {
					fmt.Fprintf(&b, "\n  %s (%s)", l.SQL(), l.Reason)
				}
				return fmt.Errorf("refusing to apply statements that lose data, pass --allow-data-loss to proceed:%s", b.String())
			}

			if err := database.Apply(ctx, ddl); err != nil {
				return err
			}
			return nil
		},
	}
)

func init() {
	applyCmd.Flags().Bool("allow-data-loss", false, "apply statements that drop tables, columns, queues or sequences")
	applyCmd.Flags().Bool("ignore-alter-database", false, "ignore alter database statements")
	applyCmd.Flags().Bool("ignore-change-streams", false, "ignore change streams statements")
	applyCmd.Flags().Bool("ignore-models", false, "ignore model statements")
	applyCmd.Flags().Bool("ignore-proto-bundles", false, "ignore proto bundle statements")
	applyCmd.Flags().Bool("derive-proto-bundle", false, "build the proto bundle from the types the schema uses, ignoring any CREATE PROTO BUNDLE; needs proto descriptors")
	applyCmd.Flags().String("proto-descriptors", "", "proto descriptors file (FileDescriptorSet) of SOURCE, a file or standard input")
	applyCmd.MarkFlagsMutuallyExclusive("derive-proto-bundle", "ignore-proto-bundles")

	rootCmd.AddCommand(applyCmd)
}
