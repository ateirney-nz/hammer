package hammer

import (
	"strings"

	"github.com/cloudspannerecosystem/memefish/ast"
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
