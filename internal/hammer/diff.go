package hammer

import (
	stdcmp "cmp"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/cloudspannerecosystem/memefish/ast"
	"github.com/cloudspannerecosystem/memefish/token"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func Diff(ddl1, ddl2 DDL) (DDL, error) {
	database1, err := NewDatabase(ddl1)
	if err != nil {
		return DDL{}, err
	}
	database2, err := NewDatabase(ddl2)
	if err != nil {
		return DDL{}, err
	}

	generator := &Generator{
		from:                      database1,
		to:                        database2,
		alteredChangeStreamStates: map[string]*ChangeStream{},
	}
	return generator.GenerateDDL()
}

func NewDatabase(ddl DDL) (*Database, error) {
	var (
		schemas              []*Schema
		tables               []*Table
		changeStreams        []*ChangeStream
		views                []*View
		functions            []*Function
		queues               []*Queue
		sequences            []*Sequence
		models               []*Model
		localityGroups       []*LocalityGroup
		roles                []*Role
		grants               []*Grant
		alterDatabaseOptions *ast.AlterDatabase
		options              *ast.Options
	)

	m := make(map[string]*Table)
	for _, istmt := range ddl.List {
		switch stmt := istmt.(type) {
		case *ast.CreateSchema:
			schemas = append(schemas, &Schema{CreateSchema: stmt})
		case *ast.CreateTable:
			t := &Table{CreateTable: stmt}
			tables = append(tables, t)
			m[identsToComparable(stmt.Name.Idents...)] = t
		case *ast.CreateIndex:
			if t, ok := m[identsToComparable(stmt.TableName.Idents...)]; ok {
				t.indexes = append(t.indexes, stmt)
			} else {
				return nil, fmt.Errorf("cannot find ddl of table to apply index %s", stmt.Name.SQL())
			}
		case *ast.CreateSearchIndex:
			if t, ok := m[identsToComparable(stmt.TableName.Idents...)]; ok {
				t.searchIndexes = append(t.searchIndexes, stmt)
			} else {
				return nil, fmt.Errorf("cannot find ddl of table to apply search index %s", stmt.Name.SQL())
			}
		case *ast.CreateVectorIndex:
			if t, ok := m[identsToComparable(stmt.TableName)]; ok {
				t.vectorIndexes = append(t.vectorIndexes, stmt)
			} else {
				return nil, fmt.Errorf("cannot find ddl of table to apply vector index %s", stmt.Name.SQL())
			}
		case *ast.AlterTable:
			t, ok := m[identsToComparable(stmt.Name.Idents...)]
			if !ok {
				return nil, fmt.Errorf("cannot find ddl of table to apply index %s", stmt.Name.SQL())
			}
			switch alteration := stmt.TableAlteration.(type) {
			case *ast.AddTableConstraint:
				t.TableConstraints = append(t.TableConstraints, alteration.TableConstraint)
			default:
				return nil, fmt.Errorf("unsupported table alteration: %v", stmt)
			}
		case *ast.AlterDatabase:
			alterDatabaseOptions = stmt
			options = stmt.Options
		case *ast.CreateChangeStream:
			switch forType := stmt.For.(type) {
			case *ast.ChangeStreamForTables:
				for _, table := range forType.Tables {
					if t, ok := m[identsToComparable(table.TableName)]; ok {
						t.changeStreams = append(t.changeStreams, &ChangeStream{CreateChangeStream: stmt})
					}
				}
			default:
				changeStreams = append(changeStreams, &ChangeStream{CreateChangeStream: stmt})
			}
		case *ast.CreateView:
			views = append(views, &View{CreateView: stmt})
		case *ast.CreateFunction:
			functions = append(functions, &Function{CreateFunction: stmt})
		case *ast.CreateQueue:
			queues = append(queues, &Queue{CreateQueue: stmt})
		case *ast.CreateSequence:
			sequences = append(sequences, &Sequence{CreateSequence: stmt})
		case *ast.CreateModel:
			models = append(models, &Model{CreateModel: stmt})
		case *ast.CreateLocalityGroup:
			localityGroups = append(localityGroups, &LocalityGroup{CreateLocalityGroup: stmt})
		case *ast.AlterLocalityGroup:
			// Only the built-in default group exists without a CREATE statement.
			if _, dup := findFirst(localityGroups, isDefaultLocalityGroup); !strings.EqualFold(stmt.Name.Name, defaultLocalityGroup) || dup {
				return nil, fmt.Errorf("unexpected ddl statement: %v", stmt.SQL())
			}
			localityGroups = append(localityGroups, &LocalityGroup{CreateLocalityGroup: &ast.CreateLocalityGroup{Name: stmt.Name, Options: stmt.Options}})
		case *ast.CreateRole:
			roles = append(roles, &Role{CreateRole: stmt})
		case *ast.Grant:
			grants = append(grants, &Grant{Grant: stmt})
		default:
			return nil, fmt.Errorf("unexpected ddl statement: %v", stmt.SQL())
		}
	}
	for _, t := range tables {
		if i := t.Cluster; i != nil {
			if p, ok := m[identsToComparable(i.TableName.Idents...)]; ok {
				p.children = append(p.children, t)
			} else {
				return nil, fmt.Errorf("parent ddl %s not found", i.TableName.SQL())
			}
		}
	}
	for _, q := range queues {
		if i := q.Cluster; i != nil {
			if p, ok := m[identsToComparable(i.TableName.Idents...)]; ok {
				p.queues = append(p.queues, q)
			} else {
				return nil, fmt.Errorf("parent ddl %s not found", i.TableName.SQL())
			}
		}
	}

	return &Database{
		schemas:              schemas,
		tables:               tables,
		changeStreams:        changeStreams,
		views:                views,
		functions:            functions,
		queues:               queues,
		sequences:            sequences,
		models:               models,
		localityGroups:       localityGroups,
		roles:                roles,
		grants:               grants,
		alterDatabaseOptions: alterDatabaseOptions,
		options:              options,
	}, nil
}

type Database struct {
	schemas              []*Schema
	tables               []*Table
	changeStreams        []*ChangeStream
	views                []*View
	functions            []*Function
	queues               []*Queue
	sequences            []*Sequence
	models               []*Model
	localityGroups       []*LocalityGroup
	roles                []*Role
	grants               []*Grant
	alterDatabaseOptions *ast.AlterDatabase
	options              *ast.Options
}

func (d *Database) grantsOnTable(table *Table) []*Grant {
	return d.grantsFromPath(table.Name)
}

func (d *Database) grantsFromPath(path *ast.Path) []*Grant {
	target := identsToComparable(path.Idents...)
	return grantsOn[*ast.PrivilegeOnTable](d, func(name string) bool { return name == target })
}

func (d *Database) grantsOnView(view *View) []*Grant {
	target := identsToComparable(view.Name.Idents...)
	return grantsOn[*ast.SelectPrivilegeOnView](d, func(name string) bool { return name == target })
}

func (d *Database) grantsOnChangeStream(cs *ChangeStream) []*Grant {
	target := identsToComparable(cs.Name)
	return grantsOn[*ast.SelectPrivilegeOnChangeStream](d, func(name string) bool { return name == target })
}

func (d *Database) grantsOnQueue(queue *Queue) []*Grant {
	target := identsToComparable(queue.Name.Idents...)
	return grantsOn[*ast.PrivilegeOnQueue](d, func(name string) bool { return strings.EqualFold(name, target) })
}

func (d *Database) grantsOnSequence(sequence *Sequence) []*Grant {
	target := identsToComparable(sequence.Name.Idents...)
	return grantsOn[*ast.PrivilegeOnSequence](d, func(name string) bool { return strings.EqualFold(name, target) })
}

func grantsOn[P ast.Privilege](d *Database, match func(name string) bool) []*Grant {
	var result []*Grant
	for _, grant := range d.grants {
		p, ok := grant.Grant.Privilege.(P)
		if !ok {
			continue
		}
		if privilegeNamesMatch(p, match) {
			result = append(result, grant)
		}
	}
	return result
}

func privilegeNamesMatch(p ast.Privilege, match func(name string) bool) bool {
	return slices.ContainsFunc(privilegeNames(p), func(n *ast.Path) bool {
		return match(identsToComparable(n.Idents...))
	})
}

func privilegeNames(p ast.Privilege) []*ast.Path {
	switch p := p.(type) {
	case *ast.PrivilegeOnTable:
		return p.Names
	case *ast.SelectPrivilegeOnView:
		return p.Names
	case *ast.SelectPrivilegeOnChangeStream:
		return p.Names
	case *ast.PrivilegeOnQueue:
		return p.Names
	case *ast.PrivilegeOnSequence:
		return p.Names
	case *ast.ExecutePrivilegeOnTableFunction:
		return p.Names
	}
	return nil
}

func (d *Database) grantsOnRole(role *Role) []*Grant {
	var result []*Grant
	target := identsToComparable(role.Name)
	for _, grant := range d.grants {
		for _, r := range grant.Roles {
			if identsToComparable(r) == target {
				result = append(result, grant)
				break
			}
		}
	}
	return result
}

type Table struct {
	*ast.CreateTable

	indexes       []*ast.CreateIndex
	searchIndexes []*ast.CreateSearchIndex
	vectorIndexes []*ast.CreateVectorIndex
	children      []*Table
	queues        []*Queue
	changeStreams []*ChangeStream
}

type Schema struct {
	*ast.CreateSchema
}

type Queue struct {
	*ast.CreateQueue
}

type View struct {
	*ast.CreateView
}

type Function struct {
	*ast.CreateFunction
}

type Sequence struct {
	*ast.CreateSequence
}

type Model struct {
	*ast.CreateModel
}

type LocalityGroup struct {
	*ast.CreateLocalityGroup
}

const (
	defaultLocalityGroup = "default"
	localityGroupOption  = "locality_group"
)

type Role struct {
	*ast.CreateRole
}

type Grant struct {
	*ast.Grant
}

type ChangeStream struct {
	*ast.CreateChangeStream
}

type ChangeStreamType string

const (
	ChangeStreamTypeAll    ChangeStreamType = "ALL"
	ChangeStreamTypeNone   ChangeStreamType = "NONE"
	ChangeStreamTypeTables ChangeStreamType = "TABLES"
)

func (cs *ChangeStream) Type() ChangeStreamType {
	if cs.For == nil {
		return ChangeStreamTypeNone
	}
	switch cs.For.(type) {
	case *ast.ChangeStreamForTables:
		return ChangeStreamTypeTables
	default:
		return ChangeStreamTypeAll
	}
}

type droppedConstraint struct {
	table      string
	constraint *ast.TableConstraint
}

type Generator struct {
	from *Database
	to   *Database

	droppedTables                  []string
	droppedIndexes                 []string
	droppedChangeStreams           []string
	droppedQueues                  []string
	droppedConstraints             []droppedConstraint
	droppedGrants                  []*Grant
	willCreateOrAlterChangeStreams []*ChangeStream
	deferredColumnDrops            DDL
	alteredChangeStreamStates      map[string]*ChangeStream
}

func (g *Generator) GenerateDDL() (DDL, error) {
	ddl := DDL{}

	// for alter database
	ddl.AppendDDL(g.generateDDLForAlterDatabaseOptions())

	// create schemas before any object that may be created inside them
	for _, toSchema := range g.to.schemas {
		if _, exists := g.findSchemaByName(g.from.schemas, identsToComparable(toSchema.Name)); !exists {
			ddl.Append(toSchema)
		}
	}

	// create or alter sequences before any column default that may use them
	for _, toSequence := range g.to.sequences {
		fromSequence, exists := g.findSequenceByName(g.from.sequences, identsToComparable(toSequence.Name.Idents...))
		if !exists {
			ddl.Append(toSequence)
			continue
		}
		ddl.AppendDDL(g.generateDDLForAlterSequence(fromSequence, toSequence))
	}

	// create or alter locality groups before any table or column that uses them
	for _, toGroup := range g.to.localityGroups {
		fromGroup, exists := g.findLocalityGroupByName(g.from.localityGroups, toGroup.Name.Name)
		switch {
		case exists:
			ddl.AppendDDL(g.generateDDLForAlterLocalityGroup(fromGroup, toGroup))
		case isDefaultLocalityGroup(toGroup):
			ddl.AppendDDL(g.generateDDLForAlterLocalityGroup(&LocalityGroup{&ast.CreateLocalityGroup{Name: toGroup.Name}}, toGroup))
		default:
			ddl.Append(toGroup)
		}
	}

	// create or replace functions before the views and functions that call them
	functions, err := sortFunctionsByDependency(g.to.functions)
	if err != nil {
		return DDL{}, err
	}
	// Spanner rejects replacing a function's signature under the functions and views that call it, and
	// the same holds for a model's columns: drop them first, recreate them below.
	changed, signature := g.changedFunctions()
	maps.Copy(signature, g.replacedModels())
	recreatedFunctions, recreatedViews, err := g.callersOfChangedSignatures(signature)
	if err != nil {
		return DDL{}, err
	}
	recreatedFunction, recreatedView := map[string]bool{}, map[string]bool{}
	for _, f := range recreatedFunctions {
		recreatedFunction[functionKey(f.Name)] = true
	}
	for _, v := range recreatedViews {
		recreatedView[lowerName(v.Name)] = true
	}
	if err := g.rejectTablesUsingFunctions(changed, signature, recreatedFunction); err != nil {
		return DDL{}, err
	}
	for _, v := range slices.Backward(recreatedViews) {
		ddl.AppendDDL(g.generateDDLForDropView(v))
	}
	for _, f := range slices.Backward(recreatedFunctions) {
		ddl.Append(&ast.DropFunction{Name: f.Name})
	}
	// create or alter models before the views that call ML.PREDICT on them
	for _, toModel := range g.to.models {
		fromModel, exists := g.findModelByName(g.from.models, toModel.Name.Name)
		if !exists {
			ddl.Append(toModel)
			continue
		}
		ddl.AppendDDL(g.generateDDLForAlterModel(fromModel, toModel))
	}

	for _, toFunction := range functions {
		fromFunction, exists := g.findFunctionByName(g.from.functions, toFunction.Name)
		replace := exists && !recreatedFunction[functionKey(toFunction.Name)]
		if exists && g.functionEqual(fromFunction, toFunction) && replace {
			continue
		}
		ddl.Append(&ast.CreateFunction{
			OrReplace: replace, Name: toFunction.Name, Params: toFunction.Params, ReturnType: toFunction.ReturnType,
			SqlSecurity: toFunction.SqlSecurity, Determinism: toFunction.Determinism, Language: toFunction.Language,
			Remote: toFunction.Remote, Options: toFunction.Options, Definition: toFunction.Definition,
		})
	}

	// drop views before any table or column they may reference is dropped
	for i := len(g.from.views) - 1; i >= 0; i-- {
		fromView := g.from.views[i]
		if _, exists := g.findViewByName(g.to.views, identsToComparable(fromView.Name.Idents...)); !exists && !recreatedView[lowerName(fromView.Name)] {
			ddl.AppendDDL(g.generateDDLForDropView(fromView))
		}
	}

	// for alter table
	var constraintTargets []*Table
	for _, toTable := range g.to.tables {
		fromTable, exists := g.findTableByName(g.from.tables, identsToComparable(toTable.Name.Idents...))

		if !exists {
			ddl.AppendDDL(g.generateDDLForCreateTableAndIndex(toTable))
			continue
		}

		if g.isDroppedTable(identsToComparable(toTable.Name.Idents...)) {
			ddl.AppendDDL(g.generateDDLForCreateTableAndIndex(toTable))
			continue
		}

		if !g.interleaveEqual(fromTable, toTable) {
			ddl.AppendDDL(g.generateDDLForDropConstraintIndexAndTable(fromTable))
			ddl.AppendDDL(g.generateDDLForCreateTableAndIndex(toTable))
			continue
		}

		if !g.primaryKeyEqual(fromTable, toTable) {
			ddl.AppendDDL(g.generateDDLForDropConstraintIndexAndTable(fromTable))
			ddl.AppendDDL(g.generateDDLForCreateTableAndIndex(toTable))
			continue
		}

		if options := localityGroupOptions(fromTable.Options, toTable.Options); options != nil {
			ddl.Append(&ast.AlterTable{Name: toTable.Name, TableAlteration: &ast.AlterTableSetOptions{Options: options}})
		}
		ddl.AppendDDL(g.generateDDLForDropIndex(fromTable, toTable))
		ddl.AppendDDL(g.generateDDLForDropStoredColumn(fromTable, toTable))
		columns, err := g.generateDDLForColumns(fromTable, toTable)
		if err != nil {
			return DDL{}, err
		}
		ddl.AppendDDL(columns)
		ddl.AppendDDL(g.generateDDLForCreateIndex(fromTable, toTable))
		ddl.AppendDDL(g.generateDDLForAlterIndex(fromTable, toTable))
		ddl.AppendDDL(g.generateDDLForAlterVectorIndex(fromTable, toTable))
		constraintTargets = append(constraintTargets, toTable)
		ddl.AppendDDL(g.generateDDLForRowDeletionPolicy(fromTable, toTable))
		ddl.AppendDDL(g.generateDDLForCreateChangeStream(g.from, toTable))
	}

	// for constraints
	for _, toTable := range constraintTargets {
		fromTable, _ := g.findTableByName(g.from.tables, identsToComparable(toTable.Name.Idents...))
		ddl.AppendDDL(g.generateDDLForConstraints(fromTable, toTable))
	}

	// for queues
	for _, toQueue := range g.to.queues {
		name := identsToComparable(toQueue.Name.Idents...)
		fromQueue, exists := g.findQueueByName(g.from.queues, name)
		if !exists || g.isDroppedQueue(name) {
			ddl.Append(toQueue)
			continue
		}
		// ALTER QUEUE cannot change columns, primary keys or the parent table.
		if !g.queueColumnsEqual(fromQueue, toQueue) || !queueInterleaveAlterable(fromQueue.Cluster, toQueue.Cluster) {
			ddl.AppendDDL(g.generateDDLForDropQueue(fromQueue))
			ddl.Append(toQueue)
			continue
		}
		ddl.AppendDDL(g.generateDDLForAlterQueue(fromQueue, toQueue))
	}

	// for alter change stream
	for _, toChangeStream := range g.to.changeStreams {
		fromChangeStream, exists := g.findChangeStreamByName(g.from, identsToComparable(toChangeStream.Name))
		if !exists {
			ddl.Append(toChangeStream)
			continue
		}
		ddl.AppendDDL(g.generateDDLForAlterChangeStream(fromChangeStream, toChangeStream))
	}
	for _, cs := range g.willCreateOrAlterChangeStreams {
		fromChangeStream, exists := g.findChangeStreamByName(g.from, identsToComparable(cs.Name))
		if !exists || g.isDroppedChangeStream(identsToComparable(cs.Name)) {
			ddl.Append(cs)
			continue
		}
		if alteredState, hasAlteredState := g.alteredChangeStreamStates[identsToComparable(cs.Name)]; hasAlteredState {
			fromChangeStream = alteredState
		}
		ddl.AppendDDL(g.generateDDLForAlterChangeStream(fromChangeStream, cs))
	}

	// for views, after the views they select from
	views, err := sortViewsByDependency(g.to.views)
	if err != nil {
		return DDL{}, err
	}
	for _, toView := range views {
		fromView, exists := g.findViewByName(g.from.views, identsToComparable(toView.Name.Idents...))
		if !exists || recreatedView[lowerName(toView.Name)] {
			ddl.Append(toView)
			continue
		}
		if g.viewEqual(fromView, toView) {
			continue
		}
		ddl.AppendDDL(g.generateDDLForReplaceView(fromView, toView))
	}

	// drop change streams
	for _, fromChangeStream := range g.from.changeStreams {
		if g.isDroppedChangeStream(identsToComparable(fromChangeStream.Name)) {
			continue
		}
		if _, exists := g.findChangeStreamByName(g.to, identsToComparable(fromChangeStream.Name)); !exists {
			ddl.AppendDDL(g.generateDDLForDropChangeStream(fromChangeStream))
			g.droppedChangeStreams = append(g.droppedChangeStreams, identsToComparable(fromChangeStream.Name))
		}
	}
	for _, fromTable := range g.from.tables {
		if _, exists := g.findTableByName(g.to.tables, identsToComparable(fromTable.Name.Idents...)); !exists {
			continue
		}
		for _, cs := range fromTable.changeStreams {
			if g.isDroppedChangeStream(identsToComparable(cs.Name)) {
				continue
			}
			if _, exists := g.findChangeStreamByName(g.to, identsToComparable(cs.Name)); !exists {
				ddl.AppendDDL(g.generateDDLForDropChangeStream(cs))
				g.droppedChangeStreams = append(g.droppedChangeStreams, identsToComparable(cs.Name))
			}
		}
	}

	// Spanner rejects dropping a column that a view or change stream still references.
	ddl.AppendDDL(g.deferredColumnDrops)

	// drop queues
	for _, fromQueue := range g.from.queues {
		name := identsToComparable(fromQueue.Name.Idents...)
		if g.isDroppedQueue(name) {
			continue
		}
		if _, exists := g.findQueueByName(g.to.queues, name); !exists {
			ddl.AppendDDL(g.generateDDLForDropQueue(fromQueue))
		}
	}

	// drop tables
	for _, fromTable := range g.from.tables {
		if _, exists := g.findTableByName(g.to.tables, identsToComparable(fromTable.Name.Idents...)); !exists {
			ddl.AppendDDL(g.generateDDLForDropConstraintIndexAndTable(fromTable))
		}
	}

	// drop functions after the views and functions that call them have been dropped or replaced
	droppedFunctions, err := sortFunctionsByDependency(g.from.functions)
	if err != nil {
		return DDL{}, err
	}
	for _, fromFunction := range slices.Backward(droppedFunctions) {
		if _, exists := g.findFunctionByName(g.to.functions, fromFunction.Name); !exists && !recreatedFunction[functionKey(fromFunction.Name)] {
			ddl.Append(&ast.DropFunction{Name: fromFunction.Name})
		}
	}

	// drop models after the views that call ML.PREDICT on them have been dropped
	for _, fromModel := range g.from.models {
		if _, exists := g.findModelByName(g.to.models, fromModel.Name.Name); !exists {
			ddl.Append(&ast.DropModel{Name: fromModel.Name})
		}
	}

	// drop sequences after every column default that may use them has been dropped
	for _, fromSequence := range g.from.sequences {
		if _, exists := g.findSequenceByName(g.to.sequences, identsToComparable(fromSequence.Name.Idents...)); !exists {
			ddl.AppendDDL(g.generateDDLForDropSequence(fromSequence))
		}
	}

	// drop locality groups after every table and column that used them has been dropped or moved
	for _, fromGroup := range g.from.localityGroups {
		if _, exists := g.findLocalityGroupByName(g.to.localityGroups, fromGroup.Name.Name); exists {
			continue
		}
		if isDefaultLocalityGroup(fromGroup) {
			ddl.AppendDDL(g.generateDDLForAlterLocalityGroup(fromGroup, &LocalityGroup{&ast.CreateLocalityGroup{Name: fromGroup.Name}}))
			continue
		}
		ddl.Append(&ast.DropLocalityGroup{Name: fromGroup.Name})
	}

	// for roles
	for _, toRole := range g.to.roles {
		roleName := identsToComparable(toRole.Name)
		if _, exists := g.findRoleByName(g.from.roles, roleName); !exists {
			ddl.Append(toRole)
			continue
		}
	}
	for _, fromRole := range g.from.roles {
		roleName := identsToComparable(fromRole.Name)
		if _, exists := g.findRoleByName(g.to.roles, roleName); !exists {
			ddl.AppendDDL(g.generateDDLForDropRole(fromRole))
		}
	}

	// for grants
	for _, fromGrant := range g.from.grants {
		if _, exists := g.findGrant(g.to.grants, fromGrant); !exists {
			if g.isDroppedGrant(fromGrant) {
				continue
			}
			ddl.AppendDDL(g.generateDDLForRevokeAll(fromGrant))
		}
	}
	for _, toGrant := range g.to.grants {
		if fromGrant, exists := g.findGrant(g.from.grants, toGrant); exists {
			if g.isDroppedGrant(fromGrant) {
				ddl.Append(toGrant)
			}
		} else {
			ddl.Append(toGrant)
		}
	}

	// drop schemas after every object inside them has been dropped
	for _, fromSchema := range g.from.schemas {
		if _, exists := g.findSchemaByName(g.to.schemas, identsToComparable(fromSchema.Name)); !exists {
			ddl.Append(&ast.DropSchema{Name: fromSchema.Name})
		}
	}

	return ddl, nil
}

func (g *Generator) generateDDLForAlterDatabaseOptions() DDL {
	ddl := DDL{}
	optionsFrom := make(map[string]string)
	optionsTo := make(map[string]string)
	if g.from.options != nil {
		for _, o := range g.from.options.Records {
			optionsFrom[o.Name.Name] = o.Value.SQL()
		}
	}
	if g.to.options != nil {
		for _, o := range g.to.options.Records {
			optionsTo[o.Name.Name] = o.Value.SQL()
		}
	}
	if reflect.DeepEqual(optionsFrom, optionsTo) {
		return ddl
	}
	alter := g.to.alterDatabaseOptions
	if alter == nil {
		alter = g.from.alterDatabaseOptions
	}
	var records []*ast.OptionsDef
	if g.to.alterDatabaseOptions != nil {
		records = slices.Clone(alter.Options.Records)
	}
	if g.from.options != nil {
		for _, r := range g.from.options.Records {
			if _, ok := optionsTo[r.Name.Name]; ok {
				continue
			}
			records = append(records, &ast.OptionsDef{
				Name:  &ast.Ident{Name: r.Name.Name},
				Value: &ast.NullLiteral{},
			})
		}
	}

	ddl.Append(&ast.AlterDatabase{
		Name:    alter.Name,
		Options: &ast.Options{Records: records},
	})
	return ddl
}

func (g *Generator) generateDDLForCreateTableAndIndex(table *Table) DDL {
	ddl := DDL{}

	ddl.Append(table)
	for _, i := range table.indexes {
		ddl.Append(i)
	}
	for _, i := range table.searchIndexes {
		ddl.Append(i)
	}
	for _, i := range table.vectorIndexes {
		ddl.Append(i)
	}
	for _, cs := range table.changeStreams {
		g.addWillCreateOrAlterChangeStream(cs)
	}
	return ddl
}

func (g *Generator) addWillCreateOrAlterChangeStream(cs *ChangeStream) {
	if g.isWillCreateOrAlterChangeStream(identsToComparable(cs.Name)) {
		return
	}
	g.willCreateOrAlterChangeStreams = append(g.willCreateOrAlterChangeStreams, cs)
}

func (g *Generator) isWillCreateOrAlterChangeStream(name string) bool {
	for _, cs := range g.willCreateOrAlterChangeStreams {
		if identsToComparable(cs.Name) == name {
			return true
		}
	}
	return false
}

func (g *Generator) generateDDLForDropConstraintIndexAndTable(table *Table) DDL {
	ddl := DDL{}

	if g.isDroppedTable(identsToComparable(table.Name.Idents...)) {
		return ddl
	}
	for _, t := range table.children {
		ddl.AppendDDL(g.generateDDLForDropConstraintIndexAndTable(t))
	}
	for _, q := range table.queues {
		if !g.isDroppedQueue(identsToComparable(q.Name.Idents...)) {
			ddl.AppendDDL(g.generateDDLForDropQueue(q))
		}
	}
	for _, i := range table.indexes {
		ddl.Append(&ast.DropIndex{Name: i.Name})
	}
	for _, i := range table.searchIndexes {
		ddl.Append(&ast.DropSearchIndex{Name: i.Name})
	}
	for _, i := range table.vectorIndexes {
		ddl.Append(&ast.DropVectorIndex{Name: i.Name})
	}
	for _, cs := range table.changeStreams {
		if g.isDroppedChangeStream(identsToComparable(cs.Name)) {
			continue
		}
		if alter, ok := g.generateDDLForDetachTableFromChangeStream(table, cs); ok {
			ddl.AppendDDL(alter)
			continue
		}
		if _, exists := g.findChangeStreamByName(g.to, identsToComparable(cs.Name)); exists {
			if _, tableExists := g.findTableByName(g.to.tables, identsToComparable(table.Name.Idents...)); !tableExists {
				continue
			}
		}
		ddl.AppendDDL(g.generateDDLForDropChangeStream(cs))
		g.droppedChangeStreams = append(g.droppedChangeStreams, identsToComparable(cs.Name))
	}
	ddl.AppendDDL(g.generateDDLForDropNamedConstraintsMatchingPredicate(func(_ *Table, constraint *ast.TableConstraint) bool {
		fk, ok := constraint.Constraint.(*ast.ForeignKey)
		if !ok {
			return false
		}
		return identsToComparable(fk.ReferenceTable.Idents...) == identsToComparable(table.Name.Idents...)
	}))
	g.markGrantsDropped(g.from.grantsOnTable(table))
	ddl.Append(DataLoss{&ast.DropTable{Name: table.Name}, "drops table " + table.Name.SQL()})
	g.droppedTables = append(g.droppedTables, identsToComparable(table.Name.Idents...))
	return ddl
}

func (g *Generator) generateDDLForDetachTableFromChangeStream(table *Table, cs *ChangeStream) (DDL, bool) {
	ddl := DDL{}

	csFor, ok := cs.For.(*ast.ChangeStreamForTables)
	if !ok || len(csFor.Tables) <= 1 {
		return ddl, false
	}
	toCS, exists := g.findChangeStreamByName(g.to, identsToComparable(cs.Name))
	if !exists {
		return ddl, false
	}

	tableName := identsToComparable(table.Name.Idents...)
	var remainingTables []*ast.ChangeStreamForTable
	for _, t := range csFor.Tables {
		if identsToComparable(t.TableName) != tableName {
			remainingTables = append(remainingTables, t)
		}
	}
	hasRemainingTableInTarget := slices.ContainsFunc(remainingTables, func(t *ast.ChangeStreamForTable) bool {
		_, exists := g.findTableByName(g.to.tables, identsToComparable(t.TableName))
		return exists
	})
	if !hasRemainingTableInTarget {
		return ddl, false
	}

	g.alteredChangeStreamStates[identsToComparable(cs.Name)] = &ChangeStream{
		CreateChangeStream: &ast.CreateChangeStream{
			Name:    cs.Name,
			For:     &ast.ChangeStreamForTables{Tables: remainingTables},
			Options: cs.Options,
		},
	}

	// Do not emit ALTER here when it is already handled in GenerateDDL
	// via willCreateOrAlterChangeStreams.
	// Also emit intermediate SET FOR only when the final target is FOR TABLES.
	if !g.isWillCreateOrAlterChangeStream(identsToComparable(cs.Name)) && toCS.Type() == ChangeStreamTypeTables {
		ddl.Append(&ast.AlterChangeStream{
			Name: cs.Name,
			ChangeStreamAlteration: &ast.ChangeStreamSetFor{
				For: &ast.ChangeStreamForTables{Tables: remainingTables},
			},
		})
	}
	return ddl, true
}

func (g *Generator) generateDDLForConstraints(from, to *Table) DDL {
	ddl := DDL{}

	for _, toConstraint := range to.TableConstraints {
		isUnnamedConstraint := toConstraint.Name == nil

		if isUnnamedConstraint {
			_, exists := g.findUnnamedConstraint(from.TableConstraints, toConstraint)
			if !exists {
				ddl.Append(&ast.AlterTable{Name: to.Name, TableAlteration: &ast.AddTableConstraint{TableConstraint: toConstraint}})
			}
			continue
		}

		fromConstraint, exists := g.findNamedConstraint(from.TableConstraints, identsToComparable(toConstraint.Name))

		if !exists || g.isDroppedConstraint(to.Name, toConstraint) {
			ddl.Append(&ast.AlterTable{Name: to.Name, TableAlteration: &ast.AddTableConstraint{TableConstraint: toConstraint}})
			continue
		}

		if g.constraintEqual(fromConstraint, toConstraint) {
			continue
		}

		ddl.AppendDDL(g.generateDDLForDropNamedConstraint(from.Name, fromConstraint))
		ddl.Append(&ast.AlterTable{
			Name:            to.Name,
			TableAlteration: &ast.AddTableConstraint{TableConstraint: toConstraint},
		})
	}

	for _, fromConstraint := range from.TableConstraints {
		if fromConstraint.Name == nil {
			continue
		}

		if _, exists := g.findNamedConstraint(to.TableConstraints, identsToComparable(fromConstraint.Name)); !exists {
			ddl.AppendDDL(g.generateDDLForDropNamedConstraint(from.Name, fromConstraint))
		}
	}
	return ddl
}

func (g *Generator) generateDDLForRowDeletionPolicy(from, to *Table) DDL {
	ddl := DDL{}

	if alteration := g.rowDeletionPolicyAlteration(from.RowDeletionPolicy, to.RowDeletionPolicy); alteration != nil {
		ddl.Append(markRowDeletionPolicy(&ast.AlterTable{Name: to.Name, TableAlteration: alteration}, alteration))
	}
	return ddl
}

// markRowDeletionPolicy flags the alterations that start deleting rows.
func markRowDeletionPolicy(stmt Statement, alteration rowDeletionPolicyAlteration) Statement {
	if _, drops := alteration.(*ast.DropRowDeletionPolicy); drops {
		return stmt
	}
	return DataLoss{stmt, "row deletion policy may delete existing rows"}
}

// rowDeletionPolicyAlteration is an alteration that both ALTER TABLE and ALTER QUEUE accept.
type rowDeletionPolicyAlteration interface {
	ast.TableAlteration
	ast.QueueAlteration
}

func (g *Generator) rowDeletionPolicyAlteration(from, to *ast.CreateRowDeletionPolicy) rowDeletionPolicyAlteration {
	switch {
	case from != nil && to != nil:
		if g.createRowDeletionPolicyEqual(from, to) {
			return nil
		}
		return &ast.ReplaceRowDeletionPolicy{RowDeletionPolicy: to.RowDeletionPolicy}
	case from != nil && to == nil:
		return &ast.DropRowDeletionPolicy{}
	case from == nil && to != nil:
		return &ast.AddRowDeletionPolicy{RowDeletionPolicy: to.RowDeletionPolicy}
	}
	return nil
}

func (g *Generator) generateDDLForColumns(from, to *Table) (DDL, error) {
	ddl := DDL{}

	for _, toCol := range to.Columns {
		fromCol, exists := g.findColumnByName(from.Columns, identsToComparable(toCol.Name))

		if !exists {
			add, err := g.generateDDLForAddColumn(to.Name, toCol)
			if err != nil {
				return DDL{}, err
			}
			ddl.AppendDDL(add)
			continue
		}

		if isColHidden(fromCol) != isColHidden(toCol) {
			ddl.Append(AlterColumn{Table: to.Name.SQL(), Def: toCol})
			ddl.AppendDDL(g.generateDDLForColumnOptions(to, fromCol, toCol))
			continue
		}

		if g.columnDefEqual(fromCol, toCol) {
			ddl.AppendDDL(g.generateDDLForColumnOptions(to, fromCol, toCol))
			continue
		}

		if !g.columnTypeAlterable(from, fromCol, toCol) || requireDropAndCreateByDefault(fromCol.DefaultSemantics) || requireDropAndCreateByDefault(toCol.DefaultSemantics) {
			recreate, err := g.generateDDLForDropAndCreateColumn(from, to, fromCol, toCol)
			if err != nil {
				return DDL{}, err
			}
			ddl.AppendDDL(recreate)
			continue
		}

		notNullChanged := fromCol.NotNull != toCol.NotNull
		st, ok := fromCol.Type.(*ast.ScalarSchemaType)
		isTimestamp := ok && st.Name == ast.TimestampTypeName
		if !isTimestamp || notNullChanged || !g.columnDefaultExprEqual(fromCol.DefaultSemantics, toCol.DefaultSemantics) {
			if notNullChanged {
				ddl.AppendDDL(g.generateDDLForDropForeignKeysReferencingColumn(to.Name, toCol.Name))
			}
			if !fromCol.NotNull && toCol.NotNull {
				if toCol.DefaultSemantics == nil {
					if _, err := withZeroDefault(toCol); err != nil {
						return DDL{}, fmt.Errorf("setting NOT NULL on column %s.%s: %w", to.Name.SQL(), toCol.Name.SQL(), err)
					}
				}
				ddl.Append(Update{Table: to.Name.SQL(), Def: toCol})
			}
			ddl.Append(AlterColumn{Table: to.Name.SQL(), Def: toCol})
		}
		ddl.AppendDDL(g.generateDDLForColumnOptions(to, fromCol, toCol))
	}
	for _, fromCol := range from.Columns {
		if _, exists := g.findColumnByName(to.Columns, identsToComparable(fromCol.Name)); !exists {
			drop := g.generateDDLForDropColumn(from.Name, fromCol.Name)
			if g.isReferencedByViewOrChangeStream(fromCol.Name) {
				// Only the DROP COLUMN waits; the constraint drops before it must stay ahead of any re-added constraint.
				last := len(drop.List) - 1
				g.deferredColumnDrops.Append(drop.List[last])
				drop.List = drop.List[:last]
			}
			ddl.AppendDDL(drop)
		}
	}
	return ddl, nil
}

// isReferencedByViewOrChangeStream reports whether a view or change stream that the diff rewrites
// or drops mentions the column name, so that must come before the column is dropped.
func (g *Generator) isReferencedByViewOrChangeStream(column *ast.Ident) bool {
	mentions := func(n ast.Node) bool {
		for c := range ast.Preorder(n) {
			if id, ok := c.(*ast.Ident); ok && strings.EqualFold(id.Name, column.Name) {
				return true
			}
		}
		return false
	}
	for _, v := range g.from.views {
		if toView, ok := g.findViewByName(g.to.views, identsToComparable(v.Name.Idents...)); ok && !g.viewEqual(v, toView) && mentions(v.Query) {
			return true
		}
	}
	changeStreams := slices.Clone(g.from.changeStreams)
	for _, t := range g.from.tables {
		changeStreams = append(changeStreams, t.changeStreams...)
	}
	return slices.ContainsFunc(changeStreams, func(cs *ChangeStream) bool {
		return cs.For != nil && mentions(cs.For)
	})
}

func (g *Generator) generateDDLForColumnOptions(table *Table, from, to *ast.ColumnDef) DDL {
	ddl := DDL{}
	if st, ok := from.Type.(*ast.ScalarSchemaType); ok && st.Name == ast.TimestampTypeName && !g.optionsValueEqual(from.Options, to.Options, "allow_commit_timestamp") {
		ddl.Append(AlterColumn{Table: table.Name.SQL(), Def: to, SetOptions: true})
	}
	if options := localityGroupOptions(from.Options, to.Options); options != nil {
		ddl.Append(&ast.AlterTable{Name: table.Name, TableAlteration: &ast.AlterColumn{Name: to.Name, Alteration: &ast.AlterColumnSetOptions{Options: options}}})
	}
	return ddl
}

// localityGroupOptions returns the SET OPTIONS that move a table or column between locality groups,
// or nil when it stays. An object with no locality_group is in the default group, and Spanner
// rejects locality_group = NULL, so leaving a group names the default one.
func localityGroupOptions(from, to *ast.Options) *ast.Options {
	if strings.EqualFold(localityGroupOf(from), localityGroupOf(to)) {
		return nil
	}
	return &ast.Options{Records: []*ast.OptionsDef{{
		Name:  &ast.Ident{Name: localityGroupOption},
		Value: &ast.StringLiteral{Value: localityGroupOf(to)},
	}}}
}

func localityGroupOf(options *ast.Options) string {
	if v := optionsValueFromName(options, localityGroupOption); v != nil {
		if s, ok := (*v).(*ast.StringLiteral); ok {
			return s.Value
		}
	}
	return defaultLocalityGroup
}

// columnTypeAlterable reports whether the column can change type in place. A column used by a
// foreign key, check constraint or generated column is recreated instead, as Spanner refuses to retype it.
func (g *Generator) columnTypeAlterable(table *Table, from, to *ast.ColumnDef) bool {
	if g.columnTypeEqual(from, to) {
		return true
	}
	// The backfill for a new NOT NULL would write a value of the target type into a column of the old type.
	if !alterableTypeChange(from.Type, to.Type) || !from.NotNull && to.NotNull {
		return false
	}
	for _, c := range table.Columns {
		if gen, ok := c.DefaultSemantics.(*ast.GeneratedColumnExpr); ok && exprReferencesColumn(gen.Expr, from.Name) {
			return false
		}
	}
	for _, t := range g.from.tables {
		for _, c := range t.TableConstraints {
			if foreignKeyReferencesColumn(t, table.Name, from.Name, c) || checkReferencesColumn(t, table.Name, from.Name, c) {
				return false
			}
		}
	}
	return true
}

// alterableTypeChange reports whether Spanner converts from to to in place with ALTER COLUMN.
// PROTO and ENUM changes are left out: the schema syntax cannot tell them apart, so an ALTER may be rejected.
// https://cloud.google.com/spanner/docs/schema-updates#supported-updates
func alterableTypeChange(from, to ast.SchemaType) bool {
	if fromArray, ok := from.(*ast.ArraySchemaType); ok {
		toArray, ok := to.(*ast.ArraySchemaType)
		return ok && alterableTypeChange(fromArray.Item, toArray.Item)
	}
	return alterableTypeKinds[[2]string{typeKind(from), typeKind(to)}]
}

var alterableTypeKinds = map[[2]string]bool{
	{"STRING", "BYTES"}: true,
	{"BYTES", "STRING"}: true,
}

func typeKind(t ast.SchemaType) string {
	switch t := t.(type) {
	case *ast.ScalarSchemaType:
		return string(t.Name)
	case *ast.SizedSchemaType:
		return string(t.Name)
	}
	return fmt.Sprintf("%T", t)
}

func requireDropAndCreateByDefault(d ast.ColumnDefaultSemantics) bool {
	_, isExpr := d.(*ast.ColumnDefaultExpr)
	return d != nil && !isExpr
}

func (g *Generator) generateDDLForAddColumn(table *ast.Path, col *ast.ColumnDef) (DDL, error) {
	ddl := DDL{}

	if !col.NotNull || col.DefaultSemantics != nil {
		ddl.Append(&ast.AlterTable{Name: table, TableAlteration: &ast.AddColumn{Column: col}})
		return ddl, nil
	}
	// A NOT NULL column needs a default to fill existing rows.
	withDefault, err := withZeroDefault(col)
	if err != nil {
		return DDL{}, fmt.Errorf("adding NOT NULL column %s.%s: %w", table.SQL(), col.Name.SQL(), err)
	}
	ddl.Append(&ast.AlterTable{Name: table, TableAlteration: &ast.AddColumn{Column: withDefault}})
	ddl.Append(&ast.AlterTable{Name: table, TableAlteration: &ast.AlterColumn{Name: col.Name, Alteration: &ast.AlterColumnDropDefault{}}})
	return ddl, nil
}

func (g *Generator) generateDDLForDropForeignKeysReferencingColumn(table *ast.Path, column *ast.Ident) DDL {
	return g.generateDDLForDropNamedConstraintsMatchingPredicate(func(t *Table, constraint *ast.TableConstraint) bool {
		return foreignKeyReferencesColumn(t, table, column, constraint)
	})
}

func (g *Generator) generateDDLForDropColumn(table *ast.Path, column *ast.Ident) DDL {
	ddl := DDL{}

	ddl.AppendDDL(g.generateDDLForDropNamedConstraintsMatchingPredicate(func(t *Table, constraint *ast.TableConstraint) bool {
		return foreignKeyReferencesColumn(t, table, column, constraint) || checkReferencesColumn(t, table, column, constraint)
	}))

	grants := g.from.grantsFromPath(table)
	for _, grant := range grants {
		priv, ok := grant.Grant.Privilege.(*ast.PrivilegeOnTable)
		if !ok {
			continue
		}
		if !hasPrivilegeOnColumn(priv, column) {
			continue
		}
		if !g.markGrantDropped(grant) {
			continue
		}
		// A grant that also covers other columns survives the drop on them, so it is revoked first.
		if !grantOnColumnOnly(priv, column) {
			ddl.AppendDDL(g.generateDDLForRevokeAll(grant))
		}
	}

	ddl.Append(DataLoss{&ast.AlterTable{
		Name:            table,
		TableAlteration: &ast.DropColumn{Name: column},
	}, "drops column " + table.SQL() + "." + column.SQL()})

	return ddl
}

func (g *Generator) generateDDLForDropAndCreateColumn(from, to *Table, fromCol, toCol *ast.ColumnDef) (DDL, error) {
	ddl := DDL{}

	indexes := []*ast.CreateIndex{}
	for _, i := range g.findIndexByColumn(from.indexes, identsToComparable(fromCol.Name)) {
		if !g.isDroppedIndex(identsToComparable(i.Name.Idents...)) {
			indexes = append(indexes, i)
		}
	}
	for _, i := range indexes {
		ddl.Append(&ast.DropIndex{Name: i.Name})
	}

	searchIndexes := []*ast.CreateSearchIndex{}
	for _, i := range g.findSearchIndexByColumn(from.searchIndexes, identsToComparable(fromCol.Name)) {
		if !g.isDroppedIndex(identsToComparable(i.Name.Idents...)) {
			searchIndexes = append(searchIndexes, i)
		}
	}
	for _, i := range searchIndexes {
		ddl.Append(&ast.DropSearchIndex{Name: i.Name})
	}

	vectorIndexes := []*ast.CreateVectorIndex{}
	for _, i := range from.vectorIndexes {
		if vectorIndexReferencesColumn(i, identsToComparable(fromCol.Name)) && !g.isDroppedIndex(identsToComparable(i.Name)) {
			vectorIndexes = append(vectorIndexes, i)
		}
	}
	for _, i := range vectorIndexes {
		ddl.Append(&ast.DropVectorIndex{Name: i.Name})
	}

	ddl.AppendDDL(g.generateDDLForDropColumn(from.Name, fromCol.Name))

	add, err := g.generateDDLForAddColumn(to.Name, toCol)
	if err != nil {
		return DDL{}, err
	}
	ddl.AppendDDL(add)
	for _, i := range indexes {
		recreated := *i
		recreated.Storing = g.storingKeptIn(i.Storing, to)
		ddl.Append(&recreated)
	}
	for _, i := range searchIndexes {
		ddl.Append(i)
	}
	for _, i := range vectorIndexes {
		recreated := *i
		recreated.Storing = g.storingKeptIn(i.Storing, to)
		ddl.Append(&recreated)
	}
	return ddl, nil
}

// storingKeptIn drops the stored columns the target table no longer has, which Spanner would
// otherwise refuse to drop while the recreated index still stores them.
func (g *Generator) storingKeptIn(storing *ast.Storing, to *Table) *ast.Storing {
	columns := slices.DeleteFunc(slices.Clone(storingColumns(storing)), func(c *ast.Ident) bool {
		_, kept := g.findColumnByName(to.Columns, identsToComparable(c))
		return !kept
	})
	if len(columns) == 0 {
		return nil
	}
	return &ast.Storing{Columns: columns}
}

func (g *Generator) generateDDLForDropIndex(from, to *Table) DDL {
	ddl := DDL{}

	for _, toIndex := range to.indexes {
		fromIndex, exists := g.findIndexByName(from.indexes, identsToComparable(toIndex.Name.Idents...))

		if exists && !g.indexEqualIgnoringStoring(fromIndex, toIndex) {
			ddl.Append(&ast.DropIndex{Name: fromIndex.Name})
			g.droppedIndexes = append(g.droppedIndexes, identsToComparable(fromIndex.Name.Idents...))
		}
	}
	for _, fromIndex := range from.indexes {
		if _, exists := g.findIndexByName(to.indexes, identsToComparable(fromIndex.Name.Idents...)); !exists {
			ddl.Append(&ast.DropIndex{Name: fromIndex.Name})
			g.droppedIndexes = append(g.droppedIndexes, identsToComparable(fromIndex.Name.Idents...))
		}
	}

	for _, toIndex := range to.searchIndexes {
		fromIndex, exists := g.findSearchIndexByName(from.searchIndexes, identsToComparable(toIndex.Name.Idents...))

		if exists && !g.searchIndexEqual(fromIndex, toIndex) {
			ddl.Append(&ast.DropSearchIndex{Name: fromIndex.Name})
			g.droppedIndexes = append(g.droppedIndexes, identsToComparable(fromIndex.Name.Idents...))
		}
	}
	for _, fromIndex := range from.searchIndexes {
		if _, exists := g.findSearchIndexByName(to.searchIndexes, identsToComparable(fromIndex.Name.Idents...)); !exists {
			ddl.Append(&ast.DropSearchIndex{Name: fromIndex.Name})
			g.droppedIndexes = append(g.droppedIndexes, identsToComparable(fromIndex.Name.Idents...))
		}
	}

	for _, toIndex := range to.vectorIndexes {
		fromIndex, exists := g.findVectorIndexByName(from.vectorIndexes, identsToComparable(toIndex.Name))

		if exists && !g.vectorIndexEqualIgnoringStoring(fromIndex, toIndex) {
			ddl.Append(&ast.DropVectorIndex{Name: fromIndex.Name})
			g.droppedIndexes = append(g.droppedIndexes, identsToComparable(fromIndex.Name))
		}
	}
	for _, fromIndex := range from.vectorIndexes {
		if _, exists := g.findVectorIndexByName(to.vectorIndexes, identsToComparable(fromIndex.Name)); !exists {
			ddl.Append(&ast.DropVectorIndex{Name: fromIndex.Name})
			g.droppedIndexes = append(g.droppedIndexes, identsToComparable(fromIndex.Name))
		}
	}

	return ddl
}

func (g *Generator) generateDDLForCreateIndex(from, to *Table) DDL {
	ddl := DDL{}

	for _, toIndex := range to.indexes {
		fromIndex, exists := g.findIndexByName(from.indexes, identsToComparable(toIndex.Name.Idents...))

		if !exists || !g.indexEqualIgnoringStoring(fromIndex, toIndex) {
			ddl.Append(toIndex)
		}
	}

	for _, toIndex := range to.searchIndexes {
		fromIndex, exists := g.findSearchIndexByName(from.searchIndexes, identsToComparable(toIndex.Name.Idents...))

		if !exists || !g.searchIndexEqual(fromIndex, toIndex) {
			ddl.Append(toIndex)
		}
	}

	for _, toIndex := range to.vectorIndexes {
		fromIndex, exists := g.findVectorIndexByName(from.vectorIndexes, identsToComparable(toIndex.Name))

		if !exists || !g.vectorIndexEqualIgnoringStoring(fromIndex, toIndex) {
			ddl.Append(toIndex)
		}
	}

	return ddl
}

// generateDDLForDropStoredColumn drops stored columns whose table column is dropped; Spanner
// rejects dropping a column that an index still stores.
func (g *Generator) generateDDLForDropStoredColumn(from, to *Table) DDL {
	ddl := DDL{}

	for _, fromIndex := range from.indexes {
		toIndex, exists := g.findIndexByName(to.indexes, identsToComparable(fromIndex.Name.Idents...))
		if !exists || !g.indexEqualIgnoringStoring(fromIndex, toIndex) || fromIndex.Storing == nil {
			continue
		}
		for _, c := range fromIndex.Storing.Columns {
			if _, kept := g.findColumnByName(to.Columns, identsToComparable(c)); !kept {
				ddl.Append(&ast.AlterIndex{Name: toIndex.Name, IndexAlteration: &ast.DropStoredColumn{Name: c}})
			}
		}
	}
	for _, fromIndex := range from.vectorIndexes {
		toIndex, exists := g.findVectorIndexByName(to.vectorIndexes, identsToComparable(fromIndex.Name))
		if !exists || !g.vectorIndexEqualIgnoringStoring(fromIndex, toIndex) {
			continue
		}
		for _, c := range storingColumns(fromIndex.Storing) {
			if _, kept := g.findColumnByName(to.Columns, identsToComparable(c)); !kept {
				ddl.Append(&ast.AlterVectorIndex{Name: &ast.Path{Idents: []*ast.Ident{toIndex.Name}}, Alteration: &ast.DropStoredColumn{Name: c}})
			}
		}
	}

	return ddl
}

func (g *Generator) generateDDLForAlterIndex(from, to *Table) DDL {
	ddl := DDL{}

	for _, toIndex := range to.indexes {
		if toIndex.Storing == nil {
			continue
		}

		fromIndex, exists := g.findIndexByName(from.indexes, identsToComparable(toIndex.Name.Idents...))
		if !exists || !g.indexEqualIgnoringStoring(fromIndex, toIndex) {
			continue
		}

		for _, toIndexStoringColumn := range toIndex.Storing.Columns {
			if fromIndex.Storing != nil {
				if _, exists := g.findIdentByName(fromIndex.Storing.Columns, identsToComparable(toIndexStoringColumn)); exists {
					continue
				}
			}
			ddl.Append(&ast.AlterIndex{
				Name: toIndex.Name,
				IndexAlteration: &ast.AddStoredColumn{
					Name: toIndexStoringColumn,
				},
			})
		}
	}

	for _, fromIndex := range from.indexes {
		if fromIndex.Storing == nil {
			continue
		}

		toIndex, exists := g.findIndexByName(to.indexes, identsToComparable(fromIndex.Name.Idents...))
		if !exists || !g.indexEqualIgnoringStoring(fromIndex, toIndex) {
			continue
		}

		for _, fromIndexStoringColumn := range fromIndex.Storing.Columns {
			if toIndex.Storing != nil {
				if _, exists := g.findIdentByName(toIndex.Storing.Columns, identsToComparable(fromIndexStoringColumn)); exists {
					continue
				}
			}
			if _, kept := g.findColumnByName(to.Columns, identsToComparable(fromIndexStoringColumn)); !kept {
				continue
			}
			ddl.Append(&ast.AlterIndex{
				Name: toIndex.Name,
				IndexAlteration: &ast.DropStoredColumn{
					Name: fromIndexStoringColumn,
				},
			})
		}
	}

	return ddl
}

func (g *Generator) generateDDLForAlterVectorIndex(from, to *Table) DDL {
	ddl := DDL{}

	for _, toIndex := range to.vectorIndexes {
		fromIndex, exists := g.findVectorIndexByName(from.vectorIndexes, identsToComparable(toIndex.Name))
		if !exists || !g.vectorIndexEqualIgnoringStoring(fromIndex, toIndex) {
			continue
		}
		name := &ast.Path{Idents: []*ast.Ident{toIndex.Name}}
		for _, c := range storingColumns(toIndex.Storing) {
			if _, ok := g.findIdentByName(storingColumns(fromIndex.Storing), identsToComparable(c)); !ok {
				ddl.Append(&ast.AlterVectorIndex{Name: name, Alteration: &ast.AddStoredColumn{Name: c}})
			}
		}
		for _, c := range storingColumns(fromIndex.Storing) {
			_, stillStored := g.findIdentByName(storingColumns(toIndex.Storing), identsToComparable(c))
			_, kept := g.findColumnByName(to.Columns, identsToComparable(c))
			if !stillStored && kept {
				ddl.Append(&ast.AlterVectorIndex{Name: name, Alteration: &ast.DropStoredColumn{Name: c}})
			}
		}
		if fromV, toV := g.optionsToMap(fromIndex.Options)[disableSearch], g.optionsToMap(toIndex.Options)[disableSearch]; fromV != toV {
			var value ast.Expr = &ast.NullLiteral{}
			if r, ok := findFirst(toIndex.Options.Records, func(r *ast.OptionsDef) bool { return r.Name.Name == disableSearch }); ok {
				value = r.Value
			}
			ddl.Append(&ast.AlterVectorIndex{Name: name, Alteration: &ast.VectorIndexSetOptions{
				Options: &ast.Options{Records: []*ast.OptionsDef{{Name: &ast.Ident{Name: disableSearch}, Value: value}}},
			}})
		}
	}

	return ddl
}

func storingColumns(s *ast.Storing) []*ast.Ident {
	if s == nil {
		return nil
	}
	return s.Columns
}

func (g *Generator) generateDDLForCreateChangeStream(from *Database, to *Table) DDL {
	ddl := DDL{}

	for _, cs := range to.changeStreams {
		g.addWillCreateOrAlterChangeStream(cs)
	}
	return ddl
}

func (g *Generator) generateDDLForDropNamedConstraint(table *ast.Path, constraint *ast.TableConstraint) DDL {
	ddl := DDL{}

	if constraint.Name == nil {
		return ddl
	}

	if g.isDroppedConstraint(table, constraint) {
		return ddl
	}
	g.droppedConstraints = append(g.droppedConstraints, droppedConstraint{table: identsToComparable(table.Idents...), constraint: constraint})

	ddl.Append(&ast.AlterTable{
		Name: table,
		TableAlteration: &ast.DropConstraint{
			Name: constraint.Name,
		},
	})

	return ddl
}

func (g *Generator) isDroppedTable(name string) bool {
	return slices.Contains(g.droppedTables, name)
}

func (g *Generator) isDroppedIndex(name string) bool {
	return slices.Contains(g.droppedIndexes, name)
}

func (g *Generator) isDroppedConstraint(table *ast.Path, constraint *ast.TableConstraint) bool {
	name := identsToComparable(table.Idents...)
	for _, c := range g.droppedConstraints {
		if strings.EqualFold(c.table, name) && g.constraintEqual(c.constraint, constraint) {
			return true
		}
	}
	return false
}

func (g *Generator) isDroppedChangeStream(name string) bool {
	return slices.Contains(g.droppedChangeStreams, name)
}

// markGrantDropped records grant as dropped and reports whether it was not recorded yet.
func (g *Generator) markGrantDropped(grant *Grant) bool {
	if g.isDroppedGrant(grant) {
		return false
	}
	g.droppedGrants = append(g.droppedGrants, grant)
	return true
}

func (g *Generator) markGrantsDropped(grants []*Grant) {
	for _, grant := range grants {
		g.markGrantDropped(grant)
	}
}

func (g *Generator) isDroppedGrant(grant *Grant) bool {
	for _, dg := range g.droppedGrants {
		if equalGrant(dg, grant) {
			return true
		}
	}
	return false
}

func (g *Generator) interleaveEqual(x, y *Table) bool {
	return cmp.Equal(x.Cluster, y.Cluster,
		ignorePos,
		cmp.Comparer(func(a, b *ast.Cluster) bool {
			if a == nil || b == nil {
				return a == b
			}
			aVal, bVal := *a, *b
			aVal.OnDelete = stdcmp.Or(a.OnDelete, ast.OnDeleteNoAction)
			bVal.OnDelete = stdcmp.Or(b.OnDelete, ast.OnDeleteNoAction)
			return cmp.Equal(aVal, bVal, ignorePos)
		}),
	)
}

func (g *Generator) primaryKeyEqual(x, y *Table) bool {
	xPrimaryKeys := primaryKeysForComparison(x)
	yPrimaryKeys := primaryKeysForComparison(y)
	if !g.indexKeysEqual(xPrimaryKeys, yPrimaryKeys) {
		return false
	}
	for _, pk := range yPrimaryKeys {
		xCol, exists := g.findColumnByName(x.Columns, identsToComparable(pk.Name))
		if !exists {
			return false
		}
		yCol, exists := g.findColumnByName(y.Columns, identsToComparable(pk.Name))
		if !exists {
			return false
		}
		if !g.columnDefEqual(xCol, yCol) {
			return false
		}
	}
	return true
}

var (
	ignorePos = cmpopts.IgnoreTypes(token.Pos(0))

	indexKeyComparer = cmp.Comparer(func(a, b *ast.IndexKey) bool {
		aVal, bVal := *a, *b
		aVal.Dir = stdcmp.Or(a.Dir, ast.DirectionAsc)
		bVal.Dir = stdcmp.Or(b.Dir, ast.DirectionAsc)
		return cmp.Equal(aVal, bVal, ignorePos)
	})

	orderByItemComparer = cmp.Comparer(func(a, b *ast.OrderByItem) bool {
		aVal, bVal := *a, *b
		aVal.Dir = stdcmp.Or(a.Dir, ast.DirectionAsc)
		bVal.Dir = stdcmp.Or(b.Dir, ast.DirectionAsc)
		return cmp.Equal(aVal, bVal, ignorePos)
	})
)

func (g *Generator) indexKeysEqual(x, y []*ast.IndexKey) bool {
	return cmp.Equal(x, y, ignorePos, indexKeyComparer)
}

// primaryKeysForComparison folds the inline `id INT64 PRIMARY KEY` column-def
// form into the table-level `PRIMARY KEY (id)` form that Spanner's GetDdl
// always returns, so the two surface forms compare as equivalent.
func primaryKeysForComparison(table *Table) []*ast.IndexKey {
	if len(table.PrimaryKeys) > 0 {
		return table.PrimaryKeys
	}
	var keys []*ast.IndexKey
	for _, col := range table.Columns {
		if !col.PrimaryKey {
			continue
		}
		keys = append(keys, &ast.IndexKey{Name: col.Name})
	}
	return keys
}

func (g *Generator) columnDefEqual(x, y *ast.ColumnDef) bool {
	return cmp.Equal(x, y,
		ignorePos,
		cmpopts.IgnoreFields(ast.ColumnDef{}, "PrimaryKey", "Options"),
		cmp.Comparer(func(x, y *ast.Ident) bool {
			return strings.EqualFold(x.Name, y.Name)
		}),
		cmp.Comparer(func(x, y *ast.TimestampLiteral) bool {
			return x.Value.SQL() == y.Value.SQL()
		}),
	)
}

func (g *Generator) columnTypeEqual(x, y *ast.ColumnDef) bool {
	return cmp.Equal(x.Type, y.Type,
		ignorePos,
		cmp.Comparer(func(x, y *ast.SizedSchemaType) bool {
			return x.Name == y.Name
		}),
	)
}

func (g *Generator) constraintEqual(x, y *ast.TableConstraint) bool {
	return cmp.Equal(x, y,
		ignorePos,
		cmp.Comparer(func(a, b *ast.ForeignKey) bool {
			aVal, bVal := *a, *b
			aVal.OnDelete = stdcmp.Or(a.OnDelete, ast.OnDeleteNoAction)
			bVal.OnDelete = stdcmp.Or(b.OnDelete, ast.OnDeleteNoAction)
			aVal.Enforcement = stdcmp.Or(a.Enforcement, ast.Enforced)
			bVal.Enforcement = stdcmp.Or(b.Enforcement, ast.Enforced)
			return cmp.Equal(aVal, bVal, ignorePos)
		}),
	)
}

func (g *Generator) indexEqualIgnoringStoring(x, y *ast.CreateIndex) bool {
	return cmp.Equal(x, y,
		ignorePos,
		cmpopts.IgnoreTypes(&ast.Storing{}),
		cmpopts.IgnoreFields(ast.CreateIndex{}, "IfNotExists"),
		indexKeyComparer,
		g.indexOptionsComparer(),
	)
}

// disableSearch is the one vector index option ALTER VECTOR INDEX can change without a rebuild.
const disableSearch = "disable_search"

// vectorIndexEqualIgnoringStoring also ignores disable_search; both are altered in place.
func (g *Generator) vectorIndexEqualIgnoringStoring(x, y *ast.CreateVectorIndex) bool {
	return cmp.Equal(x, y,
		ignorePos,
		cmpopts.IgnoreTypes(&ast.Storing{}),
		cmpopts.IgnoreFields(ast.CreateVectorIndex{}, "IfNotExists", "Options"),
	) && g.optionsEqual(withoutOption(x.Options, disableSearch), withoutOption(y.Options, disableSearch))
}

func withoutOption(options *ast.Options, name string) *ast.Options {
	if options == nil {
		return nil
	}
	return &ast.Options{Records: slices.DeleteFunc(slices.Clone(options.Records), func(r *ast.OptionsDef) bool { return r.Name.Name == name })}
}

func (g *Generator) searchIndexEqual(x, y *ast.CreateSearchIndex) bool {
	return cmp.Equal(x, y, ignorePos, orderByItemComparer, g.indexOptionsComparer())
}

// indexOptionsComparer treats an index in the default locality group as one with no locality group.
func (g *Generator) indexOptionsComparer() cmp.Option {
	withoutDefault := func(o *ast.Options) *ast.Options {
		if o == nil {
			return nil
		}
		return &ast.Options{Records: slices.DeleteFunc(slices.Clone(o.Records), func(r *ast.OptionsDef) bool {
			return r.Name.Name == localityGroupOption && strings.EqualFold(localityGroupOf(&ast.Options{Records: []*ast.OptionsDef{r}}), defaultLocalityGroup)
		})}
	}
	return cmp.Comparer(func(x, y *ast.Options) bool { return g.optionsEqual(withoutDefault(x), withoutDefault(y)) })
}

func (g *Generator) changeStreamForEqual(x, y ast.ChangeStreamFor) bool {
	return cmp.Equal(x, y,
		ignorePos,
		cmpopts.SortSlices(func(a, b *ast.ChangeStreamForTable) bool {
			return identsToComparable(a.TableName) < identsToComparable(b.TableName)
		}),
		cmpopts.SortSlices(func(a, b *ast.Ident) bool {
			return identsToComparable(a) < identsToComparable(b)
		}),
	)
}

func (g *Generator) columnDefaultExprEqual(x, y ast.ColumnDefaultSemantics) bool {
	return cmp.Equal(x, y, ignorePos)
}

func (g *Generator) createRowDeletionPolicyEqual(x, y *ast.CreateRowDeletionPolicy) bool {
	return cmp.Equal(x, y, ignorePos)
}

func (g *Generator) optionsEqual(x, y *ast.Options) bool {
	return cmp.Equal(g.optionsToMap(x), g.optionsToMap(y))
}

func (g *Generator) optionsToMap(options *ast.Options) map[string]string {
	m := map[string]string{}
	if options == nil {
		return m
	}
	for _, o := range options.Records {
		// Spanner reads an option reset to its default back as `= NULL`.
		if _, isNull := o.Value.(*ast.NullLiteral); !isNull {
			m[o.Name.Name] = o.Value.SQL()
		}
	}
	return m
}

func (g *Generator) optionsValueEqual(x, y *ast.Options, name string) bool {
	xv := optionsValueFromName(x, name)
	yv := optionsValueFromName(y, name)
	return cmp.Equal(xv, yv, ignorePos)
}

func optionsValueFromName(options *ast.Options, name string) *ast.Expr {
	if options == nil {
		return nil
	}
	for _, o := range options.Records {
		if o.Name.Name == name {
			return &o.Value
		}
	}
	return nil
}

func defaultByScalarTypeName(t ast.ScalarTypeName) ast.Expr {
	switch t {
	case ast.BoolTypeName:
		return &ast.BoolLiteral{Value: false}
	case ast.Int64TypeName:
		return &ast.IntLiteral{Value: "0"}
	case ast.Float32TypeName, ast.Float64TypeName:
		return &ast.FloatLiteral{Value: "0"}
	case ast.StringTypeName:
		return &ast.StringLiteral{Value: ""}
	case ast.BytesTypeName:
		return &ast.BytesLiteral{Value: nil}
	case ast.DateTypeName:
		return &ast.DateLiteral{Value: &ast.StringLiteral{Value: "0001-01-01"}}
	case ast.TimestampTypeName:
		return &ast.TimestampLiteral{Value: &ast.StringLiteral{Value: "0001-01-01T00:00:00Z"}}
	case ast.NumericTypeName:
		return &ast.NumericLiteral{Value: &ast.StringLiteral{Value: "0"}}
	case ast.JSONTypeName:
		return &ast.JSONLiteral{Value: &ast.StringLiteral{Value: "{}"}}
	case ast.TokenListTypeName:
		return &ast.BytesLiteral{Value: nil}
	default:
		panic("not implemented")
	}
}

func identsToComparable(is ...*ast.Ident) string {
	var b strings.Builder
	for i, n := range is {
		if i != 0 {
			b.WriteByte('.')
		}
		b.WriteString(n.Name)
	}
	return b.String()
}

func isUUID(t *ast.NamedType) bool {
	return len(t.Path) == 1 && strings.EqualFold(t.Path[0].Name, "UUID")
}

func zeroUUID(t *ast.NamedType) ast.Expr {
	return &ast.CastExpr{Expr: &ast.StringLiteral{Value: "00000000-0000-0000-0000-000000000000"}, Type: t}
}

func withZeroDefault(col *ast.ColumnDef) (*ast.ColumnDef, error) {
	c := *col
	switch t := col.Type.(type) {
	case *ast.ArraySchemaType:
		c.DefaultSemantics = &ast.ColumnDefaultExpr{Expr: &ast.ArrayLiteral{Values: nil}}
	case *ast.ScalarSchemaType:
		c.DefaultSemantics = &ast.ColumnDefaultExpr{Expr: defaultByScalarTypeName(t.Name)}
	case *ast.SizedSchemaType:
		c.DefaultSemantics = &ast.ColumnDefaultExpr{Expr: defaultByScalarTypeName(t.Name)}
	case *ast.NamedType:
		if !isUUID(t) {
			return nil, fmt.Errorf("type %s needs an explicit DEFAULT", t.SQL())
		}
		c.DefaultSemantics = &ast.ColumnDefaultExpr{Expr: zeroUUID(t)}
	}
	return &c, nil
}

func findFirst[T any](items []T, match func(T) bool) (T, bool) {
	for _, item := range items {
		if match(item) {
			return item, true
		}
	}
	var zero T
	return zero, false
}

func (g *Generator) findTableByName(tables []*Table, name string) (*Table, bool) {
	return findFirst(tables, func(t *Table) bool {
		return strings.EqualFold(identsToComparable(t.Name.Idents...), name)
	})
}

func (g *Generator) findColumnByName(cols []*ast.ColumnDef, name string) (*ast.ColumnDef, bool) {
	return findFirst(cols, func(c *ast.ColumnDef) bool {
		return strings.EqualFold(identsToComparable(c.Name), name)
	})
}

func (g *Generator) findIdentByName(idents []*ast.Ident, name string) (*ast.Ident, bool) {
	return findFirst(idents, func(i *ast.Ident) bool {
		return strings.EqualFold(identsToComparable(i), name)
	})
}

func (g *Generator) findNamedConstraint(constraints []*ast.TableConstraint, name string) (*ast.TableConstraint, bool) {
	if name == "" {
		return nil, false
	}
	return findFirst(constraints, func(c *ast.TableConstraint) bool {
		return identsToComparable(c.Name) == name
	})
}

func (g *Generator) findUnnamedConstraint(constraints []*ast.TableConstraint, item *ast.TableConstraint) (*ast.TableConstraint, bool) {
	return findFirst(constraints, func(c *ast.TableConstraint) bool {
		return g.constraintEqual(c, item)
	})
}

func (g *Generator) findIndexByName(indexes []*ast.CreateIndex, name string) (*ast.CreateIndex, bool) {
	return findFirst(indexes, func(i *ast.CreateIndex) bool {
		return identsToComparable(i.Name.Idents...) == name
	})
}

func (g *Generator) findIndexByColumn(indexes []*ast.CreateIndex, column string) []*ast.CreateIndex {
	result := []*ast.CreateIndex{}
	for _, i := range indexes {
		if indexReferencesColumn(i, column) {
			result = append(result, i)
		}
	}
	return result
}

func indexReferencesColumn(i *ast.CreateIndex, column string) bool {
	for _, k := range i.Keys {
		if identsToComparable(k.Name) == column {
			return true
		}
	}
	if i.Storing != nil {
		for _, c := range i.Storing.Columns {
			if identsToComparable(c) == column {
				return true
			}
		}
	}
	return false
}

func (g *Generator) findSearchIndexByName(indexes []*ast.CreateSearchIndex, name string) (*ast.CreateSearchIndex, bool) {
	return findFirst(indexes, func(i *ast.CreateSearchIndex) bool {
		return identsToComparable(i.Name.Idents...) == name
	})
}

func (g *Generator) findVectorIndexByName(indexes []*ast.CreateVectorIndex, name string) (*ast.CreateVectorIndex, bool) {
	return findFirst(indexes, func(i *ast.CreateVectorIndex) bool {
		return identsToComparable(i.Name) == name
	})
}

func (g *Generator) findSearchIndexByColumn(indexes []*ast.CreateSearchIndex, column string) []*ast.CreateSearchIndex {
	result := []*ast.CreateSearchIndex{}
	for _, i := range indexes {
		if searchIndexReferencesColumn(i, column) {
			result = append(result, i)
		}
	}
	return result
}

func foreignKeyReferencesColumn(constraintHost *Table, tableName *ast.Path, column *ast.Ident, constraint *ast.TableConstraint) bool {
	fk, ok := constraint.Constraint.(*ast.ForeignKey)
	if !ok {
		return false
	}
	columnKey := identsToComparable(column)
	if identsToComparable(constraintHost.Name.Idents...) == identsToComparable(tableName.Idents...) {
		for _, c := range fk.Columns {
			if identsToComparable(c) == columnKey {
				return true
			}
		}
	}
	if identsToComparable(fk.ReferenceTable.Idents...) == identsToComparable(tableName.Idents...) {
		for _, refColumn := range fk.ReferenceColumns {
			if identsToComparable(refColumn) == columnKey {
				return true
			}
		}
	}
	return false
}

func checkReferencesColumn(constraintHost *Table, tableName *ast.Path, column *ast.Ident, constraint *ast.TableConstraint) bool {
	check, ok := constraint.Constraint.(*ast.Check)
	if !ok || identsToComparable(constraintHost.Name.Idents...) != identsToComparable(tableName.Idents...) {
		return false
	}
	return exprReferencesColumn(check.Expr, column)
}

func exprReferencesColumn(expr ast.Expr, column *ast.Ident) bool {
	// Any identifier with the column's name counts, so a function or field name may cause a needless re-add but never a missed one.
	for node := range ast.Preorder(expr) {
		if id, ok := node.(*ast.Ident); ok && strings.EqualFold(id.Name, column.Name) {
			return true
		}
	}
	return false
}

func vectorIndexReferencesColumn(i *ast.CreateVectorIndex, column string) bool {
	return identsToComparable(i.ColumnName) == column ||
		slices.ContainsFunc(storingColumns(i.Storing), func(c *ast.Ident) bool { return identsToComparable(c) == column })
}

func searchIndexReferencesColumn(i *ast.CreateSearchIndex, column string) bool {
	for _, c := range i.TokenListPart {
		if identsToComparable(c) == column {
			return true
		}
	}
	if i.Storing != nil {
		for _, c := range i.Storing.Columns {
			if identsToComparable(c) == column {
				return true
			}
		}
	}
	for _, c := range i.PartitionColumns {
		if identsToComparable(c) == column {
			return true
		}
	}
	if i.OrderBy != nil {
		for _, item := range i.OrderBy.Items {
			if id, ok := item.Expr.(*ast.Ident); ok && identsToComparable(id) == column {
				return true
			}
		}
	}
	return false
}

func (g *Generator) generateDDLForDropNamedConstraintsMatchingPredicate(predicate func(table *Table, constraint *ast.TableConstraint) bool) DDL {
	ddl := DDL{}

	for _, table := range g.from.tables {
		for _, constraint := range table.TableConstraints {
			if predicate(table, constraint) {
				ddl.AppendDDL(g.generateDDLForDropNamedConstraint(table.Name, constraint))
			}
		}
	}

	return ddl
}

func (g *Generator) findChangeStreamByName(database *Database, name string) (*ChangeStream, bool) {
	match := func(cs *ChangeStream) bool { return identsToComparable(cs.Name) == name }
	if cs, ok := findFirst(database.changeStreams, match); ok {
		return cs, true
	}
	for _, table := range database.tables {
		if cs, ok := findFirst(table.changeStreams, match); ok {
			return cs, true
		}
	}
	return nil, false
}

func (g *Generator) generateDDLForAlterChangeStream(from, to *ChangeStream) DDL {
	ddl := DDL{}
	fromType, toType := from.Type(), to.Type()
	switch {
	case fromType == ChangeStreamTypeAll && toType == ChangeStreamTypeTables:
		ddl.Append(&ast.AlterChangeStream{Name: to.Name, ChangeStreamAlteration: &ast.ChangeStreamSetFor{For: to.For}})
	case fromType == ChangeStreamTypeAll && toType == ChangeStreamTypeNone:
		ddl.Append(&ast.AlterChangeStream{Name: to.Name, ChangeStreamAlteration: &ast.ChangeStreamDropForAll{}})
	case fromType == ChangeStreamTypeTables && toType == ChangeStreamTypeAll:
		ddl.Append(&ast.AlterChangeStream{Name: to.Name, ChangeStreamAlteration: &ast.ChangeStreamSetFor{For: to.For}})
	case fromType == ChangeStreamTypeTables && toType == ChangeStreamTypeNone:
		ddl.Append(&ast.AlterChangeStream{Name: to.Name, ChangeStreamAlteration: &ast.ChangeStreamDropForAll{}})
	case fromType == ChangeStreamTypeTables && toType == ChangeStreamTypeTables:
		if !g.changeStreamForEqual(from.For, to.For) {
			ddl.Append(&ast.AlterChangeStream{Name: to.Name, ChangeStreamAlteration: &ast.ChangeStreamSetFor{For: to.For}})
		}
	case fromType == ChangeStreamTypeNone && toType == ChangeStreamTypeAll:
		ddl.Append(&ast.AlterChangeStream{Name: to.Name, ChangeStreamAlteration: &ast.ChangeStreamSetFor{For: to.For}})
	case fromType == ChangeStreamTypeNone && toType == ChangeStreamTypeTables:
		ddl.Append(&ast.AlterChangeStream{Name: to.Name, ChangeStreamAlteration: &ast.ChangeStreamSetFor{For: to.For}})
	}
	if !g.optionsEqual(from.Options, to.Options) {
		ddl.Append(&ast.AlterChangeStream{Name: to.Name, ChangeStreamAlteration: &ast.ChangeStreamSetOptions{Options: optionsForSet(from.Options, to.Options)}})
	}
	return ddl
}

// optionsForSet returns the options for SET OPTIONS that turn from into to.
// It resets the options that exist only in from to null.
func optionsForSet(from, to *ast.Options) *ast.Options {
	options := &ast.Options{}
	if to != nil {
		options.Records = append(options.Records, to.Records...)
	}
	if from != nil {
		for _, r := range from.Records {
			if optionsValueFromName(to, r.Name.Name) == nil {
				options.Records = append(options.Records, &ast.OptionsDef{
					Name:  &ast.Ident{Name: r.Name.Name},
					Value: &ast.NullLiteral{},
				})
			}
		}
	}
	return options
}

func (g *Generator) generateDDLForDropChangeStream(changeStream *ChangeStream) DDL {
	ddl := DDL{}

	g.markGrantsDropped(g.from.grantsOnChangeStream(changeStream))

	ddl.Append(&ast.DropChangeStream{Name: changeStream.Name})
	return ddl
}

func (g *Generator) findQueueByName(queues []*Queue, name string) (*Queue, bool) {
	return findFirst(queues, func(q *Queue) bool {
		return strings.EqualFold(identsToComparable(q.Name.Idents...), name)
	})
}

func (g *Generator) isDroppedQueue(name string) bool {
	return slices.ContainsFunc(g.droppedQueues, func(q string) bool { return strings.EqualFold(q, name) })
}

func (g *Generator) queueColumnsEqual(x, y *Queue) bool {
	if !g.indexKeysEqual(x.PrimaryKeys, y.PrimaryKeys) {
		return false
	}
	if len(x.Columns) != len(y.Columns) {
		return false
	}
	for _, yCol := range y.Columns {
		xCol, exists := g.findColumnByName(x.Columns, identsToComparable(yCol.Name))
		if !exists || isColHidden(xCol) != isColHidden(yCol) || !g.columnDefEqual(xCol, yCol) || !g.optionsEqual(xCol.Options, yCol.Options) {
			return false
		}
	}
	return true
}

// queueInterleaveAlterable reports whether ALTER QUEUE can turn the interleave x into y.
// ALTER QUEUE can change PARENT and ON DELETE, but not the parent table itself.
func queueInterleaveAlterable(x, y *ast.Cluster) bool {
	if x == nil || y == nil {
		return x == y
	}
	return strings.EqualFold(identsToComparable(x.TableName.Idents...), identsToComparable(y.TableName.Idents...))
}

func queueOnDelete(c *ast.Cluster) ast.OnDeleteAction {
	if !c.Enforced {
		return ""
	}
	if c.OnDelete == "" {
		return ast.OnDeleteNoAction
	}
	return c.OnDelete
}

func (g *Generator) generateDDLForAlterQueue(from, to *Queue) DDL {
	ddl := DDL{}

	if x, y := from.Cluster, to.Cluster; x != nil && y != nil {
		switch {
		case x.Enforced == y.Enforced && queueOnDelete(x) == queueOnDelete(y):
		case x.Enforced && y.Enforced:
			ddl.Append(&ast.AlterQueue{Name: to.Name, QueueAlteration: &ast.SetOnDelete{OnDelete: queueOnDelete(y)}})
		case !x.Enforced && queueOnDelete(y) == ast.OnDeleteCascade:
			// Spanner cannot change INTERLEAVE IN to INTERLEAVE IN PARENT ON DELETE CASCADE in one step.
			ddl.Append(&ast.AlterQueue{Name: to.Name, QueueAlteration: &ast.SetInterleaveIn{TableName: y.TableName, Enforced: true, OnDelete: ast.OnDeleteNoAction}})
			ddl.Append(&ast.AlterQueue{Name: to.Name, QueueAlteration: &ast.SetOnDelete{OnDelete: ast.OnDeleteCascade}})
		default:
			ddl.Append(&ast.AlterQueue{Name: to.Name, QueueAlteration: &ast.SetInterleaveIn{TableName: y.TableName, Enforced: y.Enforced, OnDelete: y.OnDelete}})
		}
	}

	if alteration := g.rowDeletionPolicyAlteration(from.RowDeletionPolicy, to.RowDeletionPolicy); alteration != nil {
		ddl.Append(markRowDeletionPolicy(&ast.AlterQueue{Name: to.Name, QueueAlteration: alteration}, alteration))
	}

	if !g.optionsEqual(from.Options, to.Options) {
		ddl.Append(&ast.AlterQueue{Name: to.Name, QueueAlteration: &ast.QueueSetOptions{Options: optionsForSet(from.Options, to.Options)}})
	}
	return ddl
}

func (g *Generator) generateDDLForDropQueue(queue *Queue) DDL {
	ddl := DDL{}

	// Dropping the queue also removes its grants, so record them as dropped without REVOKE.
	g.markGrantsDropped(g.from.grantsOnQueue(queue))

	ddl.Append(DataLoss{&ast.DropQueue{Name: queue.Name}, "drops queue " + queue.Name.SQL()})
	g.droppedQueues = append(g.droppedQueues, identsToComparable(queue.Name.Idents...))
	return ddl
}

func (g *Generator) findSequenceByName(sequences []*Sequence, name string) (*Sequence, bool) {
	return findFirst(sequences, func(s *Sequence) bool {
		return strings.EqualFold(identsToComparable(s.Name.Idents...), name)
	})
}

func (g *Generator) generateDDLForDropSequence(sequence *Sequence) DDL {
	ddl := DDL{}
	g.markGrantsDropped(g.from.grantsOnSequence(sequence))
	ddl.Append(DataLoss{&ast.DropSequence{Name: sequence.Name}, "drops sequence " + sequence.Name.SQL()})
	return ddl
}

func (g *Generator) generateDDLForAlterSequence(from, to *Sequence) DDL {
	ddl := DDL{}

	fromOptions := sequenceOptions(from.Options)
	toOptions := sequenceOptions(to.Options)

	var records []*ast.OptionsDef
	for _, name := range sequenceOptionNames(toOptions) {
		toValue := toOptions[name]
		if fromValue, ok := fromOptions[name]; ok && fromValue.SQL() == toValue.SQL() {
			continue
		}
		records = append(records, &ast.OptionsDef{Name: &ast.Ident{Name: name}, Value: toValue})
	}
	for _, name := range sequenceOptionNames(fromOptions) {
		if _, ok := toOptions[name]; ok {
			continue
		}
		// Spanner rejects a NULL sequence_kind, and a NULL start_with_counter
		// restarts the counter from 1, which can re-issue values already in use.
		if name == sequenceOptionKind || name == sequenceOptionStartWithCounter {
			continue
		}
		records = append(records, &ast.OptionsDef{Name: &ast.Ident{Name: name}, Value: &ast.NullLiteral{}})
	}
	if len(records) == 0 {
		return ddl
	}

	ddl.Append(&ast.AlterSequence{Name: to.Name, Options: &ast.Options{Records: records}})
	return ddl
}

func (g *Generator) findModelByName(models []*Model, name string) (*Model, bool) {
	return findFirst(models, func(m *Model) bool { return strings.EqualFold(m.Name.Name, name) })
}

// generateDDLForAlterModel replaces the model when its columns change; Spanner's ALTER MODEL only sets options.
func (g *Generator) generateDDLForAlterModel(from, to *Model) DDL {
	ddl := DDL{}
	if modelColumnsChanged(from, to) {
		ddl.Append(&ast.CreateModel{OrReplace: true, Name: to.Name, InputOutput: to.InputOutput, Options: to.Options})
		return ddl
	}

	fromOptions, toOptions := g.optionsToMap(from.Options), g.optionsToMap(to.Options)
	var records []*ast.OptionsDef
	if to.Options != nil {
		for _, def := range to.Options.Records {
			if value, ok := toOptions[def.Name.Name]; ok && value != fromOptions[def.Name.Name] {
				records = append(records, def)
			}
		}
	}
	if from.Options != nil {
		for _, def := range from.Options.Records {
			if _, ok := toOptions[def.Name.Name]; !ok && fromOptions[def.Name.Name] != "" {
				records = append(records, &ast.OptionsDef{Name: def.Name, Value: &ast.NullLiteral{}})
			}
		}
	}
	if len(records) > 0 {
		ddl.Append(&ast.AlterModel{Name: to.Name, Options: &ast.Options{Records: records}})
	}
	return ddl
}

func (g *Generator) findLocalityGroupByName(groups []*LocalityGroup, name string) (*LocalityGroup, bool) {
	return findFirst(groups, func(lg *LocalityGroup) bool { return strings.EqualFold(lg.Name.Name, name) })
}

func isDefaultLocalityGroup(group *LocalityGroup) bool {
	return strings.EqualFold(group.Name.Name, defaultLocalityGroup)
}

func (g *Generator) generateDDLForAlterLocalityGroup(from, to *LocalityGroup) DDL {
	ddl := DDL{}
	if !g.optionsEqual(from.Options, to.Options) {
		ddl.Append(&ast.AlterLocalityGroup{Name: to.Name, Options: optionsForSet(from.Options, to.Options)})
	}
	return ddl
}

// replacedModels returns the keys, as refersToAny matches them, of the models that CREATE OR REPLACE changes.
func (g *Generator) replacedModels() map[string]bool {
	replaced := map[string]bool{}
	for _, to := range g.to.models {
		if from, ok := g.findModelByName(g.from.models, to.Name.Name); ok && modelColumnsChanged(from, to) {
			replaced[modelKey(to.Name.Name)] = true
		}
	}
	return replaced
}

func modelKey(name string) string { return "model:" + strings.ToLower(name) }

func modelColumnsChanged(from, to *Model) bool { return inputOutputSQL(from) != inputOutputSQL(to) }

func inputOutputSQL(m *Model) string {
	if m.InputOutput == nil {
		return ""
	}
	return m.InputOutput.SQL()
}

func (g *Generator) findSchemaByName(schemas []*Schema, name string) (*Schema, bool) {
	return findFirst(schemas, func(s *Schema) bool {
		return identsToComparable(s.Name) == name
	})
}

func (g *Generator) findViewByName(views []*View, name string) (*View, bool) {
	return findFirst(views, func(v *View) bool {
		return identsToComparable(v.Name.Idents...) == name
	})
}

func (g *Generator) viewEqual(x, y *View) bool {
	return x.SecurityType == y.SecurityType && x.Query.SQL() == y.Query.SQL()
}

func sortViewsByDependency(views []*View) ([]*View, error) {
	byName := make(map[string]*View, len(views))
	for _, v := range views {
		byName[strings.ToLower(identsToComparable(v.Name.Idents...))] = v
	}
	const visiting, done = 1, 2
	state := make(map[*View]int, len(views))
	sorted := make([]*View, 0, len(views))
	var visit func(v *View, path []string) error
	visit = func(v *View, path []string) error {
		name := identsToComparable(v.Name.Idents...)
		switch state[v] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("views form a dependency cycle: %s", strings.Join(append(path, name), " -> "))
		}
		state[v] = visiting
		for node := range ast.Preorder(v.Query) {
			var ref string
			switch n := node.(type) {
			case *ast.TableName:
				ref = n.Table.Name
			case *ast.PathTableExpr:
				ref = identsToComparable(n.Path.Idents...)
			}
			if dep, ok := byName[strings.ToLower(ref)]; ok {
				if err := visit(dep, append(path, name)); err != nil {
					return err
				}
			}
		}
		state[v] = done
		sorted = append(sorted, v)
		return nil
	}
	for _, v := range views {
		if err := visit(v, nil); err != nil {
			return nil, err
		}
	}
	return sorted, nil
}

func (g *Generator) findFunctionByName(functions []*Function, name *ast.Path) (*Function, bool) {
	return findFirst(functions, func(f *Function) bool { return functionKey(f.Name) == functionKey(name) })
}

// functionKey keeps the schema case-sensitive and folds the function name, as Spanner does.
func functionKey(p *ast.Path) string {
	last := len(p.Idents) - 1
	return identsToComparable(p.Idents[:last]...) + "." + strings.ToLower(p.Idents[last].Name)
}

func lowerName(p *ast.Path) string {
	return strings.ToLower(identsToComparable(p.Idents...))
}

// changedFunctions returns the functions that exist on both sides with a different definition, and the
// subset whose arguments or return type change. Without RETURNS the type is inferred from the body, so any change counts.
func (g *Generator) changedFunctions() (changed, signature map[string]bool) {
	changed, signature = map[string]bool{}, map[string]bool{}
	for _, to := range g.to.functions {
		from, ok := g.findFunctionByName(g.from.functions, to.Name)
		if !ok || g.functionEqual(from, to) {
			continue
		}
		changed[functionKey(to.Name)] = true
		if from.ReturnType == nil || to.ReturnType == nil || g.functionSignature(from) != g.functionSignature(to) {
			signature[functionKey(to.Name)] = true
		}
	}
	return changed, signature
}

// callersOfChangedSignatures returns the current functions and views that call a function whose signature
// changes, or that select from such a view, each dependencies first.
func (g *Generator) callersOfChangedSignatures(signature map[string]bool) ([]*Function, []*View, error) {
	if len(signature) == 0 {
		return nil, nil, nil
	}
	names := maps.Clone(signature)
	sortedFunctions, err := sortFunctionsByDependency(g.from.functions)
	if err != nil {
		return nil, nil, err
	}
	var functions []*Function
	for _, f := range sortedFunctions {
		if f.Definition != nil && refersToAny(f.Definition, names) {
			names[functionKey(f.Name)] = true
			functions = append(functions, f)
		}
	}
	sortedViews, err := sortViewsByDependency(g.from.views)
	if err != nil {
		return nil, nil, err
	}
	var views []*View
	for _, v := range sortedViews {
		if refersToAny(v.Query, names) {
			names[lowerName(v.Name)] = true
			views = append(views, v)
		}
	}
	return functions, views, nil
}

func refersToAny(n ast.Node, names map[string]bool) bool {
	for node := range ast.Preorder(n) {
		var ref string
		switch n := node.(type) {
		case *ast.CallExpr:
			if names[functionKey(n.Func)] {
				return true
			}
		case *ast.ModelArg:
			ref = modelKey(n.Name.Idents[len(n.Name.Idents)-1].Name)
		case *ast.TableName:
			ref = n.Table.Name
		case *ast.PathTableExpr:
			ref = identsToComparable(n.Path.Idents...)
		}
		if names[strings.ToLower(ref)] {
			return true
		}
	}
	return false
}

// rejectTablesUsingFunctions fails when a column default, generated column or CHECK constraint calls a function
// that Spanner will not let the diff change: any change under a generated column or CHECK, a signature
// change (or recreation) under a default.
func (g *Generator) rejectTablesUsingFunctions(changed, signature, recreated map[string]bool) error {
	anyChange, signatureChange := maps.Clone(changed), maps.Clone(signature)
	maps.Copy(anyChange, recreated)
	maps.Copy(signatureChange, recreated)
	if len(anyChange) == 0 {
		return nil
	}
	fail := func(names map[string]bool, expr ast.Expr, user string) error {
		for node := range ast.Preorder(expr) {
			if call, ok := node.(*ast.CallExpr); ok && names[functionKey(call.Func)] {
				return fmt.Errorf("cannot change function %s: %s calls it", call.Func.SQL(), user)
			}
		}
		return nil
	}
	for _, t := range g.from.tables {
		table := t.Name.SQL()
		for _, col := range t.Columns {
			var err error
			switch d := col.DefaultSemantics.(type) {
			case *ast.GeneratedColumnExpr:
				err = fail(anyChange, d.Expr, "generated column "+table+"."+col.Name.SQL())
			case *ast.ColumnDefaultExpr:
				err = fail(signatureChange, d.Expr, "the default of column "+table+"."+col.Name.SQL())
				if err != nil {
					err = fmt.Errorf("%w; if that column or its table is dropped in the same diff, split the change into two migrations", err)
				}
			}
			if err != nil {
				return err
			}
		}
		for _, c := range t.TableConstraints {
			if check, ok := c.Constraint.(*ast.Check); ok {
				if err := fail(anyChange, check.Expr, "a CHECK constraint on table "+table); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (g *Generator) functionSignature(f *Function) string {
	var b strings.Builder
	for _, p := range f.Params {
		b.WriteString(p.SQL() + ",")
	}
	if f.ReturnType != nil {
		b.WriteString(f.ReturnType.SQL())
	}
	return b.String()
}

func (g *Generator) functionEqual(x, y *Function) bool {
	a, b := *x.CreateFunction, *y.CreateFunction
	a.OrReplace, b.OrReplace, b.Name = false, false, a.Name
	return a.SQL() == b.SQL()
}

func sortFunctionsByDependency(functions []*Function) ([]*Function, error) {
	byName := make(map[string]*Function, len(functions))
	for _, f := range functions {
		byName[functionKey(f.Name)] = f
	}
	const visiting, done = 1, 2
	state := make(map[*Function]int, len(functions))
	sorted := make([]*Function, 0, len(functions))
	var visit func(f *Function, path []string) error
	visit = func(f *Function, path []string) error {
		name := identsToComparable(f.Name.Idents...)
		switch state[f] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("functions form a dependency cycle: %s", strings.Join(append(path, name), " -> "))
		}
		state[f] = visiting
		if f.Definition != nil {
			for node := range ast.Preorder(f.Definition) {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					continue
				}
				if dep, ok := byName[functionKey(call.Func)]; ok {
					if err := visit(dep, append(path, name)); err != nil {
						return err
					}
				}
			}
		}
		state[f] = done
		sorted = append(sorted, f)
		return nil
	}
	for _, f := range functions {
		if err := visit(f, nil); err != nil {
			return nil, err
		}
	}
	return sorted, nil
}

func (g *Generator) generateDDLForReplaceView(from, to *View) DDL {
	ddl := DDL{}
	// CREATE OR REPLACE VIEW removes the grants on the view.
	g.markGrantsDropped(g.from.grantsOnView(from))
	ddl.Append(&ast.CreateView{Name: to.Name, Query: to.Query, SecurityType: to.SecurityType, OrReplace: true})
	return ddl
}

func (g *Generator) generateDDLForDropView(view *View) DDL {
	ddl := DDL{}
	g.markGrantsDropped(g.from.grantsOnView(view))

	ddl.Append(&ast.DropView{Name: view.Name})
	return ddl
}

func isColHidden(col *ast.ColumnDef) bool {
	return !col.Hidden.Invalid() && col.Hidden != token.Pos(0)
}

func (g *Generator) findRoleByName(roles []*Role, name string) (*Role, bool) {
	return findFirst(roles, func(r *Role) bool {
		return identsToComparable(r.Name) == name
	})
}

func (g *Generator) generateDDLForDropRole(role *Role) DDL {
	ddl := DDL{}
	grants := g.from.grantsOnRole(role)

	for _, grant := range grants {
		if !g.markGrantDropped(grant) {
			continue
		}

		// If the resource doesn't exist in "to(Database)", skip REVOKE:
		// dropping the object means the grant no longer applies. Record droppedGrants only.
		if !g.existsGrantResourceIn(grant, g.to) {
			continue
		}
		ddl.AppendDDL(g.generateDDLForRevokeAll(grant))
	}
	ddl.Append(&ast.DropRole{
		Name: role.Name,
	})
	return ddl
}

func (g *Generator) findGrant(grants []*Grant, grant *Grant) (*Grant, bool) {
	return findFirst(grants, func(grt *Grant) bool {
		return equalGrant(grt, grant)
	})
}

func (g *Generator) generateDDLForRevokeAll(grant *Grant) DDL {
	ddl := DDL{}
	if len(grant.Roles) == 0 {
		return ddl
	}

	stmt := &ast.Revoke{
		Privilege: grant.Privilege,
		Roles:     grant.Roles,
	}

	ddl.Append(stmt)
	return ddl
}

// existsGrantResourceIn returns true if any target resource of the grant exists in the given database.
// Note: this checks resource existence (table/view/change stream…), not whether the grant itself exists.
func (g *Generator) existsGrantResourceIn(grant *Grant, database *Database) bool {
	if grant == nil || grant.Grant == nil || grant.Grant.Privilege == nil {
		return false
	}
	var exists func(name string) bool
	switch grant.Grant.Privilege.(type) {
	case *ast.PrivilegeOnTable:
		exists = func(name string) bool { _, ok := g.findTableByName(database.tables, name); return ok }
	case *ast.SelectPrivilegeOnView:
		exists = func(name string) bool { _, ok := g.findViewByName(database.views, name); return ok }
	case *ast.SelectPrivilegeOnChangeStream:
		exists = func(name string) bool { _, ok := g.findChangeStreamByName(database, name); return ok }
	case *ast.PrivilegeOnQueue:
		exists = func(name string) bool { _, ok := g.findQueueByName(database.queues, name); return ok }
	case *ast.PrivilegeOnSequence:
		exists = func(name string) bool { _, ok := g.findSequenceByName(database.sequences, name); return ok }
	case *ast.ExecutePrivilegeOnTableFunction:
		// Not tracked yet; return true so REVOKE precedes DROP ROLE (safe order).
		// TODO: Check existence when table functions are modeled.
		return true
	default:
		return false
	}
	return privilegeNamesMatch(grant.Grant.Privilege, exists)
}

func equalGrant(a, b *Grant) bool {
	if a == nil || b == nil {
		return a == b
	}
	return equalAstGrant(a.Grant, b.Grant)
}

func equalAstGrant(a, b *ast.Grant) bool {
	if a == nil || b == nil {
		return a == b
	}
	if !equalPrivilege(a.Privilege, b.Privilege) {
		return false
	}
	return equalIdentLists(a.Roles, b.Roles)
}

func equalPrivilege(a, b ast.Privilege) bool {
	if a == nil || b == nil {
		return a == b
	}
	if reflect.TypeOf(a) != reflect.TypeOf(b) {
		return false
	}

	switch aTyped := a.(type) {
	case *ast.PrivilegeOnTable:
		bTyped := b.(*ast.PrivilegeOnTable)
		if !equalPathLists(aTyped.Names, bTyped.Names) {
			return false
		}
		return equalPrivilegesOnTable(aTyped.Privileges, bTyped.Privileges)
	case *ast.PrivilegeOnQueue:
		bTyped := b.(*ast.PrivilegeOnQueue)
		if !equalPathLists(aTyped.Names, bTyped.Names) {
			return false
		}
		return equalPrivilegesOnTable(aTyped.Privileges, bTyped.Privileges)
	case *ast.PrivilegeOnSequence:
		bTyped := b.(*ast.PrivilegeOnSequence)
		if !equalPathLists(aTyped.Names, bTyped.Names) {
			return false
		}
		return equalPrivilegesOnTable(aTyped.Privileges, bTyped.Privileges)
	case *ast.SelectPrivilegeOnChangeStream:
		bTyped := b.(*ast.SelectPrivilegeOnChangeStream)
		return equalPathLists(aTyped.Names, bTyped.Names)
	case *ast.SelectPrivilegeOnView:
		bTyped := b.(*ast.SelectPrivilegeOnView)
		return equalPathLists(aTyped.Names, bTyped.Names)
	case *ast.ExecutePrivilegeOnTableFunction:
		bTyped := b.(*ast.ExecutePrivilegeOnTableFunction)
		return equalPathLists(aTyped.Names, bTyped.Names)
	case *ast.RolePrivilege:
		bTyped := b.(*ast.RolePrivilege)
		return equalIdentLists(aTyped.Names, bTyped.Names)
	default:
		return fmt.Sprintf("%#v", a) == fmt.Sprintf("%#v", b)
	}
}

func equalPrivilegesOnTable(a, b []ast.TablePrivilege) bool {
	if len(a) != len(b) {
		return false
	}
	for i, tp := range a {
		if !matchTablePrivilege(tp, b[i]) {
			return false
		}
	}
	return true
}

func matchTablePrivilege(a, b ast.TablePrivilege) bool {
	if reflect.TypeOf(a) != reflect.TypeOf(b) {
		return false
	}
	switch aTyped := a.(type) {
	case *ast.SelectPrivilege:
		bTyped := b.(*ast.SelectPrivilege)
		return equalIdentLists(aTyped.Columns, bTyped.Columns)
	case *ast.InsertPrivilege:
		bTyped := b.(*ast.InsertPrivilege)
		return equalIdentLists(aTyped.Columns, bTyped.Columns)
	case *ast.UpdatePrivilege:
		bTyped := b.(*ast.UpdatePrivilege)
		return equalIdentLists(aTyped.Columns, bTyped.Columns)
	case *ast.DeletePrivilege:
		return true
	default:
		return fmt.Sprintf("%#v", a) == fmt.Sprintf("%#v", b)
	}
}

func equalIdentLists(a, b []*ast.Ident) bool {
	return identsToComparable(a...) == identsToComparable(b...)
}

func equalPathLists(a, b []*ast.Path) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if identsToComparable(a[i].Idents...) != identsToComparable(b[i].Idents...) {
			return false
		}
	}
	return true
}

func hasPrivilegeOnColumn(p *ast.PrivilegeOnTable, column *ast.Ident) bool {
	if p == nil || column == nil {
		return false
	}
	targetKey := identsToComparable(column)
	for _, privilege := range p.Privileges {
		switch t := privilege.(type) {
		case *ast.SelectPrivilege:
			for _, c := range t.Columns {
				if identsToComparable(c) == targetKey {
					return true
				}
			}
		case *ast.InsertPrivilege:
			for _, c := range t.Columns {
				if identsToComparable(c) == targetKey {
					return true
				}
			}
		case *ast.UpdatePrivilege:
			for _, c := range t.Columns {
				if identsToComparable(c) == targetKey {
					return true
				}
			}
		case *ast.DeletePrivilege:
			continue
		default:
			continue
		}
	}
	return false
}

func grantOnColumnOnly(p *ast.PrivilegeOnTable, column *ast.Ident) bool {
	if len(p.Names) != 1 {
		return false
	}
	target := identsToComparable(column)
	for _, privilege := range p.Privileges {
		var columns []*ast.Ident
		switch t := privilege.(type) {
		case *ast.SelectPrivilege:
			columns = t.Columns
		case *ast.InsertPrivilege:
			columns = t.Columns
		case *ast.UpdatePrivilege:
			columns = t.Columns
		default:
			return false
		}
		if len(columns) != 1 || identsToComparable(columns[0]) != target {
			return false
		}
	}
	return true
}
