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

func field(name string, number int32, typ descriptorpb.FieldDescriptorProto_Type, typeName string) *descriptorpb.FieldDescriptorProto {
	f := &descriptorpb.FieldDescriptorProto{
		Name: proto.String(name), Number: proto.Int32(number), Type: typ.Enum(),
		Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
	}
	if typeName != "" {
		f.TypeName = proto.String(typeName)
	}
	return f
}

// deriveDescriptors serializes a proto3 file in which pkg.Order uses the enum pkg.Status, the
// nested pkg.Order.Line (which uses the enum pkg.Unit), the nested enum pkg.Order.State and a map of pkg.Tag; pkg.Unused is
// reachable from nothing.
func deriveDescriptors() []byte {
	const (
		msg   = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
		enum  = descriptorpb.FieldDescriptorProto_TYPE_ENUM
		int64 = descriptorpb.FieldDescriptorProto_TYPE_INT64
	)
	tags := field("tags", 4, msg, ".pkg.Order.TagsEntry")
	tags.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	order := &descriptorpb.DescriptorProto{
		Name: proto.String("Order"),
		Field: []*descriptorpb.FieldDescriptorProto{
			field("id", 1, int64, ""), field("status", 2, enum, ".pkg.Status"), field("line", 3, msg, ".pkg.Order.Line"), tags,
		},
		NestedType: []*descriptorpb.DescriptorProto{
			{Name: proto.String("Line"), Field: []*descriptorpb.FieldDescriptorProto{field("unit", 1, enum, ".pkg.Unit")}},
			{
				Name:    proto.String("TagsEntry"),
				Field:   []*descriptorpb.FieldDescriptorProto{field("key", 1, int64, ""), field("value", 2, msg, ".pkg.Tag")},
				Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
			},
		},
		EnumType: []*descriptorpb.EnumDescriptorProto{protoEnum("State", "STATE_UNSPECIFIED:0")},
	}
	file := &descriptorpb.FileDescriptorProto{
		Name: proto.String("pkg/order.proto"), Package: proto.String("pkg"), Syntax: proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			order,
			{Name: proto.String("Tag"), Field: []*descriptorpb.FieldDescriptorProto{field("label", 1, int64, "")}},
			{Name: proto.String("Unused"), Field: []*descriptorpb.FieldDescriptorProto{field("x", 1, int64, "")}},
		},
		EnumType: []*descriptorpb.EnumDescriptorProto{protoEnum("Status", "STATUS_UNSPECIFIED:0"), protoEnum("Unit", "UNIT_UNSPECIFIED:0")},
	}
	b, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{file}})
	if err != nil {
		panic(err)
	}
	return b
}

// crossFileDescriptors serializes pkg.Event, which has a google.protobuf.Timestamp field defined in another file.
func crossFileDescriptors() []byte {
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{
		{
			Name: proto.String("google/protobuf/timestamp.proto"), Package: proto.String("google.protobuf"), Syntax: proto.String("proto3"),
			MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Timestamp"), Field: []*descriptorpb.FieldDescriptorProto{field("seconds", 1, descriptorpb.FieldDescriptorProto_TYPE_INT64, "")}}},
		},
		{
			Name: proto.String("pkg/event.proto"), Package: proto.String("pkg"), Syntax: proto.String("proto3"), Dependency: []string{"google/protobuf/timestamp.proto"},
			MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Event"), Field: []*descriptorpb.FieldDescriptorProto{field("at", 1, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".google.protobuf.Timestamp")}}},
		},
	}}
	b, err := proto.Marshal(set)
	if err != nil {
		panic(err)
	}
	return b
}

func TestDeriveProtoBundle(t *testing.T) {
	const orderClosure = "pkg.`Order`, pkg.`Order`.Line, pkg.Status, pkg.Tag, pkg.Unit"
	tests := []struct {
		name        string
		schema      string
		descriptors []byte
		want        string // the derived bundle's types; empty for no bundle
		wantIgnored bool
		wantErr     string
	}{
		{name: "a column pulls in what its fields use, but not map entries or unreferenced types",
			schema: "CREATE TABLE t (id INT64 NOT NULL, o pkg.Order) PRIMARY KEY (id);", descriptors: deriveDescriptors(), want: orderClosure},
		{name: "an array column",
			schema: "CREATE TABLE t (id INT64 NOT NULL, o ARRAY<pkg.Tag>) PRIMARY KEY (id);", descriptors: deriveDescriptors(), want: "pkg.Tag"},
		{name: "a nested type pulls in its enclosing message and that message's types",
			schema: "CREATE TABLE t (id INT64 NOT NULL, l pkg.Order.Line) PRIMARY KEY (id);", descriptors: deriveDescriptors(), want: orderClosure},
		{name: "a nested enum pulls in its enclosing message and that message's types",
			schema: "CREATE TABLE t (id INT64 NOT NULL, s pkg.Order.State) PRIMARY KEY (id);", descriptors: deriveDescriptors(), want: "pkg.`Order`, pkg.`Order`.Line, pkg.`Order`.State, pkg.Status, pkg.Tag, pkg.Unit"},
		{name: "a message that uses itself",
			schema: "CREATE TABLE t (id INT64 NOT NULL, n pkg.Node) PRIMARY KEY (id);",
			descriptors: protoDescriptors(&descriptorpb.DescriptorProto{Name: proto.String("Node"), Field: []*descriptorpb.FieldDescriptorProto{
				field("next", 1, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".pkg.Node")}}),
			want: "pkg.Node"},
		{name: "a group field is followed",
			schema: "CREATE TABLE t (id INT64 NOT NULL, h pkg.Holder) PRIMARY KEY (id);",
			descriptors: protoDescriptors(&descriptorpb.DescriptorProto{Name: proto.String("Holder"),
				Field:      []*descriptorpb.FieldDescriptorProto{field("item", 1, descriptorpb.FieldDescriptorProto_TYPE_GROUP, ".pkg.Holder.Item")},
				NestedType: []*descriptorpb.DescriptorProto{protoMsg("Item", "x:1")}}),
			want: "pkg.Holder, pkg.Holder.Item"},
		{name: "a well-known type from another file",
			schema: "CREATE TABLE t (id INT64 NOT NULL, e pkg.Event) PRIMARY KEY (id);", descriptors: crossFileDescriptors(), want: "google.protobuf.Timestamp, pkg.Event"},
		{name: "UUID is not a proto type",
			schema: "CREATE TABLE t (id INT64 NOT NULL, u UUID, o pkg.Tag) PRIMARY KEY (id);\n" +
				"CREATE VIEW v SQL SECURITY INVOKER AS SELECT CAST('' AS UUID) AS u;",
			descriptors: deriveDescriptors(), want: "pkg.Tag"},
		{name: "UUID alone gives no bundle",
			schema: "CREATE TABLE t (id UUID NOT NULL) PRIMARY KEY (id);", descriptors: deriveDescriptors()},
		{name: "casts in a view, default and check",
			schema: "CREATE TABLE t (id INT64 NOT NULL, s INT64 DEFAULT (CAST(0 AS pkg.Status)), CONSTRAINT c CHECK (CAST(s AS pkg.Unit) IS NOT NULL)) PRIMARY KEY (id);\n" +
				"CREATE VIEW v SQL SECURITY INVOKER AS SELECT CAST('' AS pkg.Tag) AS tg;",
			descriptors: deriveDescriptors(), want: "pkg.Status, pkg.Tag, pkg.Unit"},
		{name: "an explicit bundle is replaced",
			schema:      "CREATE PROTO BUNDLE (pkg.Unused, pkg.Tag);\nCREATE TABLE t (id INT64 NOT NULL, o pkg.Tag) PRIMARY KEY (id);",
			descriptors: deriveDescriptors(), want: "pkg.Tag", wantIgnored: true},
		{name: "no proto types, no bundle",
			schema: "CREATE TABLE t (id INT64 NOT NULL) PRIMARY KEY (id);", descriptors: deriveDescriptors()},
		{name: "an explicit bundle goes when nothing uses a type",
			schema:      "CREATE PROTO BUNDLE (pkg.Unused);\nCREATE TABLE t (id INT64 NOT NULL) PRIMARY KEY (id);",
			descriptors: deriveDescriptors(), wantIgnored: true},
		{name: "no descriptors",
			schema: "CREATE TABLE t (id INT64 NOT NULL, o pkg.Tag) PRIMARY KEY (id);", wantErr: "needs proto descriptors"},
		{name: "a type missing from the descriptors",
			schema: "CREATE TABLE t (id INT64 NOT NULL, o pkg.Gone) PRIMARY KEY (id);", descriptors: deriveDescriptors(), wantErr: "type pkg.Gone is not in the proto descriptors"},
		{name: "a map entry",
			schema: "CREATE TABLE t (id INT64 NOT NULL, o pkg.Order.TagsEntry) PRIMARY KEY (id);", descriptors: deriveDescriptors(), wantErr: "is a map entry"},
		{name: "a map entry beside its enclosing message",
			schema: "CREATE TABLE t (id INT64 NOT NULL, o pkg.Order, m pkg.Order.TagsEntry) PRIMARY KEY (id);", descriptors: deriveDescriptors(), wantErr: "is a map entry"},
		{name: "a field type missing from the descriptors",
			schema: "CREATE TABLE t (id INT64 NOT NULL, o pkg.Order) PRIMARY KEY (id);", descriptors: protoDescriptors(&descriptorpb.DescriptorProto{
				Name: proto.String("Order"), Field: []*descriptorpb.FieldDescriptorProto{field("e", 1, descriptorpb.FieldDescriptorProto_TYPE_ENUM, ".pkg.Gone")}}),
			wantErr: "type pkg.Gone (used by pkg.Order) is not in the proto descriptors"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ddl, err := StringSource(tt.schema).DDL(context.Background(), &hammer.DDLOption{})
			if err != nil {
				t.Fatal(err)
			}
			ddl.ProtoDescriptors = tt.descriptors
			got, ignored, err := hammer.DeriveProtoBundle(ddl)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ignored != tt.wantIgnored {
				t.Errorf("ignored = %v", ignored)
			}
			var bundles []string
			for _, sql := range convertStrings(got) {
				if strings.HasPrefix(sql, "CREATE PROTO BUNDLE") {
					bundles = append(bundles, sql)
				}
			}
			var want []string
			if tt.want != "" {
				want = []string{"CREATE PROTO BUNDLE (" + tt.want + ")"}
			}
			if !slices.Equal(bundles, want) {
				t.Errorf("bundles = %q, want %q", bundles, want)
			}
		})
	}
}

func TestDiffDeriveProtoBundle(t *testing.T) {
	const (
		fromSchema = "CREATE PROTO BUNDLE (pkg.Unused);\nCREATE TABLE t (id INT64 NOT NULL, o pkg.Order) PRIMARY KEY (id);\n"
		toSchema   = "CREATE TABLE t (id INT64 NOT NULL, g ARRAY<pkg.Tag>) PRIMARY KEY (id);\n"
	)
	ctx := context.Background()
	descriptors := deriveDescriptors()
	derive := func(schema string) hammer.DDL {
		ddl, err := StringSource(schema).DDL(ctx, &hammer.DDLOption{})
		if err != nil {
			t.Fatal(err)
		}
		ddl.ProtoDescriptors = descriptors
		ddl, _, err = hammer.DeriveProtoBundle(ddl)
		if err != nil {
			t.Fatal(err)
		}
		return ddl
	}
	from, to := derive(fromSchema), derive(toSchema)

	ddl, err := hammer.Diff(from, to)
	if err != nil {
		t.Fatal(err)
	}
	// Types no longer used are deleted from the bundle after the column that used them goes.
	want := []string{
		"ALTER TABLE t ADD COLUMN g ARRAY<pkg.Tag>",
		"ALTER TABLE t DROP COLUMN o",
		"ALTER PROTO BUNDLE DELETE (pkg.`Order`, pkg.`Order`.Line, pkg.Status, pkg.Unit)",
	}
	if got := convertStrings(ddl); !slices.Equal(got, want) {
		t.Errorf("%s", formatDDLMismatch(want, got))
	}
	if losses := ddl.DataLosses(); len(losses) != 1 || !strings.HasPrefix(losses[0].SQL(), "ALTER TABLE t DROP COLUMN") {
		t.Errorf("data losses = %v", losses)
	}

	inst, admin := roundTripTarget(t)
	if inst == "" {
		return
	}
	// The emulator is the judge: it accepts the derived bundle, the delete, and reads back what was derived.
	client := createDatabase(t, inst, admin, strings.Join(convertStrings(from), ";\n"), descriptors, &hammer.DDLOption{})
	if err := retryQuota(func() error { return client.ApplyDatabaseDDL(ctx, ddl) }); err != nil {
		t.Fatalf("Spanner rejected the diff:\n%s\nerror: %v", strings.Join(convertStrings(ddl), ";\n"), err)
	}
	var (
		schema string
		live   []byte
	)
	if err := retryQuota(func() (err error) { schema, live, err = client.GetDatabaseDDL(ctx); return }); err != nil {
		t.Fatal(err)
	}
	readBack := parse(t, schema, &hammer.DDLOption{})
	readBack.ProtoDescriptors = live
	rest, err := hammer.Diff(readBack, to)
	if err != nil || len(rest.List) != 0 {
		t.Errorf("read back differs from the derived target: %v%s", err, formatDDLMismatch(nil, convertStrings(rest)))
	}
}
