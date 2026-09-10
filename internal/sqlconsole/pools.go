package sqlconsole

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

var (
	ErrCatalogChanged     = errors.New("SQL console catalog changed after role validation")
	ErrUnsafeDatabaseRole = errors.New("SQL console database role is not least-privileged")
	ErrUnsafeReadRelation = errors.New("SQL console read relation has unsafe dependencies")
	ErrUnsafeWriteTarget  = errors.New("SQL console write target has unsafe side effects")
)

type WriteTarget struct {
	Schema  string
	Table   string
	Columns []string
}

type PoolOptions struct {
	CompilerURL           string
	ReadURL               string
	WriterURL             string
	AllowInsecureDatabase bool
	Catalog               schema.Catalog
	CatalogVersion        func(context.Context) (int64, error)
	WriteTargets          []WriteTarget
}

type targetSnapshot struct {
	Reference       RelationRef
	OID             uint32
	Columns         map[string]uint32
	PhysicalColumns map[string]uint32
}

type managedReadSnapshot struct {
	Reference RelationRef
	OID       uint32
	Columns   map[string]struct{}
	View      bool
}

type Pools struct {
	compiler       *database.Pool
	read           *database.Pool
	writer         *database.Pool
	targets        map[RelationRef]targetSnapshot
	managed        map[RelationRef]managedReadSnapshot
	dependencies   nativeDependencyScope
	catalog        schema.Catalog
	catalogVersion func(context.Context) (int64, error)
	closeOnce      sync.Once
}

func OpenPools(ctx context.Context, options PoolOptions) (*Pools, error) {
	if ctx == nil || options.CompilerURL == "" || options.ReadURL == "" || options.WriterURL == "" || options.CatalogVersion == nil {
		return nil, fmt.Errorf("open SQL console pools: configuration is incomplete")
	}
	targets, err := normalizeWriteTargets(options.WriteTargets)
	if err != nil {
		return nil, err
	}
	pools := &Pools{catalog: options.Catalog, catalogVersion: options.CatalogVersion}
	defer func() {
		if err != nil {
			pools.Close()
		}
	}()
	pools.compiler, err = database.Open(ctx, options.CompilerURL, MaximumTimeout, options.AllowInsecureDatabase)
	if err != nil {
		return nil, fmt.Errorf("open SQL console compiler pool: %w", err)
	}
	pools.read, err = database.Open(ctx, options.ReadURL, MaximumTimeout, options.AllowInsecureDatabase)
	if err != nil {
		return nil, fmt.Errorf("open SQL console read pool: %w", err)
	}
	pools.writer, err = database.Open(ctx, options.WriterURL, MaximumTimeout, options.AllowInsecureDatabase)
	if err != nil {
		return nil, fmt.Errorf("open SQL console writer pool: %w", err)
	}

	for _, role := range []struct {
		name      string
		pool      *database.Pool
		readOnly  bool
		temporary bool
	}{
		{name: "compiler", pool: pools.compiler, temporary: true},
		{name: "read", pool: pools.read, readOnly: true},
		{name: "writer", pool: pools.writer},
	} {
		if roleErr := validateRole(ctx, role.pool, role.name, role.readOnly, role.temporary); roleErr != nil {
			err = roleErr
			return nil, roleErr
		}
	}
	pools.dependencies, err = inspectNativeDependencies(ctx, pools.compiler)
	if err != nil {
		return nil, err
	}

	pools.targets, err = inspectWriteTargets(ctx, pools.compiler, targets)
	if err != nil {
		return nil, err
	}
	pools.managed, err = inspectManagedReadRelations(ctx, pools.compiler, pools.catalog)
	if err != nil {
		return nil, err
	}
	if err = validatePoolPrivileges(ctx, pools, targets); err != nil {
		return nil, err
	}
	if err = pools.validateCatalogVersion(ctx); err != nil {
		return nil, err
	}
	return pools, nil
}

func (pools *Pools) Close() {
	if pools == nil {
		return
	}
	pools.closeOnce.Do(func() {
		if pools.writer != nil {
			pools.writer.Close()
		}
		if pools.read != nil {
			pools.read.Close()
		}
		if pools.compiler != nil {
			pools.compiler.Close()
		}
	})
}

func (pools *Pools) acquireRead(ctx context.Context) (*pgxpool.Conn, error) {
	return pools.read.Acquire(ctx)
}

func (pools *Pools) acquireWriter(ctx context.Context) (*pgxpool.Conn, error) {
	return pools.writer.Acquire(ctx)
}

func normalizeWriteTargets(input []WriteTarget) (map[RelationRef]WriteTarget, error) {
	result := make(map[RelationRef]WriteTarget, len(input))
	for _, target := range input {
		reference := RelationRef{Schema: target.Schema, Name: target.Table}
		_, blocked := blockedWriteSchemas[strings.ToLower(target.Schema)]
		if target.Schema == "" || target.Table == "" || blocked || strings.HasPrefix(strings.ToLower(target.Schema), "pg_temp_") {
			return nil, fmt.Errorf("%w: %s.%s is not an extension-owned target", ErrUnsafeWriteTarget, target.Schema, target.Table)
		}
		if len(target.Columns) == 0 || hasDuplicateStrings(target.Columns) {
			return nil, fmt.Errorf("%w: %s.%s columns must be non-empty and unique", ErrUnsafeWriteTarget, target.Schema, target.Table)
		}
		if _, duplicate := result[reference]; duplicate {
			return nil, fmt.Errorf("%w: duplicate target %s.%s", ErrUnsafeWriteTarget, target.Schema, target.Table)
		}
		target.Columns = sortedStrings(target.Columns)
		result[reference] = target
	}
	return result, nil
}

func validateRole(ctx context.Context, pool *database.Pool, label string, readOnly, temporary bool) error {
	var (
		name, searchPath                                                           string
		superuser, inherit, createRole, createDB, canLogin, replication, bypassRLS bool
		sameUser, actualReadOnly, actualTemporary                                  bool
	)
	err := pool.QueryRow(ctx, `select r.rolname,r.rolsuper,r.rolinherit,r.rolcreaterole,r.rolcreatedb,
		r.rolcanlogin,r.rolreplication,r.rolbypassrls,current_user=session_user,
		current_setting('search_path'),current_setting('transaction_read_only')::boolean,
		has_database_privilege(current_user,current_database(),'TEMP')
		from pg_catalog.pg_roles r where r.rolname=current_user`).Scan(
		&name, &superuser, &inherit, &createRole, &createDB, &canLogin, &replication, &bypassRLS,
		&sameUser, &searchPath, &actualReadOnly, &actualTemporary,
	)
	if err != nil {
		return fmt.Errorf("%w: inspect %s role: %v", ErrUnsafeDatabaseRole, label, err)
	}
	if name == "" || superuser || inherit || createRole || createDB || !canLogin || replication || bypassRLS ||
		!sameUser || searchPath != "pg_catalog" || actualReadOnly != readOnly || actualTemporary != temporary {
		return fmt.Errorf("%w: %s role attributes or session defaults", ErrUnsafeDatabaseRole, label)
	}
	var memberships, ownerships, creatableSchemas, executableUserFunctions int
	if err := pool.QueryRow(ctx, `select count(*) from pg_catalog.pg_auth_members m
		join pg_catalog.pg_roles r on r.oid=m.member where r.rolname=current_user`).Scan(&memberships); err != nil {
		return fmt.Errorf("%w: inspect %s memberships: %v", ErrUnsafeDatabaseRole, label, err)
	}
	if err := pool.QueryRow(ctx, `select
		(select count(*) from pg_catalog.pg_database where datdba=(select oid from pg_roles where rolname=current_user))+
		(select count(*) from pg_catalog.pg_namespace where nspowner=(select oid from pg_roles where rolname=current_user) and nspname !~ '^pg_temp_')+
		(select count(*) from pg_catalog.pg_class where relowner=(select oid from pg_roles where rolname=current_user))+
		(select count(*) from pg_catalog.pg_proc where proowner=(select oid from pg_roles where rolname=current_user))+
		(select count(*) from pg_catalog.pg_type where typowner=(select oid from pg_roles where rolname=current_user))`).Scan(&ownerships); err != nil {
		return fmt.Errorf("%w: inspect %s ownership: %v", ErrUnsafeDatabaseRole, label, err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from pg_catalog.pg_namespace
		where nspname !~ '^pg_temp_' and has_schema_privilege(current_user,oid,'CREATE')`).Scan(&creatableSchemas); err != nil {
		return fmt.Errorf("%w: inspect %s schema privileges: %v", ErrUnsafeDatabaseRole, label, err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from pg_catalog.pg_proc p
		join pg_catalog.pg_namespace n on n.oid=p.pronamespace
		where n.nspname not in ('pg_catalog','information_schema','pgbase_meta','pgbase_relation') and n.nspname !~ '^pg_(toast|temp_)'
		and has_function_privilege(current_user,p.oid,'EXECUTE')`).Scan(&executableUserFunctions); err != nil {
		return fmt.Errorf("%w: inspect %s function privileges: %v", ErrUnsafeDatabaseRole, label, err)
	}
	if memberships != 0 || ownerships != 0 || creatableSchemas != 0 || executableUserFunctions != 0 {
		return fmt.Errorf("%w: %s role membership=%d ownership=%d creatable_schemas=%d executable_user_functions=%d", ErrUnsafeDatabaseRole, label, memberships, ownerships, creatableSchemas, executableUserFunctions)
	}
	return nil
}

func inspectWriteTargets(ctx context.Context, pool *database.Pool, configured map[RelationRef]WriteTarget) (map[RelationRef]targetSnapshot, error) {
	result := make(map[RelationRef]targetSnapshot, len(configured))
	allowedOIDs := make(map[uint32]struct{}, len(configured))
	for reference, target := range configured {
		var snapshot targetSnapshot
		var kind, persistence string
		var partitioned, rowSecurity, forceRowSecurity, inherited, userTrigger, userRule, policy bool
		err := pool.QueryRow(ctx, `select c.oid,c.relkind::text,c.relpersistence::text,c.relispartition,
			c.relrowsecurity,c.relforcerowsecurity,
			exists(select 1 from pg_catalog.pg_inherits where inhrelid=c.oid or inhparent=c.oid),
			exists(select 1 from pg_catalog.pg_trigger where tgrelid=c.oid and not tgisinternal),
			exists(select 1 from pg_catalog.pg_rewrite where ev_class=c.oid and rulename <> '_RETURN'),
			exists(select 1 from pg_catalog.pg_policy where polrelid=c.oid)
			from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
			where n.nspname=$1 and c.relname=$2`, reference.Schema, reference.Name).Scan(
			&snapshot.OID, &kind, &persistence, &partitioned, &rowSecurity, &forceRowSecurity,
			&inherited, &userTrigger, &userRule, &policy,
		)
		if err != nil {
			return nil, fmt.Errorf("%w: inspect %s.%s: %v", ErrUnsafeWriteTarget, reference.Schema, reference.Name, err)
		}
		if kind != "r" || persistence != "p" || partitioned || rowSecurity || forceRowSecurity || inherited || userTrigger || userRule || policy {
			return nil, fmt.Errorf("%w: %s.%s is not an ordinary side-effect-free base table", ErrUnsafeWriteTarget, reference.Schema, reference.Name)
		}
		snapshot.Reference = reference
		snapshot.PhysicalColumns = make(map[string]uint32)
		rows, err := pool.Query(ctx, `select a.attname,a.attnum,t.oid,n.nspname,t.typname,
			a.atthasdef,a.attgenerated::text,a.attidentity::text
			from pg_catalog.pg_attribute a join pg_catalog.pg_type t on t.oid=a.atttypid
			join pg_catalog.pg_namespace n on n.oid=t.typnamespace
			where a.attrelid=$1 and a.attnum>0 and not a.attisdropped order by a.attnum`, snapshot.OID)
		if err != nil {
			return nil, fmt.Errorf("%w: inspect %s.%s columns: %v", ErrUnsafeWriteTarget, reference.Schema, reference.Name, err)
		}
		for rows.Next() {
			var name, typeSchema, typeName, generated, identity string
			var number int16
			var typeOID uint32
			var hasDefault bool
			if err := rows.Scan(&name, &number, &typeOID, &typeSchema, &typeName, &hasDefault, &generated, &identity); err != nil {
				rows.Close()
				return nil, err
			}
			if typeSchema != "pg_catalog" || !safeType(typeName) || hasDefault || generated != "" || identity != "" {
				rows.Close()
				return nil, fmt.Errorf("%w: %s.%s column %s has an unsafe type or expression", ErrUnsafeWriteTarget, reference.Schema, reference.Name, name)
			}
			snapshot.PhysicalColumns[name] = typeOID
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		snapshot.Columns = make(map[string]uint32, len(target.Columns))
		for _, column := range target.Columns {
			typeOID, exists := snapshot.PhysicalColumns[column]
			if !exists {
				return nil, fmt.Errorf("%w: configured column %s.%s.%s does not exist", ErrUnsafeWriteTarget, reference.Schema, reference.Name, column)
			}
			snapshot.Columns[column] = typeOID
		}
		result[reference] = snapshot
		allowedOIDs[snapshot.OID] = struct{}{}
	}
	for reference, snapshot := range result {
		rows, err := pool.Query(ctx, `select contype::text,conrelid,confrelid,confupdtype::text,confdeltype::text,convalidated
			from pg_catalog.pg_constraint where conrelid=$1 or confrelid=$1`, snapshot.OID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var kind, updateAction, deleteAction string
			var relationOID, foreignOID uint32
			var validated bool
			if err := rows.Scan(&kind, &relationOID, &foreignOID, &updateAction, &deleteAction, &validated); err != nil {
				rows.Close()
				return nil, err
			}
			switch kind {
			case "p", "u", "n":
				if !validated {
					rows.Close()
					return nil, fmt.Errorf("%w: %s.%s has an unvalidated constraint", ErrUnsafeWriteTarget, reference.Schema, reference.Name)
				}
			case "f":
				_, relationAllowed := allowedOIDs[relationOID]
				_, foreignAllowed := allowedOIDs[foreignOID]
				if !validated || !relationAllowed || !foreignAllowed || !safeForeignKeyAction(updateAction) || !safeForeignKeyAction(deleteAction) {
					rows.Close()
					return nil, fmt.Errorf("%w: %s.%s has an out-of-scope foreign key effect", ErrUnsafeWriteTarget, reference.Schema, reference.Name)
				}
			default:
				rows.Close()
				return nil, fmt.Errorf("%w: %s.%s constraint kind %s is not allowed", ErrUnsafeWriteTarget, reference.Schema, reference.Name, kind)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return result, nil
}

func safeType(name string) bool {
	_, allowed := allowedCasts[strings.ToLower(name)]
	return allowed || name == "bpchar"
}

func safeForeignKeyAction(action string) bool { return action == "a" || action == "r" }

func validatePoolPrivileges(ctx context.Context, pools *Pools, targets map[RelationRef]WriteTarget) error {
	managed := managedReadColumns(pools.catalog)
	for label, pool := range map[string]*database.Pool{"compiler": pools.compiler, "read": pools.read, "writer": pools.writer} {
		for relation, columns := range managed {
			if err := validateColumnReads(ctx, pool, label, relation, columns); err != nil {
				return err
			}
			if err := requireNoTableWrites(ctx, pool, label, relation); err != nil {
				return err
			}
		}
	}
	for relation, target := range targets {
		for label, pool := range map[string]*database.Pool{"compiler": pools.compiler, "read": pools.read, "writer": pools.writer} {
			if err := validateColumnReads(ctx, pool, label, relation, stringSet(target.Columns)); err != nil {
				return err
			}
		}
		for label, pool := range map[string]*database.Pool{"compiler": pools.compiler, "read": pools.read} {
			if err := requireNoTableWrites(ctx, pool, label, relation); err != nil {
				return err
			}
		}
		if err := validateWriterPrivileges(ctx, pools.writer, relation, stringSet(target.Columns)); err != nil {
			return err
		}
	}
	allowedRelations := make(map[RelationRef]struct{}, len(managed)+len(targets)+1)
	for relation := range managed {
		allowedRelations[relation] = struct{}{}
	}
	for relation := range targets {
		allowedRelations[relation] = struct{}{}
	}
	allowedRelations[RelationRef{Schema: "pgbase_meta", Name: "sql_console_write_receipts"}] = struct{}{}
	for label, pool := range map[string]*database.Pool{"compiler": pools.compiler, "read": pools.read, "writer": pools.writer} {
		if err := validateNoUnexpectedRelationPrivileges(ctx, pool, label, allowedRelations); err != nil {
			return err
		}
	}
	return validateReceiptPrivileges(ctx, pools)
}

func validateReceiptPrivileges(ctx context.Context, pools *Pools) error {
	relation := RelationRef{Schema: "pgbase_meta", Name: "sql_console_write_receipts"}
	insertColumns := stringSet([]string{
		"execution_id", "administrator_id", "idempotency_hash", "request_hash",
		"row_count", "affected_rows", "result_bytes", "committed_at",
	})
	for label, pool := range map[string]*database.Pool{"compiler": pools.compiler, "read": pools.read, "writer": pools.writer} {
		var schemaUsage bool
		if err := pool.QueryRow(ctx, `select has_schema_privilege(current_user,$1,'USAGE')`, relation.Schema).Scan(&schemaUsage); err != nil {
			return fmt.Errorf("%w: inspect %s receipt schema privilege: %v", ErrUnsafeDatabaseRole, label, err)
		}
		if schemaUsage != (label == "writer") {
			return fmt.Errorf("%w: %s receipt schema privilege", ErrUnsafeDatabaseRole, label)
		}
		rows, err := pool.Query(ctx, `select a.attname,
			has_column_privilege(current_user,c.oid,a.attnum,'SELECT'),
			has_column_privilege(current_user,c.oid,a.attnum,'INSERT'),
			has_column_privilege(current_user,c.oid,a.attnum,'UPDATE')
			from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
			join pg_catalog.pg_attribute a on a.attrelid=c.oid and a.attnum>0 and not a.attisdropped
			where n.nspname=$1 and c.relname=$2 order by a.attnum`, relation.Schema, relation.Name)
		if err != nil {
			return err
		}
		for rows.Next() {
			var column string
			var readable, insertable, updateable bool
			if err := rows.Scan(&column, &readable, &insertable, &updateable); err != nil {
				rows.Close()
				return err
			}
			_, wanted := insertColumns[column]
			if readable || updateable || insertable != (label == "writer" && wanted) {
				rows.Close()
				return fmt.Errorf("%w: %s receipt column privilege on %s", ErrUnsafeDatabaseRole, label, column)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		var unsafe bool
		if err := pool.QueryRow(ctx, `select
			has_table_privilege(current_user,c.oid,'DELETE') or has_table_privilege(current_user,c.oid,'TRUNCATE') or
			has_table_privilege(current_user,c.oid,'REFERENCES') or has_table_privilege(current_user,c.oid,'TRIGGER')
			from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
			where n.nspname=$1 and c.relname=$2`, relation.Schema, relation.Name).Scan(&unsafe); err != nil || unsafe {
			return fmt.Errorf("%w: %s receipt table privilege", ErrUnsafeDatabaseRole, label)
		}
	}
	return nil
}

func managedReadColumns(catalog schema.Catalog) map[RelationRef]map[string]struct{} {
	result := make(map[RelationRef]map[string]struct{}, len(catalog.Collections))
	for _, collection := range catalog.Collections {
		columns := stringSet([]string{"id", "created_at", "updated_at"})
		for _, field := range collection.Fields {
			if field.Type == schema.TypePassword || field.Hidden || field.Type == schema.TypeFile ||
				(field.Type == schema.TypeRelation && field.Relation != nil && field.Relation.Cardinality == schema.RelationMultiple) {
				continue
			}
			columns[field.ColumnName] = struct{}{}
		}
		result[RelationRef{Schema: "app", Name: collection.TableName}] = columns
	}
	return result
}

func validateColumnReads(ctx context.Context, pool *database.Pool, label string, relation RelationRef, expected map[string]struct{}) error {
	rows, err := pool.Query(ctx, `select a.attname,has_column_privilege(current_user,c.oid,a.attnum,'SELECT')
		from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
		join pg_catalog.pg_attribute a on a.attrelid=c.oid and a.attnum>0 and not a.attisdropped
		where n.nspname=$1 and c.relname=$2 order by a.attnum`, relation.Schema, relation.Name)
	if err != nil {
		return fmt.Errorf("%w: inspect %s read privilege on %s.%s: %v", ErrUnsafeDatabaseRole, label, relation.Schema, relation.Name, err)
	}
	seen := make(map[string]struct{})
	for rows.Next() {
		var column string
		var readable bool
		if err := rows.Scan(&column, &readable); err != nil {
			rows.Close()
			return err
		}
		_, wanted := expected[column]
		if readable != wanted {
			rows.Close()
			return fmt.Errorf("%w: %s role read privilege on %s.%s.%s", ErrUnsafeDatabaseRole, label, relation.Schema, relation.Name, column)
		}
		seen[column] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for column := range expected {
		if _, exists := seen[column]; !exists {
			return fmt.Errorf("%w: expected column %s.%s.%s is absent", ErrUnsafeDatabaseRole, relation.Schema, relation.Name, column)
		}
	}
	return nil
}

func requireNoTableWrites(ctx context.Context, pool *database.Pool, label string, relation RelationRef) error {
	var writable bool
	err := pool.QueryRow(ctx, `select
		has_table_privilege(current_user,c.oid,'INSERT') or has_table_privilege(current_user,c.oid,'UPDATE') or
		has_table_privilege(current_user,c.oid,'DELETE') or has_table_privilege(current_user,c.oid,'TRUNCATE') or
		has_table_privilege(current_user,c.oid,'REFERENCES') or has_table_privilege(current_user,c.oid,'TRIGGER')
		from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
		where n.nspname=$1 and c.relname=$2`, relation.Schema, relation.Name).Scan(&writable)
	if err != nil || writable {
		return fmt.Errorf("%w: %s role can write %s.%s", ErrUnsafeDatabaseRole, label, relation.Schema, relation.Name)
	}
	return nil
}

func validateWriterPrivileges(ctx context.Context, pool *database.Pool, relation RelationRef, expected map[string]struct{}) error {
	rows, err := pool.Query(ctx, `select a.attname,
		has_column_privilege(current_user,c.oid,a.attnum,'INSERT'),
		has_column_privilege(current_user,c.oid,a.attnum,'UPDATE')
		from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
		join pg_catalog.pg_attribute a on a.attrelid=c.oid and a.attnum>0 and not a.attisdropped
		where n.nspname=$1 and c.relname=$2 order by a.attnum`, relation.Schema, relation.Name)
	if err != nil {
		return err
	}
	for rows.Next() {
		var column string
		var insertable, updateable bool
		if err := rows.Scan(&column, &insertable, &updateable); err != nil {
			rows.Close()
			return err
		}
		_, wanted := expected[column]
		if insertable != wanted || updateable != wanted {
			rows.Close()
			return fmt.Errorf("%w: writer column privilege on %s.%s.%s", ErrUnsafeDatabaseRole, relation.Schema, relation.Name, column)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	var deleteAllowed, unsafe bool
	err = pool.QueryRow(ctx, `select has_table_privilege(current_user,c.oid,'DELETE'),
		has_table_privilege(current_user,c.oid,'TRUNCATE') or has_table_privilege(current_user,c.oid,'REFERENCES') or
		has_table_privilege(current_user,c.oid,'TRIGGER')
		from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
		where n.nspname=$1 and c.relname=$2`, relation.Schema, relation.Name).Scan(&deleteAllowed, &unsafe)
	if err != nil || !deleteAllowed || unsafe {
		return fmt.Errorf("%w: writer table privilege on %s.%s", ErrUnsafeDatabaseRole, relation.Schema, relation.Name)
	}
	return nil
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func (pools *Pools) ValidatePlan(ctx context.Context, plan Plan) error {
	if err := pools.validateCatalogVersion(ctx); err != nil {
		return err
	}
	if err := pools.dependencies.validate(plan); err != nil {
		return err
	}
	for _, statement := range plan.Statements {
		for _, relation := range statement.Relations {
			if _, ok := pools.managed[relation]; ok {
				continue
			}
			if _, ok := pools.targets[relation]; !ok {
				return fmt.Errorf("%w: relation %s.%s is outside the allowed read scope", ErrSQLRejected, relation.Schema, relation.Name)
			}
		}
		if statement.WriteTarget == nil {
			continue
		}
		target, ok := pools.targets[*statement.WriteTarget]
		if !ok {
			return fmt.Errorf("%w: write target %s.%s is not configured", ErrSQLRejected, statement.WriteTarget.Schema, statement.WriteTarget.Name)
		}
		for _, column := range statement.WriteColumns {
			if _, ok := target.Columns[column]; !ok {
				return fmt.Errorf("%w: write column %s.%s.%s is not configured", ErrSQLRejected, statement.WriteTarget.Schema, statement.WriteTarget.Name, column)
			}
		}
	}
	return nil
}

func (pools *Pools) validateCatalogVersion(ctx context.Context) error {
	if pools == nil || pools.catalogVersion == nil {
		return ErrCatalogChanged
	}
	current, err := pools.catalogVersion(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCatalogChanged, err)
	}
	if current != pools.catalog.Version {
		return fmt.Errorf("%w: validated=%d current=%d", ErrCatalogChanged, pools.catalog.Version, current)
	}
	return nil
}

func sortedTargetSnapshots(values map[RelationRef]targetSnapshot) []targetSnapshot {
	result := make([]targetSnapshot, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].OID < result[right].OID })
	return result
}
