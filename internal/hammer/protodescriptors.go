package hammer

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/cloudspannerecosystem/memefish/ast"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// protoTypes indexes the messages and enums of a descriptor set by fully qualified name.
type protoTypes map[string]protoType

type protoType struct {
	msg  *descriptorpb.DescriptorProto     // nil for an enum
	enum *descriptorpb.EnumDescriptorProto // nil for a message

	parent   string // enclosing message, "" at top level
	mapEntry bool
}

// parseProtoTypes returns nil for empty bytes; the first definition of a name wins.
func parseProtoTypes(b []byte) (protoTypes, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(b, &set); err != nil {
		return nil, fmt.Errorf("invalid proto descriptors: %w", err)
	}
	types := protoTypes{}
	add := func(name string, t protoType) {
		if _, ok := types[name]; !ok {
			types[name] = t
		}
	}
	for _, f := range set.GetFile() {
		var addMessage func(prefix, parent string, m *descriptorpb.DescriptorProto)
		addMessage = func(prefix, parent string, m *descriptorpb.DescriptorProto) {
			name := joinProtoName(prefix, m.GetName())
			add(name, protoType{msg: m, parent: parent, mapEntry: m.GetOptions().GetMapEntry()})
			for _, n := range m.GetNestedType() {
				addMessage(name, name, n)
			}
			for _, e := range m.GetEnumType() {
				add(joinProtoName(name, e.GetName()), protoType{enum: e, parent: name})
			}
		}
		for _, m := range f.GetMessageType() {
			addMessage(f.GetPackage(), "", m)
		}
		for _, e := range f.GetEnumType() {
			add(joinProtoName(f.GetPackage(), e.GetName()), protoType{enum: e})
		}
	}
	return types, nil
}

func joinProtoName(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func (t protoType) exists() bool {
	return t.msg != nil || t.enum != nil
}

// zeroValue returns the NOT NULL backfill for a column of the named proto or enum type.
// Without descriptors the type cannot be classified.
func (p protoTypes) zeroValue(t *ast.NamedType) (ast.Expr, error) {
	if p == nil {
		return nil, fmt.Errorf("type %s needs an explicit DEFAULT", t.SQL())
	}
	pt := p[identsToComparable(t.Path...)]
	switch {
	case pt.msg != nil:
		if slices.ContainsFunc(pt.msg.GetField(), func(f *descriptorpb.FieldDescriptorProto) bool {
			return f.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REQUIRED
		}) {
			return nil, fmt.Errorf("type %s needs an explicit DEFAULT: an empty message lacks its required fields", t.SQL())
		}
		return &ast.CastExpr{Expr: &ast.StringLiteral{Value: ""}, Type: t}, nil
	case pt.enum != nil && len(pt.enum.GetValue()) > 0:
		n := strconv.Itoa(int(pt.enum.GetValue()[0].GetNumber()))
		return &ast.CastExpr{Expr: &ast.IntLiteral{Base: 10, Value: n}, Type: t}, nil
	}
	return nil, fmt.Errorf("type %s is not in the proto descriptors", t.SQL())
}

// equal compares what Spanner keeps of a type: the descriptors Spanner returns drop options,
// source info, file syntax and edition, and proto3 `optional` (with its synthetic oneof), so
// those are left out too.
func (t protoType) equal(u protoType) bool {
	switch {
	case t.msg != nil && u.msg != nil:
		return proto.Equal(projectMessage(t.msg), projectMessage(u.msg))
	case t.enum != nil && u.enum != nil:
		return proto.Equal(projectEnum(t.enum), projectEnum(u.enum))
	}
	return false
}

func projectMessage(m *descriptorpb.DescriptorProto) *descriptorpb.DescriptorProto {
	out := &descriptorpb.DescriptorProto{Name: m.Name}
	for _, f := range m.GetField() {
		out.Field = append(out.Field, projectField(f))
	}
	for i, o := range m.GetOneofDecl() {
		if !syntheticOneof(m, int32(i)) {
			out.OneofDecl = append(out.OneofDecl, &descriptorpb.OneofDescriptorProto{Name: o.Name})
		}
	}
	return out
}

func projectField(f *descriptorpb.FieldDescriptorProto) *descriptorpb.FieldDescriptorProto {
	out := &descriptorpb.FieldDescriptorProto{
		Name:         f.Name,
		Number:       f.Number,
		Label:        f.Label,
		Type:         f.Type,
		TypeName:     f.TypeName,
		JsonName:     f.JsonName,
		OneofIndex:   f.OneofIndex,
		DefaultValue: f.DefaultValue,
	}
	if f.GetProto3Optional() {
		out.OneofIndex = nil
	}
	return out
}

// syntheticOneof reports whether oneof i only wraps a proto3 `optional` field.
// Synthetic oneofs follow all real ones, so dropping them leaves real indexes unchanged.
func syntheticOneof(m *descriptorpb.DescriptorProto, i int32) bool {
	for _, f := range m.GetField() {
		if f.OneofIndex != nil && f.GetOneofIndex() == i {
			return f.GetProto3Optional()
		}
	}
	return false
}

func projectEnum(e *descriptorpb.EnumDescriptorProto) *descriptorpb.EnumDescriptorProto {
	out := &descriptorpb.EnumDescriptorProto{Name: e.Name}
	for _, v := range e.GetValue() {
		out.Value = append(out.Value, &descriptorpb.EnumValueDescriptorProto{Name: v.Name, Number: v.Number})
	}
	return out
}

// changes summarises how the new type differs from the old one, and whether that is
// destructive: removes, renumbers or retypes a field or enum value, or switches between
// message and enum.
func (t protoType) changes(old protoType) (out []string, destructive bool) {
	switch {
	case (t.msg == nil) != (old.msg == nil):
		return []string{"changed between message and enum"}, true
	case t.enum != nil:
		out, destructive = enumChanges(t.enum, old.enum)
	default:
		out, destructive = messageChanges(t.msg, old.msg)
	}
	if len(out) == 0 {
		out = []string{"descriptor changed"}
	}
	return out, destructive
}

func messageChanges(local, live *descriptorpb.DescriptorProto) (out []string, removes bool) {
	liveFields := map[string]*descriptorpb.FieldDescriptorProto{}
	for _, f := range live.GetField() {
		liveFields[f.GetName()] = f
	}
	for _, f := range local.GetField() {
		lf, ok := liveFields[f.GetName()]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("field added: %s = %d", f.GetName(), f.GetNumber()))
		case !proto.Equal(projectField(f), projectField(lf)):
			out = append(out, "field changed: "+f.GetName())
			if f.GetNumber() != lf.GetNumber() || f.GetType() != lf.GetType() || f.GetTypeName() != lf.GetTypeName() || f.GetLabel() != lf.GetLabel() {
				removes = true
			}
		}
		delete(liveFields, f.GetName())
	}
	for _, f := range live.GetField() {
		if _, ok := liveFields[f.GetName()]; ok {
			out = append(out, fmt.Sprintf("field removed: %s = %d", f.GetName(), f.GetNumber()))
			removes = true
		}
	}
	oneofs := func(m *descriptorpb.DescriptorProto) []string {
		var names []string
		for _, o := range projectMessage(m).GetOneofDecl() {
			names = append(names, o.GetName())
		}
		return names
	}
	if !slices.Equal(oneofs(local), oneofs(live)) {
		out = append(out, "oneofs changed")
	}
	return out, removes
}

func enumChanges(local, live *descriptorpb.EnumDescriptorProto) (out []string, removes bool) {
	liveValues := map[string]int32{}
	for _, v := range live.GetValue() {
		liveValues[v.GetName()] = v.GetNumber()
	}
	for _, v := range local.GetValue() {
		n, ok := liveValues[v.GetName()]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("value added: %s = %d", v.GetName(), v.GetNumber()))
		case n != v.GetNumber():
			out = append(out, fmt.Sprintf("value renumbered: %s %d -> %d", v.GetName(), n, v.GetNumber()))
			removes = true
		}
		delete(liveValues, v.GetName())
	}
	for _, v := range live.GetValue() {
		if _, ok := liveValues[v.GetName()]; ok {
			out = append(out, fmt.Sprintf("value removed: %s = %d", v.GetName(), v.GetNumber()))
			removes = true
		}
	}
	return out, removes
}
