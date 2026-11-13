package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/daichirata/hammer/internal/hammer"
)

// readDDL reads the schema of source. A live database brings its own proto descriptors; for a
// file or stdin they come from descriptorsFile, the value of the named flag. With derive, a file
// or stdin schema gets the proto bundle its types need; a live database keeps the one it has.
func readDDL(ctx context.Context, source hammer.Source, flag, descriptorsFile string, derive bool, option *hammer.DDLOption) (hammer.DDL, error) {
	if descriptorsFile != "" && hammer.Scheme(source.String()) == "spanner" {
		return hammer.DDL{}, fmt.Errorf("--%s cannot be used with %s: its proto descriptors are read from the database", flag, source)
	}
	ddl, err := source.DDL(ctx, option)
	if err != nil || option.IgnoreProtoBundles {
		return ddl, err
	}
	if descriptorsFile != "" {
		if ddl.ProtoDescriptors, err = os.ReadFile(descriptorsFile); err != nil {
			return ddl, err
		}
	}
	if !derive || hammer.Scheme(source.String()) == "spanner" {
		return ddl, nil
	}
	if descriptorsFile == "" {
		return ddl, fmt.Errorf("--derive-proto-bundle needs --%s for %s", flag, source)
	}
	ddl, ignored, err := hammer.DeriveProtoBundle(ddl)
	if err != nil {
		return ddl, fmt.Errorf("--derive-proto-bundle: %s: %w", source, err)
	}
	if ignored {
		fmt.Fprintf(os.Stderr, "-- note: ignored the CREATE PROTO BUNDLE in %s, deriving it from the types the schema uses\n", source)
	}
	return ddl, nil
}
