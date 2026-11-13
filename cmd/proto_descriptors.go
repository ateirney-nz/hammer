package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/daichirata/hammer/internal/hammer"
)

// readDDL reads the schema of source. A live database brings its own proto descriptors; for a
// file or stdin they come from descriptorsFile, the value of the named flag.
func readDDL(ctx context.Context, source hammer.Source, flag, descriptorsFile string, option *hammer.DDLOption) (hammer.DDL, error) {
	if descriptorsFile != "" && hammer.Scheme(source.String()) == "spanner" {
		return hammer.DDL{}, fmt.Errorf("--%s cannot be used with %s: its proto descriptors are read from the database", flag, source)
	}
	ddl, err := source.DDL(ctx, option)
	if err != nil || descriptorsFile == "" || option.IgnoreProtoBundles {
		return ddl, err
	}
	ddl.ProtoDescriptors, err = os.ReadFile(descriptorsFile)
	return ddl, err
}
