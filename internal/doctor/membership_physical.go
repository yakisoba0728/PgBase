package doctor

import "strconv"

// The types and matchers below describe pgbase_meta's record-membership
// installation, which no schema planner manifest covers: those objects are
// created by system migration 000007 rather than by a catalog change, so their
// expectation has to live here. Collection and relation objects are compared
// against schema.ExpectedPhysicalSchema instead.

type physicalColumn struct {
	typeName   string
	notNull    bool
	defaultSQL *string
	attribute  int16
}

type physicalTable struct {
	oid         int64
	schema      string
	name        string
	relKind     string
	persistence string
	columns     map[string]physicalColumn
}

type physicalConstraint struct {
	tableOID             int64
	name, constraintType string
	columns              []int16
	targetTableOID       int64
	targetColumns        []int16
	deleteAction         string
	updateAction         string
	matchType            string
	deferrable, deferred bool
	validated            bool
	checkExpression      *string
}

type physicalIndex struct {
	tableOID                    int64
	name, method                string
	columns, options            []int16
	unique, primary             bool
	valid, ready, live          bool
	keyCount, attributeCount    int
	hasPredicate, hasExpression bool
}

func ownedObjectKey(owner int64, name string) string {
	return strconv.FormatInt(owner, 10) + "\x00" + name
}

func keyConstraintMatches(constraint physicalConstraint, table *physicalTable, kind string, columns []string) bool {
	return table != nil && constraint.tableOID == table.oid && constraint.constraintType == kind && constraint.validated &&
		!constraint.deferrable && !constraint.deferred &&
		columnNumbersMatch(table, constraint.columns, columns)
}

func foreignKeyMatches(
	constraint physicalConstraint,
	source *physicalTable,
	sourceColumns []string,
	target *physicalTable,
	targetColumns []string,
	deleteAction string,
	deferrable, deferred bool,
) bool {
	return source != nil && target != nil && constraint.tableOID == source.oid &&
		constraint.constraintType == "f" && constraint.targetTableOID == target.oid &&
		constraint.deleteAction == deleteAction && constraint.updateAction == "a" && constraint.matchType == "s" &&
		constraint.deferrable == deferrable && constraint.deferred == deferred && constraint.validated &&
		columnNumbersMatch(source, constraint.columns, sourceColumns) &&
		columnNumbersMatch(target, constraint.targetColumns, targetColumns)
}

func indexOptionsAreDefault(options []int16, count int) bool {
	if len(options) != count {
		return false
	}
	for _, option := range options {
		if option != 0 {
			return false
		}
	}
	return true
}

func columnNumbersMatch(table *physicalTable, numbers []int16, names []string) bool {
	if table == nil || len(numbers) != len(names) {
		return false
	}
	for index, name := range names {
		column, ok := table.columns[name]
		if !ok || column.attribute != numbers[index] {
			return false
		}
	}
	return true
}
