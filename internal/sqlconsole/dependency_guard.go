package sqlconsole

import (
	"context"
	"fmt"
	"strings"

	"github.com/yakisoba0728/PgBase/internal/database"
)

type nativeDependencyScope struct {
	functions map[string][]uint32
	operators map[string][]uint32
	casts     map[string]uint32
}

var specialSQLFunctions = map[string]struct{}{
	"coalesce": {}, "greatest": {}, "least": {}, "nullif": {}, "trim": {},
}

var specialSQLOperators = map[string]struct{}{"AND": {}, "OR": {}, "NOT": {}}

func inspectNativeDependencies(ctx context.Context, pool *database.Pool) (nativeDependencyScope, error) {
	scope := nativeDependencyScope{
		functions: make(map[string][]uint32, len(allowedFunctions)),
		operators: make(map[string][]uint32, len(allowedOperators)+len(allowedUnaryOperators)),
		casts:     make(map[string]uint32, len(allowedCasts)),
	}
	for name := range allowedFunctions {
		if _, special := specialSQLFunctions[name]; special {
			scope.functions[name] = nil
			continue
		}
		rows, err := pool.Query(ctx, `select p.oid,p.provolatile::text,p.prosecdef,p.proconfig is not null,p.prokind::text
			from pg_catalog.pg_proc p join pg_catalog.pg_namespace n on n.oid=p.pronamespace
			where n.nspname='pg_catalog' and p.proname=$1 order by p.oid`, name)
		if err != nil {
			return nativeDependencyScope{}, err
		}
		for rows.Next() {
			var oid uint32
			var volatility, kind string
			var securityDefiner, configured bool
			if err := rows.Scan(&oid, &volatility, &securityDefiner, &configured, &kind); err != nil {
				rows.Close()
				return nativeDependencyScope{}, err
			}
			if oid == 0 || securityDefiner || configured || kind == "p" || (volatility == "v" && name != "pg_sleep") {
				rows.Close()
				return nativeDependencyScope{}, fmt.Errorf("%w: builtin function pg_catalog.%s has an unsafe OID", ErrUnsafeReadRelation, name)
			}
			scope.functions[name] = append(scope.functions[name], oid)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nativeDependencyScope{}, err
		}
		rows.Close()
		if len(scope.functions[name]) == 0 {
			return nativeDependencyScope{}, fmt.Errorf("%w: builtin function pg_catalog.%s is absent", ErrUnsafeReadRelation, name)
		}
	}
	operatorNames := make(map[string]struct{}, len(allowedOperators)+len(allowedUnaryOperators))
	for name := range allowedOperators {
		operatorNames[name] = struct{}{}
	}
	for name := range allowedUnaryOperators {
		operatorNames[name] = struct{}{}
	}
	for name := range operatorNames {
		if _, special := specialSQLOperators[name]; special {
			scope.operators[name] = nil
			continue
		}
		catalogName := name
		if catalogName == "!=" {
			catalogName = "<>"
		}
		rows, err := pool.Query(ctx, `select o.oid,p.oid,p.provolatile::text,p.prosecdef,p.proconfig is not null
			from pg_catalog.pg_operator o join pg_catalog.pg_namespace n on n.oid=o.oprnamespace
			join pg_catalog.pg_proc p on p.oid=o.oprcode
			join pg_catalog.pg_namespace pn on pn.oid=p.pronamespace
			where n.nspname='pg_catalog' and pn.nspname='pg_catalog' and o.oprname=$1 order by o.oid`, catalogName)
		if err != nil {
			return nativeDependencyScope{}, err
		}
		for rows.Next() {
			var operatorOID, functionOID uint32
			var volatility string
			var securityDefiner, configured bool
			if err := rows.Scan(&operatorOID, &functionOID, &volatility, &securityDefiner, &configured); err != nil {
				rows.Close()
				return nativeDependencyScope{}, err
			}
			if operatorOID == 0 || functionOID == 0 || volatility == "v" || securityDefiner || configured {
				rows.Close()
				return nativeDependencyScope{}, fmt.Errorf("%w: builtin operator %s has an unsafe implementation OID", ErrUnsafeReadRelation, name)
			}
			scope.operators[name] = append(scope.operators[name], operatorOID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nativeDependencyScope{}, err
		}
		rows.Close()
		if len(scope.operators[name]) == 0 {
			return nativeDependencyScope{}, fmt.Errorf("%w: builtin operator %s is absent", ErrUnsafeReadRelation, name)
		}
	}
	for cast := range allowedCasts {
		typeName := postgresTypeName(cast)
		var oid uint32
		if err := pool.QueryRow(ctx, `select t.oid from pg_catalog.pg_type t
			join pg_catalog.pg_namespace n on n.oid=t.typnamespace
			where n.nspname='pg_catalog' and t.typname=$1`, typeName).Scan(&oid); err != nil || oid == 0 {
			return nativeDependencyScope{}, fmt.Errorf("%w: builtin cast %s cannot be resolved", ErrUnsafeReadRelation, cast)
		}
		scope.casts[cast] = oid
	}
	return scope, nil
}

func postgresTypeName(name string) string {
	switch name {
	case "bigint":
		return "int8"
	case "boolean":
		return "bool"
	case "decimal":
		return "numeric"
	case "integer":
		return "int4"
	case "real":
		return "float4"
	case "smallint":
		return "int2"
	default:
		return name
	}
}

func (scope nativeDependencyScope) validate(plan Plan) error {
	for _, statement := range plan.Statements {
		for _, function := range statement.Functions {
			if _, ok := scope.functions[strings.ToLower(function.Name)]; !ok {
				return fmt.Errorf("%w: function %s is outside the validated builtin OIDs", ErrSQLRejected, function.Name)
			}
		}
		for _, operator := range statement.Operators {
			if _, ok := scope.operators[strings.ToUpper(operator)]; !ok {
				return fmt.Errorf("%w: operator %s is outside the validated builtin OIDs", ErrSQLRejected, operator)
			}
		}
		for _, cast := range statement.Casts {
			if scope.casts[cast] == 0 {
				return fmt.Errorf("%w: cast %s is outside the validated builtin OIDs", ErrSQLRejected, cast)
			}
		}
	}
	return nil
}
