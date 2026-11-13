package hammer_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/daichirata/hammer/internal/hammer"
)

// protoMsg builds message pkg.<name> from "field:number" int64 fields.
func protoMsg(name string, fields ...string) *descriptorpb.DescriptorProto {
	m := &descriptorpb.DescriptorProto{Name: proto.String(name)}
	for _, f := range fields {
		fname, number, _ := strings.Cut(f, ":")
		m.Field = append(m.Field, &descriptorpb.FieldDescriptorProto{
			Name: proto.String(fname), Number: proto.Int32(int32(atoi(number))),
			Type:  descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum(),
			Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		})
	}
	return m
}

// protoEnum builds enum pkg.<name> from "VALUE:number" values.
func protoEnum(name string, values ...string) *descriptorpb.EnumDescriptorProto {
	e := &descriptorpb.EnumDescriptorProto{Name: proto.String(name)}
	for _, v := range values {
		vname, number, _ := strings.Cut(v, ":")
		e.Value = append(e.Value, &descriptorpb.EnumValueDescriptorProto{Name: proto.String(vname), Number: proto.Int32(int32(atoi(number)))})
	}
	return e
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// protoDescriptors serializes one proto2 file in package pkg; proto3 would force enums to start at zero.
func protoDescriptors(items ...any) []byte {
	file := &descriptorpb.FileDescriptorProto{Name: proto.String("pkg/x.proto"), Package: proto.String("pkg")}
	for _, item := range items {
		switch item := item.(type) {
		case *descriptorpb.DescriptorProto:
			file.MessageType = append(file.MessageType, item)
		case *descriptorpb.EnumDescriptorProto:
			file.EnumType = append(file.EnumType, item)
		}
	}
	b, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{file}})
	if err != nil {
		panic(err)
	}
	return b
}

var (
	msgA   = protoMsg("Msg", "a:1")
	msgAB  = protoMsg("Msg", "a:1", "b:2")
	kind   = protoEnum("Kind", "KIND_UNSPECIFIED:0", "K1:1")
	kind2  = protoEnum("Kind", "KIND_UNSPECIFIED:0", "K1:1", "K2:2")
	table  = "CREATE TABLE t (id INT64 NOT NULL) PRIMARY KEY (id);\n"
	tableM = "CREATE TABLE t (id INT64 NOT NULL, m pkg.Msg) PRIMARY KEY (id);\n"
)

func TestDiffProtoBundle_Statements(t *testing.T) {
	runDiffTests(t, []diffTestCase{
		{
			name:          "create the bundle",
			from:          table,
			to:            "CREATE PROTO BUNDLE (pkg.Msg, pkg.Kind);\n" + table,
			toDescriptors: protoDescriptors(msgA, kind),
			expected:      []string{"CREATE PROTO BUNDLE (pkg.Msg, pkg.Kind)"},
		},
		{
			name:            "drop the bundle",
			from:            "CREATE PROTO BUNDLE (pkg.Msg, pkg.Kind);\n" + table,
			to:              table,
			fromDescriptors: protoDescriptors(msgA, kind),
			expected:        []string{"DROP PROTO BUNDLE"},
		},
		{
			name:            "insert a type",
			from:            "CREATE PROTO BUNDLE (pkg.Msg);\n" + table,
			to:              "CREATE PROTO BUNDLE (pkg.Msg, pkg.Kind);\n" + table,
			fromDescriptors: protoDescriptors(msgA, kind),
			toDescriptors:   protoDescriptors(msgA, kind),
			expected:        []string{"ALTER PROTO BUNDLE INSERT (pkg.Kind)"},
		},
		{
			name:            "delete a type",
			from:            "CREATE PROTO BUNDLE (pkg.Msg, pkg.Kind);\n" + table,
			to:              "CREATE PROTO BUNDLE (pkg.Msg);\n" + table,
			fromDescriptors: protoDescriptors(msgA, kind),
			toDescriptors:   protoDescriptors(msgA, kind),
			expected:        []string{"ALTER PROTO BUNDLE DELETE (pkg.Kind)"},
		},
		{
			name:            "update types that gain a field or an enum value, beside an insert",
			from:            "CREATE PROTO BUNDLE (pkg.Msg, pkg.Kind);\n" + table,
			to:              "CREATE PROTO BUNDLE (pkg.Msg, pkg.Kind, pkg.Other);\n" + table,
			fromDescriptors: protoDescriptors(msgA, kind, protoMsg("Other", "x:1")),
			toDescriptors:   protoDescriptors(msgAB, kind2, protoMsg("Other", "x:1")),
			expected:        []string{"ALTER PROTO BUNDLE INSERT (pkg.Other) UPDATE (pkg.Msg, pkg.Kind)"},
		},
		{
			name:            "the bundle list order does not matter",
			from:            "CREATE PROTO BUNDLE (pkg.Msg, pkg.Kind);\n" + table,
			to:              "CREATE PROTO BUNDLE (\n  pkg.Kind,\n  pkg.Msg,\n);\n" + table,
			fromDescriptors: protoDescriptors(msgA, kind),
			toDescriptors:   protoDescriptors(msgA, kind),
			expected:        []string{},
		},
		{
			name:            "options and file details are not an update",
			from:            "CREATE PROTO BUNDLE (pkg.Msg);\n" + table,
			to:              "CREATE PROTO BUNDLE (pkg.Msg);\n" + table,
			fromDescriptors: protoDescriptors(msgA),
			toDescriptors:   protoDescriptors(&descriptorpb.DescriptorProto{Name: msgA.Name, Field: msgA.Field, Options: &descriptorpb.MessageOptions{Deprecated: proto.Bool(true)}}),
			expected:        []string{},
		},
		{
			name:            "backtick-quoted types read back from Spanner equal the schema's",
			from:            "CREATE PROTO BUNDLE (\n  pkg.Msg,\n);\nCREATE TABLE t (id INT64 NOT NULL, m `pkg.Msg`, ms ARRAY<`pkg.Msg`>) PRIMARY KEY (id);\n",
			to:              "CREATE PROTO BUNDLE (pkg.Msg);\nCREATE TABLE t (id INT64 NOT NULL, m pkg.Msg, ms ARRAY<pkg.Msg>) PRIMARY KEY (id);\n",
			fromDescriptors: protoDescriptors(msgA),
			toDescriptors:   protoDescriptors(msgA),
			expected:        []string{},
		},
		{
			name:            "insert a type before the column that uses it",
			from:            "CREATE PROTO BUNDLE (pkg.Msg);\n" + tableM,
			to:              "CREATE PROTO BUNDLE (pkg.Msg, pkg.Kind);\nCREATE TABLE t (id INT64 NOT NULL, m pkg.Msg, k pkg.Kind) PRIMARY KEY (id);\n",
			fromDescriptors: protoDescriptors(msgA, kind),
			toDescriptors:   protoDescriptors(msgA, kind),
			expected:        []string{"ALTER PROTO BUNDLE INSERT (pkg.Kind)", "ALTER TABLE t ADD COLUMN k pkg.Kind"},
		},
		{
			name:            "delete a type after the column that used it",
			from:            "CREATE PROTO BUNDLE (pkg.Msg, pkg.Kind);\nCREATE TABLE t (id INT64 NOT NULL, m pkg.Msg, k pkg.Kind) PRIMARY KEY (id);\n",
			to:              "CREATE PROTO BUNDLE (pkg.Msg);\n" + tableM,
			fromDescriptors: protoDescriptors(msgA, kind),
			toDescriptors:   protoDescriptors(msgA, kind),
			expected:        []string{"ALTER TABLE t DROP COLUMN k", "ALTER PROTO BUNDLE DELETE (pkg.Kind)"},
		},
		{
			name:            "drop a generated column before the destructive update that removes its field",
			from:            "CREATE PROTO BUNDLE (pkg.Msg);\nCREATE TABLE t (id INT64 NOT NULL, m pkg.Msg, b INT64 AS (m.b) STORED) PRIMARY KEY (id);\n",
			to:              "CREATE PROTO BUNDLE (pkg.Msg);\n" + tableM,
			fromDescriptors: protoDescriptors(msgAB),
			toDescriptors:   protoDescriptors(msgA),
			expected:        []string{"ALTER TABLE t DROP COLUMN b", "ALTER PROTO BUNDLE UPDATE (pkg.Msg)"},
		},
		{
			name:            "drop the bundle after the table that used it",
			from:            "CREATE PROTO BUNDLE (pkg.Msg);\n" + tableM,
			to:              "",
			fromDescriptors: protoDescriptors(msgA),
			expected:        []string{"DROP TABLE t", "DROP PROTO BUNDLE"},
		},
		{
			name:               "ignore proto bundle statements and descriptors",
			from:               "CREATE PROTO BUNDLE (pkg.Msg);\n" + table,
			to:                 table,
			ignoreProtoBundles: true,
			fromDescriptors:    protoDescriptors(msgA),
			expected:           []string{},
		},
		{
			name:               "ignore a hand-written alter and drop of the bundle",
			from:               table,
			to:                 "ALTER PROTO BUNDLE INSERT (pkg.Msg);\nDROP PROTO BUNDLE;\n" + table,
			ignoreProtoBundles: true,
			expected:           []string{},
		},
		{
			name:    "alter proto bundle in an input is unsupported",
			from:    table,
			to:      "ALTER PROTO BUNDLE INSERT (pkg.Msg);\n" + table,
			wantErr: "unexpected ddl statement",
		},
		{
			name:    "drop proto bundle in an input is unsupported",
			from:    "DROP PROTO BUNDLE;\n" + table,
			to:      table,
			wantErr: "unexpected ddl statement",
		},
	})
}

func TestDiffProtoBundle_DataLoss(t *testing.T) {
	const bundle = "CREATE PROTO BUNDLE (pkg.Msg, pkg.Kind);\n"
	tests := []struct {
		name     string
		from, to []byte
		want     []string // "statement: reason"
	}{
		{"remove a field", protoDescriptors(msgAB, kind), protoDescriptors(msgA, kind),
			[]string{"ALTER PROTO BUNDLE UPDATE (pkg.Msg): pkg.Msg: field removed: b = 2"}},
		{"renumber a field", protoDescriptors(msgA, kind), protoDescriptors(protoMsg("Msg", "a:5"), kind),
			[]string{"ALTER PROTO BUNDLE UPDATE (pkg.Msg): pkg.Msg: field changed: a"}},
		{"remove an enum value", protoDescriptors(msgA, kind2), protoDescriptors(msgA, kind),
			[]string{"ALTER PROTO BUNDLE UPDATE (pkg.Kind): pkg.Kind: value removed: K2 = 2"}},
		{"renumber an enum value", protoDescriptors(msgA, kind), protoDescriptors(msgA, protoEnum("Kind", "KIND_UNSPECIFIED:0", "K1:9")),
			[]string{"ALTER PROTO BUNDLE UPDATE (pkg.Kind): pkg.Kind: value renumbered: K1 1 -> 9"}},
		{"swap message and enum", protoDescriptors(msgA, kind), protoDescriptors(msgA, protoMsg("Kind", "a:1")),
			[]string{"ALTER PROTO BUNDLE UPDATE (pkg.Kind): pkg.Kind: changed between message and enum"}},
		{"one loss per type", protoDescriptors(msgAB, kind2), protoDescriptors(msgA, kind),
			[]string{
				"ALTER PROTO BUNDLE UPDATE (pkg.Msg): pkg.Msg: field removed: b = 2",
				"ALTER PROTO BUNDLE UPDATE (pkg.Kind): pkg.Kind: value removed: K2 = 2",
			}},
		{"added field and value are not data loss", protoDescriptors(msgA, kind), protoDescriptors(msgAB, kind2), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			from, err := StringSource(bundle+table).DDL(ctx, &hammer.DDLOption{})
			if err != nil {
				t.Fatal(err)
			}
			// the new table makes "last" mean something: destructive updates wait for every other change
			to, err := StringSource(bundle+table+"CREATE TABLE u (id INT64 NOT NULL) PRIMARY KEY (id);\n").DDL(ctx, &hammer.DDLOption{})
			if err != nil {
				t.Fatal(err)
			}
			from.ProtoDescriptors, to.ProtoDescriptors = tt.from, tt.to
			ddl, err := hammer.Diff(from, to)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, l := range ddl.DataLosses() {
				got = append(got, l.SQL()+": "+l.Reason)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if len(got) == 0 {
				return
			}
			if last := ddl.List[len(ddl.List)-1]; !strings.HasPrefix(last.SQL(), "ALTER PROTO BUNDLE UPDATE") {
				t.Errorf("destructive updates are not last: %s", last.SQL())
			}
		})
	}
}

func TestDiffProtoBundle_MissingDescriptors(t *testing.T) {
	const bundle = "CREATE PROTO BUNDLE (pkg.Msg, pkg.Kind);\n"
	ctx := context.Background()
	parse := func(schema string, descriptors []byte) hammer.DDL {
		ddl, err := StringSource(schema).DDL(ctx, &hammer.DDLOption{})
		if err != nil {
			t.Fatal(err)
		}
		ddl.ProtoDescriptors = descriptors
		return ddl
	}
	to := parse(bundle+table, protoDescriptors(msgAB, kind))
	for name, from := range map[string]hammer.DDL{
		"no descriptors":               parse(bundle+table, nil),
		"a type missing from the file": parse(bundle+table, protoDescriptors(msgAB)),
	} {
		ddl, err := hammer.Diff(from, to)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(ddl.List) != 0 {
			t.Errorf("%s: want no statements, got %v", name, convertStrings(ddl))
		}
		want := "proto bundle types not compared for lack of descriptors: "
		if len(ddl.Notes) != 1 || !strings.HasPrefix(ddl.Notes[0], want) {
			t.Errorf("%s: notes = %q", name, ddl.Notes)
		}
	}
	if ddl, err := hammer.Diff(to, to); err != nil || len(ddl.Notes) != 0 {
		t.Errorf("complete descriptors: notes = %q, err = %v", ddl.Notes, err)
	}
}

func TestDiffProtoBundle_DescriptorsToSend(t *testing.T) {
	ctx := context.Background()
	withBundle, err := StringSource("CREATE PROTO BUNDLE (pkg.Msg);").DDL(ctx, &hammer.DDLOption{})
	if err != nil {
		t.Fatal(err)
	}
	empty := hammer.DDL{}
	fromBytes, toBytes := protoDescriptors(msgA), protoDescriptors(msgAB)
	withBundle.ProtoDescriptors = toBytes
	from := withBundle
	from.ProtoDescriptors = fromBytes
	noBytes := withBundle
	noBytes.ProtoDescriptors = nil
	for _, tt := range []struct {
		name     string
		from, to hammer.DDL
		want     []byte
	}{
		{"the target's", from, withBundle, toBytes},
		{"the source's when the target has no bundle", from, empty, fromBytes},
		{"none when the target has a bundle without descriptors", from, noBytes, nil},
	} {
		ddl, err := hammer.Diff(tt.from, tt.to)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ddl.ProtoDescriptors, tt.want) {
			t.Errorf("%s: wrong descriptors", tt.name)
		}
	}
}

func TestDiffProtoBundle_TypeMissingFromTargetDescriptors(t *testing.T) {
	ctx := context.Background()
	from, err := StringSource("CREATE PROTO BUNDLE (pkg.Msg);").DDL(ctx, &hammer.DDLOption{})
	if err != nil {
		t.Fatal(err)
	}
	to, err := StringSource("CREATE PROTO BUNDLE (pkg.Msg, pkg.Kind);").DDL(ctx, &hammer.DDLOption{})
	if err != nil {
		t.Fatal(err)
	}
	from.ProtoDescriptors, to.ProtoDescriptors = protoDescriptors(msgA), protoDescriptors(msgA)
	_, err = hammer.Diff(from, to)
	if err == nil || !strings.Contains(err.Error(), "proto bundle type pkg.Kind is not in the proto descriptors") {
		t.Errorf("err = %v", err)
	}
}
