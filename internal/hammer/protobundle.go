package hammer

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/cloudspannerecosystem/memefish/ast"
	"google.golang.org/protobuf/types/descriptorpb"
)

// unquoteProtoTypeNames splits a type written as one backtick-quoted dotted identifier into a
// path. Spanner reads proto and enum column types back quoted (`pkg.Msg`), where a schema file
// writes pkg.Msg.
func unquoteProtoTypeNames(stmt ast.Node) {
	for n := range ast.Preorder(stmt) {
		nt, ok := n.(*ast.NamedType)
		if !ok || len(nt.Path) != 1 || !strings.Contains(nt.Path[0].Name, ".") {
			continue
		}
		parts := strings.Split(nt.Path[0].Name, ".")
		nt.Path = make([]*ast.Ident, len(parts))
		for i, part := range parts {
			nt.Path[i] = &ast.Ident{Name: part}
		}
	}
}

// generateDDLForProtoBundle returns the bundle statements that must run before the objects that
// use the types (CREATE, INSERT, non-destructive UPDATE) and those that must run after the
// objects that stopped using them (DROP, DELETE, destructive UPDATE).
func (g *Generator) generateDDLForProtoBundle() (early, late DDL) {
	from, to := g.from.protoBundle, g.to.protoBundle
	switch {
	case from == nil && to == nil:
	case from == nil:
		early.Append(to)
	case to == nil:
		late.Append(&ast.DropProtoBundle{})
	default:
		var insert, update, remove []*ast.NamedType
		var uncompared []string
		fromNames := protoBundleNames(from)
		toNames := protoBundleNames(to)
		for _, nt := range to.Types.Types {
			name := identsToComparable(nt.Path...)
			if !fromNames[name] {
				insert = append(insert, nt)
				continue
			}
			oldType, newType := g.from.protoTypes[name], g.to.protoTypes[name]
			switch {
			case !oldType.exists() || !newType.exists():
				uncompared = append(uncompared, name)
			case newType.equal(oldType):
			default:
				changes, destructive := newType.changes(oldType)
				if !destructive {
					update = append(update, nt)
					break
				}
				late.Append(DataLoss{
					Statement: &ast.AlterProtoBundle{Update: &ast.AlterProtoBundleUpdate{Types: protoBundleTypes(nt)}},
					Reason:    name + ": " + strings.Join(changes, "; "),
				})
			}
		}
		for _, nt := range from.Types.Types {
			if !toNames[identsToComparable(nt.Path...)] {
				remove = append(remove, nt)
			}
		}
		if alter := (&ast.AlterProtoBundle{Insert: alterInsert(insert), Update: alterUpdate(update)}); alter.Insert != nil || alter.Update != nil {
			early.Append(alter)
		}
		if len(remove) > 0 {
			late.Append(&ast.AlterProtoBundle{Delete: &ast.AlterProtoBundleDelete{Types: protoBundleTypes(remove...)}})
		}
		if len(uncompared) > 0 {
			g.notes = append(g.notes, "proto bundle types not compared for lack of descriptors: "+strings.Join(uncompared, ", "))
		}
	}
	return early, late
}

func protoBundleNames(b *ast.CreateProtoBundle) map[string]bool {
	names := map[string]bool{}
	for _, nt := range b.Types.Types {
		names[identsToComparable(nt.Path...)] = true
	}
	return names
}

func protoBundleTypes(types ...*ast.NamedType) *ast.ProtoBundleTypes {
	return &ast.ProtoBundleTypes{Types: types}
}

func alterInsert(types []*ast.NamedType) *ast.AlterProtoBundleInsert {
	if len(types) == 0 {
		return nil
	}
	return &ast.AlterProtoBundleInsert{Types: protoBundleTypes(types...)}
}

func alterUpdate(types []*ast.NamedType) *ast.AlterProtoBundleUpdate {
	if len(types) == 0 {
		return nil
	}
	return &ast.AlterProtoBundleUpdate{Types: protoBundleTypes(types...)}
}

// DeriveProtoBundle replaces the proto bundle of ddl with the one its schema needs, and reports
// whether it replaced a CREATE PROTO BUNDLE. Spanner requires a bundle to hold every type the
// schema uses, every type a bundled message's fields use and every message enclosing a bundled
// nested type; map entries are never bundle types, but their key and value types are followed.
func DeriveProtoBundle(ddl DDL) (_ DDL, ignored bool, _ error) {
	types, err := parseProtoTypes(ddl.ProtoDescriptors)
	if err != nil {
		return DDL{}, false, err
	}
	if types == nil {
		return DDL{}, false, fmt.Errorf("deriving the proto bundle needs proto descriptors")
	}
	var list []Statement
	roots := map[string]bool{}
	for _, stmt := range ddl.List {
		if _, ok := stmt.(*ast.CreateProtoBundle); ok {
			ignored = true
			continue
		}
		list = append(list, stmt)
		for n := range ast.Preorder(stmt.(ast.Node)) {
			if nt, ok := n.(*ast.NamedType); ok && !isUUID(nt) {
				roots[identsToComparable(nt.Path...)] = true
			}
		}
	}

	var errs []error
	seen := map[string]bool{}
	var visit func(name, from string)
	visit = func(name, from string) {
		if seen[name] {
			return
		}
		seen[name] = true
		t, ok := types[name]
		switch {
		case !ok && from == "":
			errs = append(errs, fmt.Errorf("type %s is not in the proto descriptors", name))
			return
		case !ok:
			errs = append(errs, fmt.Errorf("type %s (used by %s) is not in the proto descriptors; build them with their imports", name, from))
			return
		}
		if t.parent != "" {
			visit(t.parent, name)
		}
		for _, f := range t.msg.GetField() {
			switch f.GetType() {
			case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, descriptorpb.FieldDescriptorProto_TYPE_GROUP, descriptorpb.FieldDescriptorProto_TYPE_ENUM:
				visit(strings.TrimPrefix(f.GetTypeName(), "."), name)
			}
		}
	}
	// Checked before the closure, which would otherwise mark a map entry seen through its message.
	for _, name := range slices.Sorted(maps.Keys(roots)) {
		if types[name].mapEntry {
			errs = append(errs, fmt.Errorf("type %s is a map entry, not a bundle type", name))
			delete(roots, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(roots)) {
		visit(name, "")
	}
	if err := errors.Join(errs...); err != nil {
		return DDL{}, false, err
	}

	var bundle []*ast.NamedType
	for _, name := range slices.Sorted(maps.Keys(seen)) {
		if types[name].mapEntry {
			continue
		}
		path := strings.Split(name, ".")
		nt := &ast.NamedType{Path: make([]*ast.Ident, len(path))}
		for i, part := range path {
			nt.Path[i] = &ast.Ident{Name: part}
		}
		bundle = append(bundle, nt)
	}
	ddl.List = list
	if len(bundle) > 0 {
		ddl.List = append([]Statement{&ast.CreateProtoBundle{Types: protoBundleTypes(bundle...)}}, list...)
	}
	return ddl, ignored, nil
}
