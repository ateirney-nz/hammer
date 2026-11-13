package hammer

import (
	"slices"
	"strings"
	"testing"

	"github.com/cloudspannerecosystem/memefish"
	"github.com/cloudspannerecosystem/memefish/ast"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

type fieldType = descriptorpb.FieldDescriptorProto_Type

const (
	tString  = descriptorpb.FieldDescriptorProto_TYPE_STRING
	tMessage = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
	tEnum    = descriptorpb.FieldDescriptorProto_TYPE_ENUM
)

func field(name string, number int32, typ fieldType, typeName string) *descriptorpb.FieldDescriptorProto {
	f := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(number),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     typ.Enum(),
		JsonName: proto.String(name),
	}
	if typeName != "" {
		f.TypeName = proto.String(typeName)
	}
	return f
}

func message(name string, fields ...*descriptorpb.FieldDescriptorProto) *descriptorpb.DescriptorProto {
	return &descriptorpb.DescriptorProto{Name: proto.String(name), Field: fields}
}

func enum(name string, values ...string) *descriptorpb.EnumDescriptorProto {
	e := &descriptorpb.EnumDescriptorProto{Name: proto.String(name)}
	for i, v := range values {
		e.Value = append(e.Value, &descriptorpb.EnumValueDescriptorProto{Name: proto.String(v), Number: proto.Int32(int32(i))})
	}
	return e
}

// fixture builds:
//
//	web.Order       { id; line: Order.Line; tags: map<string, common.Money>; state: Order.State; parent: Order }
//	  Order.Line    { sku; next: Order.Line }
//	  Order.TagsEntry (map entry)
//	  Order.State   (nested enum)
//	web.Currency    (top-level enum)
//	common.Money    { currency: web.Currency }
func fixture() *descriptorpb.FileDescriptorSet {
	order := message("Order",
		field("id", 1, tString, ""),
		field("line", 2, tMessage, ".web.Order.Line"),
		field("tags", 3, tMessage, ".web.Order.TagsEntry"),
		field("state", 4, tEnum, ".web.Order.State"),
		field("parent", 6, tMessage, ".web.Order"),
	)
	tags := message("TagsEntry", field("key", 1, tString, ""), field("value", 2, tMessage, ".common.Money"))
	tags.Options = &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)}
	order.NestedType = []*descriptorpb.DescriptorProto{
		message("Line", field("sku", 1, tString, ""), field("next", 2, tMessage, ".web.Order.Line")),
		tags,
	}
	order.EnumType = []*descriptorpb.EnumDescriptorProto{enum("State", "STATE_UNSPECIFIED", "OPEN")}
	return &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{
		{
			Name: proto.String("common.proto"), Package: proto.String("common"),
			MessageType: []*descriptorpb.DescriptorProto{message("Money", field("currency", 1, tEnum, ".web.Currency"))},
		},
		{
			Name: proto.String("web.proto"), Package: proto.String("web"), Dependency: []string{"common.proto"},
			MessageType: []*descriptorpb.DescriptorProto{order},
			EnumType:    []*descriptorpb.EnumDescriptorProto{enum("Currency", "USD", "NZD")},
		},
	}}
}

func types(t *testing.T, set *descriptorpb.FileDescriptorSet) protoTypes {
	t.Helper()
	b, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	p, err := parseProtoTypes(b)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseProtoTypes(t *testing.T) {
	p := types(t, fixture())
	for _, name := range []string{"web.Order", "web.Order.Line", "web.Order.TagsEntry", "web.Order.State", "web.Currency", "common.Money"} {
		if !p[name].exists() {
			t.Errorf("%s is not indexed", name)
		}
	}
	if p["web.Order.State"].enum == nil || p["web.Order"].msg == nil {
		t.Error("kinds are wrong")
	}
	if p, err := parseProtoTypes(nil); p != nil || err != nil {
		t.Errorf("empty bytes: %v, %v", p, err)
	}
	if _, err := parseProtoTypes([]byte("not a descriptor set")); err == nil {
		t.Error("garbage parsed")
	}
}

func TestProtoTypeChanges(t *testing.T) {
	order := func(s *descriptorpb.FileDescriptorSet) *descriptorpb.DescriptorProto { return s.File[1].MessageType[0] }
	cases := []struct {
		name        string
		mutate      func(*descriptorpb.FileDescriptorSet)
		typ         string
		changes     []string // nil for equal
		destructive bool
	}{
		{"options and source info ignored", func(s *descriptorpb.FileDescriptorSet) {
			order(s).Options = &descriptorpb.MessageOptions{Deprecated: proto.Bool(true)}
			order(s).Field[0].Options = &descriptorpb.FieldOptions{Deprecated: proto.Bool(true)}
			s.File[1].SourceCodeInfo = &descriptorpb.SourceCodeInfo{}
			s.File[1].EnumType[0].Options = &descriptorpb.EnumOptions{Deprecated: proto.Bool(true)}
		}, "web.Order", nil, false},
		{"field renamed in a nested message", func(s *descriptorpb.FileDescriptorSet) {
			order(s).NestedType[0].Field[0].Name = proto.String("code")
		}, "web.Order.Line", []string{"field added: code = 1", "field removed: sku = 1"}, true},
		{"field added", func(s *descriptorpb.FileDescriptorSet) {
			order(s).Field = append(order(s).Field, field("note", 9, tString, ""))
		}, "web.Order", []string{"field added: note = 9"}, false},
		{"oneof added", func(s *descriptorpb.FileDescriptorSet) {
			order(s).OneofDecl = []*descriptorpb.OneofDescriptorProto{{Name: proto.String("kind")}}
			order(s).Field[0].OneofIndex = proto.Int32(0)
		}, "web.Order", []string{"field changed: id", "oneofs changed"}, false},
		// Spanner keeps neither proto3 optional (nor its synthetic oneof) nor file syntax.
		{"proto3 optional", func(s *descriptorpb.FileDescriptorSet) {
			order(s).OneofDecl = []*descriptorpb.OneofDescriptorProto{{Name: proto.String("_id")}}
			order(s).Field[0].OneofIndex = proto.Int32(0)
			order(s).Field[0].Proto3Optional = proto.Bool(true)
		}, "web.Order", nil, false},
		{"syntax", func(s *descriptorpb.FileDescriptorSet) {
			s.File[1].Syntax = proto.String("proto3")
		}, "web.Order", nil, false},
		{"real oneof beside a proto3 optional", func(s *descriptorpb.FileDescriptorSet) {
			order(s).OneofDecl = []*descriptorpb.OneofDescriptorProto{{Name: proto.String("kind")}, {Name: proto.String("_id")}}
			order(s).Field[0].OneofIndex = proto.Int32(1)
			order(s).Field[0].Proto3Optional = proto.Bool(true)
			order(s).Field[1].OneofIndex = proto.Int32(0)
		}, "web.Order", []string{"field changed: line", "oneofs changed"}, false},
		{"field renumbered", func(s *descriptorpb.FileDescriptorSet) {
			order(s).Field[0].Number = proto.Int32(9)
		}, "web.Order", []string{"field changed: id"}, true},
		{"field retyped", func(s *descriptorpb.FileDescriptorSet) {
			order(s).Field[0].Type = descriptorpb.FieldDescriptorProto_TYPE_BYTES.Enum()
		}, "web.Order", []string{"field changed: id"}, true},
		{"field relabelled", func(s *descriptorpb.FileDescriptorSet) {
			order(s).Field[0].Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
		}, "web.Order", []string{"field changed: id"}, true},
		{"default value", func(s *descriptorpb.FileDescriptorSet) {
			order(s).Field[0].DefaultValue = proto.String("x")
		}, "web.Order", []string{"field changed: id"}, false},
		{"enum value renumbered", func(s *descriptorpb.FileDescriptorSet) {
			s.File[1].EnumType[0].Value[1].Number = proto.Int32(5)
		}, "web.Currency", []string{"value renumbered: NZD 1 -> 5"}, true},
		{"enum value added", func(s *descriptorpb.FileDescriptorSet) {
			s.File[1].EnumType[0].Value = append(s.File[1].EnumType[0].Value, &descriptorpb.EnumValueDescriptorProto{Name: proto.String("AUD"), Number: proto.Int32(2)})
		}, "web.Currency", []string{"value added: AUD = 2"}, false},
		{"nested enum value renumbered and removed", func(s *descriptorpb.FileDescriptorSet) {
			order(s).EnumType[0].Value = order(s).EnumType[0].Value[1:]
			order(s).EnumType[0].Value[0].Number = proto.Int32(7)
		}, "web.Order.State", []string{"value renumbered: OPEN 1 -> 7", "value removed: STATE_UNSPECIFIED = 0"}, true},
		{"message becomes enum", func(s *descriptorpb.FileDescriptorSet) {
			s.File[1].EnumType[0].Name = proto.String("Old")
			s.File[1].MessageType = append(s.File[1].MessageType, message("Currency"))
		}, "web.Currency", []string{"changed between message and enum"}, true},
	}
	old := types(t, fixture())
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			set := fixture()
			c.mutate(set)
			next := types(t, set)
			if equal := next[c.typ].equal(old[c.typ]); equal != (c.changes == nil) {
				t.Fatalf("equal = %v", equal)
			}
			if c.changes == nil {
				return
			}
			got, destructive := next[c.typ].changes(old[c.typ])
			if !slices.Equal(got, c.changes) || destructive != c.destructive {
				t.Errorf("got %q destructive=%v, want %q destructive=%v", got, destructive, c.changes, c.destructive)
			}
		})
	}
}

func TestProtoTypesZeroValue(t *testing.T) {
	set := fixture()
	set.File[1].MessageType = append(set.File[1].MessageType, &descriptorpb.DescriptorProto{
		Name: proto.String("Req"),
		Field: []*descriptorpb.FieldDescriptorProto{{
			Name: proto.String("a"), Number: proto.Int32(1), Type: tString.Enum(),
			Label: descriptorpb.FieldDescriptorProto_LABEL_REQUIRED.Enum(),
		}},
	})
	set.File[1].EnumType = append(set.File[1].EnumType, &descriptorpb.EnumDescriptorProto{
		Name: proto.String("Proto2Enum"), Value: []*descriptorpb.EnumValueDescriptorProto{{Name: proto.String("ONE"), Number: proto.Int32(1)}},
	})
	p := types(t, set)
	cases := []struct {
		types   protoTypes
		typ     string
		want    string
		wantErr string
	}{
		{p, "common.Money", `CAST("" AS common.Money)`, ""},
		{p, "web.Currency", "CAST(0 AS web.Currency)", ""},
		{p, "web.Proto2Enum", "CAST(1 AS web.Proto2Enum)", ""},
		{p, "web.Req", "", "needs an explicit DEFAULT"},
		{p, "web.Missing", "", "web.Missing is not in the proto descriptors"},
		{nil, "common.Money", "", "type common.Money needs an explicit DEFAULT"},
	}
	for _, c := range cases {
		stmt, err := memefish.ParseDDL("", "CREATE TABLE t (c "+c.typ+") PRIMARY KEY (c)")
		if err != nil {
			t.Fatal(err)
		}
		nt := stmt.(*ast.CreateTable).Columns[0].Type.(*ast.NamedType)
		got, err := c.types.zeroValue(nt)
		switch {
		case c.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: want error containing %q, got %v", c.typ, c.wantErr, err)
			}
		case err != nil:
			t.Errorf("%s: %v", c.typ, err)
		case got.SQL() != c.want:
			t.Errorf("%s: got %s, want %s", c.typ, got.SQL(), c.want)
		}
	}
}
