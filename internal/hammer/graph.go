package hammer

import (
	"slices"
	"strings"

	"github.com/cloudspannerecosystem/memefish/ast"
)

func (g *Generator) findGraphByName(graphs []*Graph, name *ast.Ident) (*Graph, bool) {
	return findFirst(graphs, func(x *Graph) bool { return strings.EqualFold(x.Name.Name, name.Name) })
}

// graphReads lists what the graph reads in db: its tables and views, the columns its elements name or
// expand (as "table.column"), and the functions its derived properties call. A column named only by KEY
// that defaults to the primary key is left out: a primary key change recreates the table.
func graphReads(db *Database, graph *Graph) map[string]bool {
	reads := map[string]bool{}
	for _, list := range graphElements(graph) {
		for _, e := range list {
			table := strings.ToLower(e.Name.Name)
			reads[table] = true
			mention := func(n ast.Node) {
				for c := range ast.Preorder(n) {
					switch c := c.(type) {
					case *ast.Ident:
						reads[table+"."+strings.ToLower(c.Name)] = true
					case *ast.CallExpr:
						reads[functionKey(c.Func)] = true
					}
				}
			}
			if e.Keys != nil {
				mention(e.Keys)
			}
			if e.DynamicLabel != nil {
				mention(e.DynamicLabel)
			}
			if e.DynamicProperties != nil {
				mention(e.DynamicProperties)
			}
			for _, props := range propertyLists(e) {
				switch p := props.(type) {
				case *ast.PropertyGraphDerivedPropertyList:
					mention(p)
				case *ast.PropertyGraphNoProperties:
				default:
					for _, c := range columnsOf(db, e.Name) {
						reads[table+"."+c] = true
					}
				}
			}
		}
	}
	return reads
}

// propertyLists returns the property list of each label of the element; an omitted one is nil.
func propertyLists(e *ast.PropertyGraphElement) []ast.PropertyGraphElementProperties {
	switch p := e.Properties.(type) {
	case *ast.PropertyGraphSingleProperties:
		return []ast.PropertyGraphElementProperties{p.Properties}
	case *ast.PropertyGraphLabelAndPropertiesList:
		var lists []ast.PropertyGraphElementProperties
		for _, lp := range p.LabelAndProperties {
			lists = append(lists, lp.Properties)
		}
		return lists
	}
	return []ast.PropertyGraphElementProperties{nil}
}

func graphElements(graph *Graph) [2][]*ast.PropertyGraphElement {
	var nodes, edges []*ast.PropertyGraphElement
	nodes = graph.Content.NodeTables.Tables.Elements
	if graph.Content.EdgeTables != nil {
		edges = graph.Content.EdgeTables.Tables.Elements
	}
	return [2][]*ast.PropertyGraphElement{nodes, edges}
}

// graphCanonical renders the graph with every default Spanner fills in on readback made explicit (KEY,
// LABEL, PROPERTIES, REFERENCES columns), using the table definitions in db, so the shorthand a user
// writes and the expanded form Spanner reports compare equal.
func graphCanonical(db *Database, graph *Graph) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(graph.Name.Name))
	if graph.Options != nil {
		b.WriteString(" " + graph.Options.SQL())
	}
	elements := graphElements(graph)
	for kind, list := range elements {
		for _, e := range slices.SortedFunc(slices.Values(list), func(a, b *ast.PropertyGraphElement) int {
			return strings.Compare(elementName(a), elementName(b))
		}) {
			b.WriteString("\n" + []string{"node ", "edge "}[kind])
			table := tableOf(db, e.Name)
			name := elementName(e)
			b.WriteString(name + " " + strings.ToLower(e.Name.Name))
			switch keys := e.Keys.(type) {
			case *ast.PropertyGraphEdgeElementKeys:
				b.WriteString(" key" + keyColumns(table, keys.Element))
				b.WriteString(" source" + keyColumns(nil, &ast.PropertyGraphElementKey{Keys: keys.Source.Keys}))
				b.WriteString(" -> " + referenceTarget(db, elements[0], keys.Source.ElementReference, keys.Source.ReferenceColumns))
				b.WriteString(" destination" + keyColumns(nil, &ast.PropertyGraphElementKey{Keys: keys.Destination.Keys}))
				b.WriteString(" -> " + referenceTarget(db, elements[0], keys.Destination.ElementReference, keys.Destination.ReferenceColumns))
			case *ast.PropertyGraphNodeElementKey:
				b.WriteString(" key" + keyColumns(table, keys.Key))
			default:
				b.WriteString(" key" + keyColumns(table, nil))
			}
			for _, label := range elementLabels(columnsOf(db, e.Name), e) {
				b.WriteString(" label " + label)
			}
			if e.DynamicLabel != nil {
				b.WriteString(" " + e.DynamicLabel.SQL())
			}
			if e.DynamicProperties != nil {
				b.WriteString(" " + e.DynamicProperties.SQL())
			}
		}
	}
	return b.String()
}

func tableOf(db *Database, name *ast.Ident) *Table {
	t, _ := findFirst(db.tables, func(t *Table) bool { return strings.EqualFold(identsToComparable(t.Name.Idents...), name.Name) })
	return t
}

// columnsOf lists the lowercased columns of a table or view, or nil when a view's columns cannot be read
// from its select list.
func columnsOf(db *Database, name *ast.Ident) []string {
	if t := tableOf(db, name); t != nil {
		var cols []string
		for _, c := range t.Columns {
			cols = append(cols, strings.ToLower(c.Name.Name))
		}
		return cols
	}
	if v, ok := findFirst(db.views, func(v *View) bool { return lowerName(v.Name) == strings.ToLower(name.Name) }); ok {
		return queryColumns(v.Query)
	}
	return nil
}

func queryColumns(q ast.QueryExpr) []string {
	switch q := q.(type) {
	case *ast.Query:
		return queryColumns(q.Query)
	case *ast.SubQuery:
		return queryColumns(q.Query)
	case *ast.CompoundQuery:
		return queryColumns(q.Queries[0])
	case *ast.Select:
		var cols []string
		for _, item := range q.Results {
			name := ""
			switch item := item.(type) {
			case *ast.Alias:
				name = item.As.Alias.Name
			case *ast.ExprSelectItem:
				switch x := item.Expr.(type) {
				case *ast.Ident:
					name = x.Name
				case *ast.Path:
					name = x.Idents[len(x.Idents)-1].Name
				}
			}
			if name == "" {
				return nil
			}
			cols = append(cols, strings.ToLower(name))
		}
		return cols
	}
	return nil
}

func elementName(e *ast.PropertyGraphElement) string {
	if e.Alias != nil {
		return strings.ToLower(e.Alias.Name)
	}
	return strings.ToLower(e.Name.Name)
}

// keyColumns lists an explicit KEY, or the table's primary key when omitted.
func keyColumns(table *Table, key *ast.PropertyGraphElementKey) string {
	var cols []string
	switch {
	case key != nil:
		for _, c := range key.Keys.ColumnNameList {
			cols = append(cols, strings.ToLower(c.Name))
		}
	case table != nil:
		for _, k := range table.PrimaryKeys {
			cols = append(cols, strings.ToLower(k.Name.Name))
		}
	}
	return "(" + strings.Join(cols, ",") + ")"
}

// referenceTarget names the referenced node element and its columns, defaulting to that node's key.
func referenceTarget(db *Database, nodes []*ast.PropertyGraphElement, ref *ast.Ident, cols *ast.PropertyGraphColumnNameList) string {
	name := strings.ToLower(ref.Name)
	if cols != nil {
		return name + keyColumns(nil, &ast.PropertyGraphElementKey{Keys: cols})
	}
	for _, n := range nodes {
		if elementName(n) == name {
			var key *ast.PropertyGraphElementKey
			if k, ok := n.Keys.(*ast.PropertyGraphNodeElementKey); ok {
				key = k.Key
			}
			return name + keyColumns(tableOf(db, n.Name), key)
		}
	}
	return name
}

// elementLabels returns each label with its sorted properties.
func elementLabels(cols []string, e *ast.PropertyGraphElement) []string {
	var labels []string
	add := func(label string, props ast.PropertyGraphElementProperties) {
		labels = append(labels, label+" "+strings.Join(elementProperties(cols, props), ","))
	}
	switch p := e.Properties.(type) {
	case nil:
		add(elementName(e), nil)
	case *ast.PropertyGraphSingleProperties:
		add(elementName(e), p.Properties)
	case *ast.PropertyGraphLabelAndPropertiesList:
		for _, lp := range p.LabelAndProperties {
			label := elementName(e)
			if l, ok := lp.Label.(*ast.PropertyGraphElementLabelLabelName); ok {
				label = strings.ToLower(l.Name.Name)
			}
			add(label, lp.Properties)
		}
	}
	slices.Sort(labels)
	return labels
}

// elementProperties expands ALL COLUMNS [EXCEPT ...] and NO PROPERTIES into "expr AS alias" entries.
func elementProperties(cols []string, props ast.PropertyGraphElementProperties) []string {
	var out []string
	switch p := props.(type) {
	case *ast.PropertyGraphNoProperties:
	case *ast.PropertyGraphDerivedPropertyList:
		for _, d := range p.DerivedProperties {
			expr := d.Expr.SQL()
			if id, ok := d.Expr.(*ast.Ident); ok {
				expr = strings.ToLower(id.Name)
			}
			alias := expr
			if d.Alias != nil {
				alias = strings.ToLower(d.Alias.Name)
			}
			out = append(out, alias+"="+expr)
		}
	default:
		var except []string
		if a, ok := p.(*ast.PropertyGraphPropertiesAre); ok && a.ExceptColumns != nil {
			for _, c := range a.ExceptColumns.ColumnNameList {
				except = append(except, strings.ToLower(c.Name))
			}
		}
		if cols == nil {
			return []string{"all"}
		}
		for _, name := range cols {
			if !slices.Contains(except, name) {
				out = append(out, name+"="+name)
			}
		}
	}
	slices.Sort(out)
	return out
}
