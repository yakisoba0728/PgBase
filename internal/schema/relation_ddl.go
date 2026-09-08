package schema

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func createRelationStatements(sourceTable string, field PlannedField, addColumn bool) []string {
	if field.Relation == nil {
		return nil
	}
	if field.Relation.Cardinality == RelationMultiple {
		return createMultiRelationStatements(sourceTable, field)
	}
	return createSingleRelationStatements(sourceTable, field, addColumn)
}

func createSingleRelationStatements(sourceTable string, field PlannedField, addColumn bool) []string {
	relation := field.Relation
	if relation == nil {
		return nil
	}
	physicalField := field.ColumnName
	if physicalField == "" {
		physicalField = field.Name
	}
	names := NewRelationPhysicalNames(sourceTable, physicalField)
	source := pgx.Identifier{"app", sourceTable}.Sanitize()
	target := pgx.Identifier{"app", relation.TargetCollection}.Sanitize()
	column := pgx.Identifier{physicalField}.Sanitize()
	statements := make([]string, 0, 3)
	if addColumn {
		definition := column + " uuid"
		if field.Required {
			definition += " not null"
		}
		statements = append(statements, "ALTER TABLE "+source+" ADD COLUMN "+definition)
	}
	statements = append(statements,
		"ALTER TABLE "+source+" ADD CONSTRAINT "+pgx.Identifier{names.SingleForeignKey}.Sanitize()+
			" FOREIGN KEY ("+column+") REFERENCES "+target+" ("+pgx.Identifier{"id"}.Sanitize()+")"+
			" ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED",
	)
	indexPrefix := "CREATE INDEX "
	if field.Unique {
		indexPrefix = "CREATE UNIQUE INDEX "
	}
	statements = append(statements,
		indexPrefix+pgx.Identifier{names.SingleReverseIndex}.Sanitize()+" ON "+source+" ("+column+")",
	)
	return statements
}

func createMultiRelationStatements(sourceTable string, field PlannedField) []string {
	relation := field.Relation
	if relation == nil {
		return nil
	}
	physicalField := field.ColumnName
	if physicalField == "" {
		physicalField = field.Name
	}
	names := NewRelationPhysicalNames(sourceTable, physicalField)
	edge := pgx.Identifier{"pgbase_relation", names.MultiTable}.Sanitize()
	source := pgx.Identifier{"app", sourceTable}.Sanitize()
	target := pgx.Identifier{"app", relation.TargetCollection}.Sanitize()
	sourceID := pgx.Identifier{"source_id"}.Sanitize()
	targetID := pgx.Identifier{"target_id"}.Sanitize()
	position := pgx.Identifier{"position"}.Sanitize()

	createTable := "CREATE TABLE " + edge + " (" +
		sourceID + " uuid NOT NULL, " +
		targetID + " uuid NOT NULL, " +
		position + " integer NOT NULL, " +
		"CONSTRAINT " + pgx.Identifier{names.MultiPrimaryKey}.Sanitize() + " PRIMARY KEY (" + sourceID + ", " + targetID + "), " +
		"CONSTRAINT " + pgx.Identifier{names.MultiPositionUnique}.Sanitize() + " UNIQUE (" + sourceID + ", " + position + "), " +
		"CONSTRAINT " + pgx.Identifier{names.MultiPositionCheck}.Sanitize() + " CHECK (" + position + " >= 0), " +
		"CONSTRAINT " + pgx.Identifier{names.MultiSourceForeignKey}.Sanitize() + " FOREIGN KEY (" + sourceID + ")" +
		" REFERENCES " + source + " (" + pgx.Identifier{"id"}.Sanitize() + ") ON DELETE CASCADE, " +
		"CONSTRAINT " + pgx.Identifier{names.MultiTargetForeignKey}.Sanitize() + " FOREIGN KEY (" + targetID + ")" +
		" REFERENCES " + target + " (" + pgx.Identifier{"id"}.Sanitize() + ")" +
		" ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED)"

	return []string{
		createTable,
		"CREATE INDEX " + pgx.Identifier{names.MultiReverseIndex}.Sanitize() + " ON " + edge +
			" (" + targetID + ", " + sourceID + ")",
		positionFunctionStatement(sourceTable, physicalField),
		"CREATE CONSTRAINT TRIGGER " + pgx.Identifier{names.PositionTrigger}.Sanitize() +
			" AFTER INSERT OR UPDATE OR DELETE ON " + edge +
			" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION " +
			pgx.Identifier{"pgbase_relation", names.PositionFunction}.Sanitize() + "()",
		cardinalityFunctionStatement(sourceTable, physicalField, relation.MinSelect, relation.MaxSelect, false),
		"CREATE CONSTRAINT TRIGGER " + pgx.Identifier{names.SourceCardinalityTrigger}.Sanitize() +
			" AFTER INSERT ON " + source +
			" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION " +
			pgx.Identifier{"pgbase_relation", names.CardinalityFunction}.Sanitize() + "()",
		"CREATE CONSTRAINT TRIGGER " + pgx.Identifier{names.EdgeCardinalityTrigger}.Sanitize() +
			" AFTER INSERT OR UPDATE OR DELETE ON " + edge +
			" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION " +
			pgx.Identifier{"pgbase_relation", names.CardinalityFunction}.Sanitize() + "()",
	}
}

func positionFunctionStatement(sourceTable, fieldName string) string {
	names := NewRelationPhysicalNames(sourceTable, fieldName)
	edge := pgx.Identifier{"pgbase_relation", names.MultiTable}.Sanitize()
	function := pgx.Identifier{"pgbase_relation", names.PositionFunction}.Sanitize()
	return "CREATE FUNCTION " + function + `() RETURNS trigger LANGUAGE plpgsql AS $pgbase_relation$
DECLARE
    affected_source uuid;
    affected_sources uuid[];
    relation_count bigint;
    minimum_position integer;
    maximum_position integer;
BEGIN
	IF TG_OP = 'INSERT' THEN
		affected_sources := ARRAY[NEW.source_id];
	ELSIF TG_OP = 'DELETE' THEN
		affected_sources := ARRAY[OLD.source_id];
	ELSE
		affected_sources := ARRAY[OLD.source_id, NEW.source_id];
	END IF;
    FOR affected_source IN
        SELECT DISTINCT candidate
        FROM unnest(affected_sources) AS candidates(candidate)
        WHERE candidate IS NOT NULL
    LOOP
        SELECT count(*), min(position), max(position)
          INTO relation_count, minimum_position, maximum_position
          FROM ` + edge + `
         WHERE source_id = affected_source;
        IF relation_count > 0 AND
           (minimum_position <> 0 OR maximum_position::bigint <> relation_count - 1) THEN
            RAISE EXCEPTION USING
                ERRCODE = '23514',
                MESSAGE = 'relation position constraint violated',
                CONSTRAINT = '` + names.MultiPositionCheck + `';
        END IF;
    END LOOP;
    RETURN NULL;
END
$pgbase_relation$`
}

func cardinalityFunctionStatement(sourceTable, fieldName string, minimum, maximum int, replace bool) string {
	names := NewRelationPhysicalNames(sourceTable, fieldName)
	edge := pgx.Identifier{"pgbase_relation", names.MultiTable}.Sanitize()
	source := pgx.Identifier{"app", sourceTable}.Sanitize()
	function := pgx.Identifier{"pgbase_relation", names.CardinalityFunction}.Sanitize()
	verb := "CREATE FUNCTION "
	if replace {
		verb = "CREATE OR REPLACE FUNCTION "
	}
	return verb + function + `() RETURNS trigger LANGUAGE plpgsql AS $pgbase_relation$
DECLARE
    affected_source uuid;
    affected_sources uuid[];
    relation_count bigint;
BEGIN
    IF TG_TABLE_SCHEMA = 'app' AND TG_TABLE_NAME = ` + postgresStringLiteral(sourceTable) + ` THEN
        affected_source := NEW.id;
        SELECT count(*) INTO relation_count
          FROM ` + edge + `
         WHERE source_id = affected_source;
        IF relation_count < ` + fmt.Sprintf("%d", minimum) + ` OR relation_count > ` + fmt.Sprintf("%d", maximum) + ` THEN
            RAISE EXCEPTION USING
                ERRCODE = '23514',
                MESSAGE = 'relation cardinality constraint violated',
                CONSTRAINT = '` + names.CardinalityFunction + `';
        END IF;
        RETURN NULL;
    END IF;

	IF TG_OP = 'INSERT' THEN
		affected_sources := ARRAY[NEW.source_id];
	ELSIF TG_OP = 'DELETE' THEN
		affected_sources := ARRAY[OLD.source_id];
	ELSE
		affected_sources := ARRAY[OLD.source_id, NEW.source_id];
	END IF;
    FOR affected_source IN
        SELECT DISTINCT candidate
        FROM unnest(affected_sources) AS candidates(candidate)
        WHERE candidate IS NOT NULL
    LOOP
        IF EXISTS (SELECT 1 FROM ` + source + ` WHERE id = affected_source) THEN
            SELECT count(*) INTO relation_count
              FROM ` + edge + `
             WHERE source_id = affected_source;
            IF relation_count < ` + fmt.Sprintf("%d", minimum) + ` OR relation_count > ` + fmt.Sprintf("%d", maximum) + ` THEN
                RAISE EXCEPTION USING
                    ERRCODE = '23514',
                    MESSAGE = 'relation cardinality constraint violated',
                    CONSTRAINT = '` + names.CardinalityFunction + `';
            END IF;
        END IF;
    END LOOP;
    RETURN NULL;
END
$pgbase_relation$`
}

func postgresStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func dropRelationFieldStatements(collection Collection, field Field) []string {
	if field.Relation == nil {
		return nil
	}
	if field.Relation.Storage.Kind == RelationStorageEdgeTableV1 {
		return dropMultiRelationStatements(collection.TableName, field.ColumnName)
	}
	names := NewRelationPhysicalNames(collection.TableName, field.ColumnName)
	table := pgx.Identifier{"app", collection.TableName}.Sanitize()
	return []string{
		"ALTER TABLE " + table + " DROP CONSTRAINT " + pgx.Identifier{names.SingleForeignKey}.Sanitize(),
		"DROP INDEX IF EXISTS " + pgx.Identifier{"app", names.SingleReverseIndex}.Sanitize(),
		"ALTER TABLE " + table + " DROP COLUMN " + pgx.Identifier{field.ColumnName}.Sanitize(),
	}
}

func dropMultiRelationStatements(sourceTable, fieldName string) []string {
	names := NewRelationPhysicalNames(sourceTable, fieldName)
	edge := pgx.Identifier{"pgbase_relation", names.MultiTable}.Sanitize()
	source := pgx.Identifier{"app", sourceTable}.Sanitize()
	return []string{
		"DROP TRIGGER IF EXISTS " + pgx.Identifier{names.PositionTrigger}.Sanitize() + " ON " + edge,
		"DROP TRIGGER IF EXISTS " + pgx.Identifier{names.EdgeCardinalityTrigger}.Sanitize() + " ON " + edge,
		"DROP TRIGGER IF EXISTS " + pgx.Identifier{names.SourceCardinalityTrigger}.Sanitize() + " ON " + source,
		"DROP TABLE " + edge,
		"DROP FUNCTION " + pgx.Identifier{"pgbase_relation", names.PositionFunction}.Sanitize() + "()",
		"DROP FUNCTION " + pgx.Identifier{"pgbase_relation", names.CardinalityFunction}.Sanitize() + "()",
	}
}

func alterRelationFieldStatements(sourceTable string, ordinal int, previous, desired PlannedField) ([]string, error) {
	fieldPath := fmt.Sprintf("fields.%d", ordinal)
	if previous.Type != TypeRelation || desired.Type != TypeRelation || previous.Relation == nil || desired.Relation == nil {
		return nil, validation(fieldPath+".type", "relation_storage_migration_required")
	}
	before := previous.Relation
	after := desired.Relation
	if !samePlannedRelationTarget(before, after) ||
		before.Cardinality != after.Cardinality ||
		before.ValueCodecVersion != after.ValueCodecVersion ||
		before.Storage != after.Storage {
		return nil, validation(fieldPath+".relation", "relation_storage_migration_required")
	}

	if after.Cardinality == RelationSingle {
		statements := make([]string, 0, 3)
		table := pgx.Identifier{"app", sourceTable}.Sanitize()
		column := pgx.Identifier{desired.ColumnName}.Sanitize()
		if previous.Required != desired.Required {
			action := "DROP NOT NULL"
			if desired.Required {
				action = "SET NOT NULL"
			}
			statements = append(statements, "ALTER TABLE "+table+" ALTER COLUMN "+column+" "+action)
		}
		if previous.Unique != desired.Unique {
			names := NewRelationPhysicalNames(sourceTable, desired.ColumnName)
			statements = append(statements, "DROP INDEX IF EXISTS "+pgx.Identifier{"app", names.SingleReverseIndex}.Sanitize())
			prefix := "CREATE INDEX "
			if desired.Unique {
				prefix = "CREATE UNIQUE INDEX "
			}
			statements = append(statements, prefix+pgx.Identifier{names.SingleReverseIndex}.Sanitize()+" ON "+table+" ("+column+")")
		}
		return statements, nil
	}

	if before.MinSelect == after.MinSelect && before.MaxSelect == after.MaxSelect {
		return nil, nil
	}
	return []string{
		validateMultiBoundsStatement(sourceTable, desired.ColumnName, after.MinSelect, after.MaxSelect),
		cardinalityFunctionStatement(sourceTable, desired.ColumnName, after.MinSelect, after.MaxSelect, true),
	}, nil
}

func samePlannedRelationTarget(left, right *PlannedRelation) bool {
	if left == nil || right == nil || left.TargetCollectionID == nil || right.TargetCollectionID == nil {
		return left != nil && right != nil && left.TargetCollectionID == nil && right.TargetCollectionID == nil &&
			left.TargetCollection == right.TargetCollection
	}
	return *left.TargetCollectionID == *right.TargetCollectionID
}

func validateMultiBoundsStatement(sourceTable, fieldName string, minimum, maximum int) string {
	names := NewRelationPhysicalNames(sourceTable, fieldName)
	edge := pgx.Identifier{"pgbase_relation", names.MultiTable}.Sanitize()
	source := pgx.Identifier{"app", sourceTable}.Sanitize()
	return `DO $pgbase_relation$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM ` + source + ` AS source_record
          LEFT JOIN ` + edge + ` AS relation_edge
            ON relation_edge.source_id = source_record.id
         GROUP BY source_record.id
        HAVING count(relation_edge.target_id) < ` + fmt.Sprintf("%d", minimum) + `
            OR count(relation_edge.target_id) > ` + fmt.Sprintf("%d", maximum) + `
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'existing data violates relation cardinality';
    END IF;
END
$pgbase_relation$`
}
