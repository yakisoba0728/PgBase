package accessrule

import (
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// NewCatalogResolver resolves only identifiers present in one transaction-
// pinned catalog snapshot. It never consults metadata dynamically.
func NewCatalogResolver(catalog schema.Catalog, collection schema.Collection, alias string) (Resolver, error) {
	if alias == "" {
		return nil, ErrInvalid
	}
	return &catalogResolver{catalog: catalog, root: collection, rootAlias: pgx.Identifier{alias}.Sanitize()}, nil
}

type catalogResolver struct {
	catalog   schema.Catalog
	root      schema.Collection
	rootAlias string
}

func (r *catalogResolver) Resolve(path string) (Operand, error) {
	segments := strings.Split(path, ".")
	if len(segments) == 0 || len(segments) > 5 {
		return Operand{}, ErrInvalid
	}
	current, alias := r.root, r.rootAlias
	var from, predicate string
	modifier := ""
	if strings.Contains(segments[len(segments)-1], ":") {
		segments[len(segments)-1], modifier, _ = strings.Cut(segments[len(segments)-1], ":")
		if modifier != "lower" && modifier != "length" && modifier != "each" {
			return Operand{}, ErrInvalid
		}
	}
	for index, segment := range segments {
		field, reverse, target, ok := r.resolveField(current, segment)
		if !ok {
			return Operand{}, ErrInvalid
		}
		last := index == len(segments)-1
		if field.Type != schema.TypeRelation {
			column := alias + "." + pgx.Identifier{field.ColumnName}.Sanitize()
			if field.Hidden {
				return Operand{}, ErrInvalid
			}
			if field.Type == schema.TypeJSONB && !last {
				for _, part := range segments[index+1:] {
					if !validPBPath(part) {
						return Operand{}, ErrInvalid
					}
				}
				path := strings.Join(segments[index+1:], ",")
				if modifier == "each" {
					column = "(" + column + "#>'{" + path + "}')"
					return r.modifiedOperand(column, schema.Field{Type: schema.TypeJSONB}, from, predicate, modifier), nil
				}
				column = "(" + column + "#>>'{" + path + "}')"
				return r.applyModifier(r.multi(column, OperandText, from, predicate), modifier), nil
			}
			if field.Type == schema.TypeGeoPoint && index+1 == len(segments)-1 && (segments[index+1] == "lat" || segments[index+1] == "lon") {
				column = "NULLIF(" + column + "->>'" + segments[index+1] + "','')::numeric"
				return r.applyModifier(r.multi(column, OperandNumber, from, predicate), modifier), nil
			}
			if !last {
				return Operand{}, ErrInvalid
			}
			return r.modifiedOperand(column, field, from, predicate, modifier), nil
		}
		if field.Relation == nil {
			return Operand{}, ErrInvalid
		}
		nextAlias := pgx.Identifier{"pb_rel_" + string(rune('a'+index))}.Sanitize()
		table := pgx.Identifier{"app", target.TableName}.Sanitize()
		join := table + " " + nextAlias
		var condition string
		if reverse {
			if field.Relation.Cardinality == schema.RelationMultiple {
				edge := pgx.Identifier{"pgbase_relation", field.Relation.Storage.Name}.Sanitize()
				e := pgx.Identifier{"pb_edge_r_" + string(rune('a'+index))}.Sanitize()
				join = edge + " " + e + " JOIN " + table + " " + nextAlias + " ON " + nextAlias + `."id"=` + e + `."source_id"`
				condition = e + `."target_id"=` + alias + `."id"`
			} else {
				condition = nextAlias + "." + pgx.Identifier{field.ColumnName}.Sanitize() + "=" + alias + `."id"`
			}
		} else if field.Relation.Cardinality == schema.RelationMultiple {
			edge := pgx.Identifier{"pgbase_relation", field.Relation.Storage.Name}.Sanitize()
			e := pgx.Identifier{"pb_edge_" + string(rune('a'+index))}.Sanitize()
			join = edge + " " + e + " JOIN " + table + " " + nextAlias + " ON " + nextAlias + `."id"=` + e + `."target_id"`
			condition = e + `."source_id"=` + alias + `."id"`
		} else {
			condition = nextAlias + `."id"=` + alias + "." + pgx.Identifier{field.ColumnName}.Sanitize()
		}
		if from == "" {
			from = join
			predicate = condition
		} else {
			from += " JOIN " + join + " ON " + condition
		}
		current, alias = target, nextAlias
		if last {
			value := r.multi(alias+`."id"`, OperandUUID, from, predicate)
			if modifier == "each" {
				return value, nil
			}
			return r.applyModifier(value, modifier), nil
		}
	}
	return Operand{}, ErrInvalid
}

func (r *catalogResolver) modifiedOperand(sql string, field schema.Field, from, predicate, modifier string) Operand {
	if modifier == "each" && field.Type == schema.TypeJSONB {
		alias := pgx.Identifier{"pb_each"}.Sanitize()
		join := "jsonb_array_elements_text(CASE WHEN jsonb_typeof(" + sql + ")='array' THEN " + sql + " ELSE jsonb_build_array(" + sql + ") END) " + alias + "(value)"
		if from != "" {
			join = from + " CROSS JOIN LATERAL " + join
		}
		return r.multi(alias+".value", OperandText, join, predicate)
	}
	return r.applyModifier(r.operand(sql, field, from, predicate), modifier)
}

func (r *catalogResolver) applyModifier(value Operand, modifier string) Operand {
	if modifier == "" || modifier == "each" && value.Cardinality == Many {
		return value
	}
	apply := func(sql string) (string, OperandKind, bool) {
		switch modifier {
		case "lower":
			if value.Kind != OperandText {
				return "", 0, false
			}
			return "lower(" + sql + ")", OperandText, true
		case "length":
			if value.Kind == OperandText {
				return "char_length(" + sql + ")", OperandNumber, true
			}
		}
		return "", 0, false
	}
	if value.Cardinality == Scalar {
		sql, kind, ok := apply(value.SQL)
		if !ok {
			return Operand{}
		}
		return Operand{SQL: sql, Kind: kind, Cardinality: Scalar}
	}
	if value.Multi == nil {
		return Operand{}
	}
	sql, kind, ok := apply(value.Multi.ValueSQL)
	if !ok {
		return Operand{}
	}
	return Operand{Kind: kind, Cardinality: Many, Multi: &MultiOperand{From: value.Multi.From, Predicate: value.Multi.Predicate, ValueSQL: sql}}
}

func (r *catalogResolver) ResolveCollection(name, path string) (Operand, error) {
	collection, ok := r.catalog.Collections[name]
	if !ok {
		return Operand{}, ErrInvalid
	}
	nested := &catalogResolver{catalog: r.catalog, root: collection, rootAlias: pgx.Identifier{"pb_collection"}.Sanitize()}
	value, err := nested.Resolve(path)
	if err != nil {
		return Operand{}, err
	}
	if value.Cardinality == Scalar {
		return nested.multi(value.SQL, value.Kind, pgx.Identifier{"app", collection.TableName}.Sanitize()+" "+nested.rootAlias, ""), nil
	}
	if value.Multi == nil {
		return Operand{}, ErrInvalid
	}
	value.Multi.From = pgx.Identifier{"app", collection.TableName}.Sanitize() + " " + nested.rootAlias + " CROSS JOIN " + value.Multi.From
	return value, nil
}

func (r *catalogResolver) resolveField(current schema.Collection, name string) (schema.Field, bool, schema.Collection, bool) {
	// System columns live outside Fields; the typed compiler and filters accept them too.
	switch name {
	case "id":
		return schema.Field{Name: name, ColumnName: name, Type: schema.TypeUUID}, false, current, true
	case "created_at", "updated_at":
		return schema.Field{Name: name, ColumnName: name, Type: schema.TypeTimestampTZ}, false, current, true
	}
	for _, f := range current.Fields {
		if f.Name == name {
			target := current
			if f.Type == schema.TypeRelation {
				target, _ = r.byID(f.Relation.TargetCollectionID)
			}
			return f, false, target, true
		}
	}
	for _, ref := range r.catalog.Incoming[current.ID] {
		source, ok := r.byID(ref.SourceCollectionID)
		if !ok {
			continue
		}
		for _, f := range source.Fields {
			if f.ID == ref.SourceFieldID && name == source.Name+"_via_"+f.Name {
				return f, true, source, true
			}
		}
	}
	return schema.Field{}, false, schema.Collection{}, false
}
func (r *catalogResolver) byID(id uuid.UUID) (schema.Collection, bool) {
	for _, c := range r.catalog.Collections {
		if c.ID == id {
			return c, true
		}
	}
	return schema.Collection{}, false
}
func (r *catalogResolver) operand(sql string, f schema.Field, from, predicate string) Operand {
	return r.multi(sql, pbKind(f.Type), from, predicate)
}
func (r *catalogResolver) multi(sql string, k OperandKind, from, predicate string) Operand {
	if k == 0 {
		return Operand{}
	}
	if from == "" {
		return Operand{SQL: sql, Kind: k, Cardinality: Scalar}
	}
	return Operand{Kind: k, Cardinality: Many, Multi: &MultiOperand{From: from, Predicate: predicate, ValueSQL: sql}}
}
func pbKind(kind schema.FieldType) OperandKind {
	switch kind {
	case schema.TypeText, schema.TypeEditor:
		return OperandText
	case schema.TypeBoolean:
		return OperandBool
	case schema.TypeInteger, schema.TypeBigInt, schema.TypeNumeric, schema.TypeGeoPoint:
		return OperandNumber
	case schema.TypeTimestampTZ, schema.TypeAutoDate:
		return OperandTime
	case schema.TypeUUID, schema.TypeRelation:
		return OperandUUID
	case schema.TypeJSONB:
		return OperandJSON
	}
	return 0
}
func validPBPath(v string) bool {
	if v == "" || len(v) > 48 {
		return false
	}
	for _, c := range v {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
