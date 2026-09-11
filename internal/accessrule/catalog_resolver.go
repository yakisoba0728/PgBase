package accessrule

import (
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// NewCatalogResolver resolves only identifiers present in one transaction-
// pinned catalog snapshot. It never consults metadata dynamically.
func NewCatalogResolver(catalog schema.Catalog, collection schema.Collection, alias string) (PBResolver, error) {
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

func (r *catalogResolver) Resolve(path string) (PBOperand, error) {
	segments := strings.Split(path, ".")
	if len(segments) == 0 || len(segments) > 5 {
		return PBOperand{}, ErrInvalid
	}
	current, alias := r.root, r.rootAlias
	var from, predicate string
	modifier := ""
	if strings.Contains(segments[len(segments)-1], ":") {
		segments[len(segments)-1], modifier, _ = strings.Cut(segments[len(segments)-1], ":")
		if modifier != "lower" && modifier != "length" && modifier != "each" {
			return PBOperand{}, ErrInvalid
		}
	}
	for index, segment := range segments {
		field, reverse, target, ok := r.resolveField(current, segment)
		if !ok {
			return PBOperand{}, ErrInvalid
		}
		last := index == len(segments)-1
		if field.Type != schema.TypeRelation {
			column := alias + "." + pgx.Identifier{field.ColumnName}.Sanitize()
			if field.Hidden {
				return PBOperand{}, ErrInvalid
			}
			if field.Type == schema.TypeJSONB && !last {
				for _, part := range segments[index+1:] {
					if !validPBPath(part) {
						return PBOperand{}, ErrInvalid
					}
				}
				path := strings.Join(segments[index+1:], ",")
				if modifier == "each" {
					column = "(" + column + "#>'{" + path + "}')"
					return r.modifiedOperand(column, schema.Field{Type: schema.TypeJSONB}, from, predicate, modifier), nil
				}
				column = "(" + column + "#>>'{" + path + "}')"
				return r.applyModifier(r.multi(column, PBText, from, predicate), modifier), nil
			}
			if field.Type == schema.TypeGeoPoint && index+1 == len(segments)-1 && (segments[index+1] == "lat" || segments[index+1] == "lon") {
				column = "NULLIF(" + column + "->>'" + segments[index+1] + "','')::numeric"
				return r.applyModifier(r.multi(column, PBNumber, from, predicate), modifier), nil
			}
			if !last {
				return PBOperand{}, ErrInvalid
			}
			return r.modifiedOperand(column, field, from, predicate, modifier), nil
		}
		if field.Relation == nil {
			return PBOperand{}, ErrInvalid
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
			value := r.multi(alias+`."id"`, PBUUID, from, predicate)
			if modifier == "each" {
				return value, nil
			}
			return r.applyModifier(value, modifier), nil
		}
	}
	return PBOperand{}, ErrInvalid
}

func (r *catalogResolver) modifiedOperand(sql string, field schema.Field, from, predicate, modifier string) PBOperand {
	if modifier == "each" && field.Type == schema.TypeJSONB {
		alias := pgx.Identifier{"pb_each"}.Sanitize()
		join := "jsonb_array_elements_text(CASE WHEN jsonb_typeof(" + sql + ")='array' THEN " + sql + " ELSE jsonb_build_array(" + sql + ") END) " + alias + "(value)"
		if from != "" {
			join = from + " CROSS JOIN LATERAL " + join
		}
		return r.multi(alias+".value", PBText, join, predicate)
	}
	return r.applyModifier(r.operand(sql, field, from, predicate), modifier)
}

func (r *catalogResolver) applyModifier(value PBOperand, modifier string) PBOperand {
	if modifier == "" || modifier == "each" && value.Cardinality == PBMany {
		return value
	}
	apply := func(sql string) (string, PBKind, bool) {
		switch modifier {
		case "lower":
			if value.Kind != PBText {
				return "", 0, false
			}
			return "lower(" + sql + ")", PBText, true
		case "length":
			if value.Kind == PBText {
				return "char_length(" + sql + ")", PBNumber, true
			}
		}
		return "", 0, false
	}
	if value.Cardinality == PBScalar {
		sql, kind, ok := apply(value.SQL)
		if !ok {
			return PBOperand{}
		}
		return PBOperand{SQL: sql, Kind: kind, Cardinality: PBScalar}
	}
	if value.Multi == nil {
		return PBOperand{}
	}
	sql, kind, ok := apply(value.Multi.ValueSQL)
	if !ok {
		return PBOperand{}
	}
	return PBOperand{Kind: kind, Cardinality: PBMany, Multi: &PBMulti{From: value.Multi.From, Predicate: value.Multi.Predicate, ValueSQL: sql}}
}

func (r *catalogResolver) ResolveCollection(name, path string) (PBOperand, error) {
	collection, ok := r.catalog.Collections[name]
	if !ok {
		return PBOperand{}, ErrInvalid
	}
	nested := &catalogResolver{catalog: r.catalog, root: collection, rootAlias: pgx.Identifier{"pb_collection"}.Sanitize()}
	value, err := nested.Resolve(path)
	if err != nil {
		return PBOperand{}, err
	}
	if value.Cardinality == PBScalar {
		return nested.multi(value.SQL, value.Kind, pgx.Identifier{"app", collection.TableName}.Sanitize()+" "+nested.rootAlias, ""), nil
	}
	return value, nil
}

func (r *catalogResolver) resolveField(current schema.Collection, name string) (schema.Field, bool, schema.Collection, bool) {
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
func (r *catalogResolver) operand(sql string, f schema.Field, from, predicate string) PBOperand {
	return r.multi(sql, pbKind(f.Type), from, predicate)
}
func (r *catalogResolver) multi(sql string, k PBKind, from, predicate string) PBOperand {
	if k == 0 {
		return PBOperand{}
	}
	if from == "" {
		return PBOperand{SQL: sql, Kind: k, Cardinality: PBScalar}
	}
	return PBOperand{Kind: k, Cardinality: PBMany, Multi: &PBMulti{From: from, Predicate: predicate, ValueSQL: sql}}
}
func pbKind(kind schema.FieldType) PBKind {
	switch kind {
	case schema.TypeText, schema.TypeEditor:
		return PBText
	case schema.TypeBoolean:
		return PBBool
	case schema.TypeInteger, schema.TypeBigInt, schema.TypeNumeric, schema.TypeGeoPoint:
		return PBNumber
	case schema.TypeTimestampTZ, schema.TypeAutoDate:
		return PBTime
	case schema.TypeUUID, schema.TypeRelation:
		return PBUUID
	case schema.TypeJSONB:
		return PBJSON
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
