package userauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

var authRuleJSONSegment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type authRuleCollection struct {
	ID     uuid.UUID
	Name   string
	Fields []schema.FieldInput
}

func EvaluateAuthRecordRuleInTx(ctx context.Context, tx database.DBTX, collectionID uuid.UUID, rule string, ruleContext AuthRuleContext) (bool, error) {
	trimmed := strings.TrimSpace(rule)
	if trimmed == "" || trimmed == "true" {
		return true, nil
	}
	if trimmed == "false" {
		return false, nil
	}
	resolver, err := loadAuthRuleResolver(ctx, tx, collectionID)
	if err != nil {
		return false, err
	}
	parsed, err := query.ParsePocketBaseAccessRule(rule)
	if err != nil {
		return false, ErrValidation
	}
	request := authPBRequestContext(ruleContext)
	if err := loadAuthRuleActor(ctx, tx, collectionID, ruleContext.Principal, &request); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	predicate, args, err := accessrule.CompilePocketBase(parsed, accessrule.PBCompileContext{
		Resolver: resolver, Principal: ruleContext.Principal, Request: &request, ArgOffset: 2,
	})
	if err != nil {
		return false, ErrValidation
	}
	queryArgs := append([]any{collectionID, ruleContext.Current.ID}, args...)
	var allowed bool
	err = tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.users AS u left join pgbase_meta.user_auth_profiles AS p on p.user_id=u.id where u.auth_collection_id=$1 and u.id=$2 and (`+predicate+`))`, queryArgs...).Scan(&allowed)
	return allowed, err
}

func loadAuthRuleActor(ctx context.Context, db database.DBTX, collectionID uuid.UUID, principal identity.Subject, request *accessrule.RequestContext) error {
	if principal.Kind != identity.User || principal.ID == uuid.Nil {
		return nil
	}
	var email string
	var emailVisibility, verified, active bool
	var raw []byte
	err := db.QueryRow(ctx, `select email,email_visibility,verified,active,coalesce(p.attributes,'{}') from pgbase_meta.users u left join pgbase_meta.user_auth_profiles p on p.user_id=u.id where u.auth_collection_id=$1 and u.id=$2 and u.active`, collectionID, principal.ID).Scan(&email, &emailVisibility, &verified, &active, &raw)
	if err != nil {
		return err
	}
	attributes := map[string]any{}
	if json.Unmarshal(raw, &attributes) != nil {
		return ErrValidation
	}
	request.Auth["id"] = principal.ID.String()
	request.Auth["email"] = email
	request.Auth["emailVisibility"] = emailVisibility
	request.Auth["verified"] = verified
	request.Auth["active"] = active
	for name, value := range attributes {
		request.Auth[name] = value
	}
	return nil
}

func (s *Service) evaluateAuthRecordRuleDB(ctx context.Context, collectionID uuid.UUID, rule string, ruleContext AuthRuleContext) bool {
	allowed := false
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if ruleContext.Current.ID != uuid.Nil {
			var present bool
			if err := tx.QueryRow(ctx, `select true from pgbase_meta.users where auth_collection_id=$1 and id=$2 for share`, collectionID, ruleContext.Current.ID).Scan(&present); err != nil {
				return err
			}
		}
		var err error
		allowed, err = EvaluateAuthRecordRuleInTx(ctx, tx, collectionID, rule, ruleContext)
		return err
	})
	return err == nil && allowed
}

func loadAuthRuleResolver(ctx context.Context, tx database.DBTX, rootID uuid.UUID) (authRuleResolver, error) {
	rows, err := tx.Query(ctx, `select id,name,fields_json from pgbase_meta.auth_collections`)
	if err != nil {
		return authRuleResolver{}, err
	}
	defer rows.Close()
	resolver := authRuleResolver{collections: map[string]authRuleCollection{}}
	for rows.Next() {
		var value authRuleCollection
		var raw []byte
		if err := rows.Scan(&value.ID, &value.Name, &raw); err != nil || json.Unmarshal(raw, &value.Fields) != nil {
			return authRuleResolver{}, errors.New("invalid auth rule metadata")
		}
		resolver.collections[value.Name] = value
		if value.ID == rootID {
			resolver.root = value
		}
	}
	if err := rows.Err(); err != nil {
		return authRuleResolver{}, err
	}
	if resolver.root.ID == uuid.Nil {
		return authRuleResolver{}, ErrNotFound
	}
	return resolver, nil
}

func authPBRequestContext(context AuthRuleContext) accessrule.RequestContext {
	request := accessrule.RequestContext{Present: map[string]bool{}, Changed: map[string]bool{}, Body: map[string]json.RawMessage{}, Auth: map[string]any{
		"id": context.Principal.ID.String(), "email": context.AuthEmail,
	}}
	for name, value := range context.Request {
		request.Present[name] = true
		current := context.Current.Fields[name]
		switch name {
		case "email":
			current = context.Current.Email
		case "emailVisibility":
			current = context.Current.EmailVisibility
		case "verified":
			current = context.Current.Verified
		case "active":
			current = context.Current.Active
		case "password":
			current = ""
		}
		request.Changed[name] = !authRuleValuesEqual(current, value)
		raw, err := json.Marshal(value)
		if err == nil {
			request.Body[name] = raw
		}
	}
	return request
}

func (s *Service) authorizeSessionTx(ctx context.Context, db database.DBTX, user User) (bool, error) {
	collectionID := user.AuthCollectionID
	if collectionID == uuid.Nil {
		collectionID = DefaultCollectionID
	}
	var rule *string
	var raw []byte
	var current AuthRecord
	err := db.QueryRow(ctx, `select c.auth_rule,u.id,u.auth_collection_id,u.email,u.email_visibility,u.verified,u.active,coalesce(p.attributes,'{}'),u.created_at,u.updated_at from pgbase_meta.users u join pgbase_meta.auth_collections c on c.id=u.auth_collection_id left join pgbase_meta.user_auth_profiles p on p.user_id=u.id where u.auth_collection_id=$1 and u.id=$2 for share of u`, collectionID, user.ID).Scan(
		&rule, &current.ID, &current.AuthCollectionID, &current.Email, &current.EmailVisibility, &current.Verified, &current.Active, &raw, &current.CreatedAt, &current.UpdatedAt,
	)
	if err != nil {
		return false, err
	}
	if json.Unmarshal(raw, &current.Fields) != nil {
		return false, ErrValidation
	}
	if rule == nil {
		return false, nil
	}
	return EvaluateAuthRecordRuleInTx(ctx, db, collectionID, *rule, AuthRuleContext{
		Principal: identity.Subject{Kind: identity.User, ID: current.ID}, AuthEmail: current.Email, Current: current,
	})
}

// enforceAuthRuleForSession is the pre-consumption form of the same
// transaction-bound guard used by insertSession. OAuth state, OTP codes, MFA
// challenges, and refresh tokens remain reusable when this guard denies.
func (s *Service) enforceAuthRuleForSession(ctx context.Context, db database.DBTX, collectionID, userID uuid.UUID) error {
	allowed, err := s.authorizeSessionTx(ctx, db, User{ID: userID, AuthCollectionID: collectionID})
	if err != nil {
		return err
	}
	if !allowed {
		return ErrInvalidCredentials
	}
	return nil
}

// authRuleResolver maps the virtual auth record aliases u/p to trusted SQL.
// All collection and field metadata is loaded by the caller in its transaction
// before compilation; the resolver performs no database work.
type authRuleResolver struct {
	root        authRuleCollection
	collections map[string]authRuleCollection
}

func (r authRuleResolver) Resolve(path string) (accessrule.PBOperand, error) {
	return r.resolve(r.root, "u", "p", path)
}

func (r authRuleResolver) ResolveCollection(name, path string) (accessrule.PBOperand, error) {
	collection, ok := r.collections[name]
	if !ok {
		return accessrule.PBOperand{}, errors.New("unknown auth collection")
	}
	// @collection operands are multi-valued by definition. The shared compiler
	// owns their any/each semantics while this adapter supplies the bounded row
	// source and typed value expression.
	value, err := r.resolve(collection, "acu", "acp", path)
	if err != nil || value.Cardinality != accessrule.PBScalar {
		return accessrule.PBOperand{}, errors.New("invalid auth collection operand")
	}
	return accessrule.PBOperand{Kind: value.Kind, Cardinality: accessrule.PBMany, Multi: &accessrule.PBMulti{
		From:      `pgbase_meta.users AS acu LEFT JOIN pgbase_meta.user_auth_profiles AS acp ON acp.user_id=acu.id`,
		Predicate: fmt.Sprintf("acu.auth_collection_id='%s'::uuid", collection.ID),
		ValueSQL:  value.SQL,
	}}, nil
}

func (r authRuleResolver) resolve(collection authRuleCollection, userAlias, profileAlias, path string) (accessrule.PBOperand, error) {
	parts := strings.Split(path, ".")
	if len(parts) == 0 || !authRuleJSONSegment.MatchString(parts[0]) {
		return accessrule.PBOperand{}, errors.New("invalid auth rule path")
	}
	switch parts[0] {
	case "id":
		return scalarOperand(userAlias+`.id`, accessrule.PBUUID, parts[1:])
	case "email":
		return scalarOperand(userAlias+`.email`, accessrule.PBText, parts[1:])
	case "emailVisibility":
		return scalarOperand(userAlias+`.email_visibility`, accessrule.PBBool, parts[1:])
	case "password":
		// The persisted hash and plaintext are never rule operands. This typed
		// placeholder exists only so request.body.password modifiers compile.
		return scalarOperand(`NULL::text`, accessrule.PBText, parts[1:])
	case "verified":
		return scalarOperand(userAlias+`.verified`, accessrule.PBBool, parts[1:])
	case "active":
		return scalarOperand(userAlias+`.active`, accessrule.PBBool, parts[1:])
	case "created":
		return scalarOperand(userAlias+`.created_at`, accessrule.PBTime, parts[1:])
	case "updated":
		return scalarOperand(userAlias+`.updated_at`, accessrule.PBTime, parts[1:])
	}
	field, ok := authRuleField(collection.Fields, parts[0])
	if !ok {
		return accessrule.PBOperand{}, errors.New("unknown auth field")
	}
	if field.Type == schema.TypeRelation {
		return r.resolveRelation(collection, field, userAlias, profileAlias, parts[1:])
	}
	return authJSONOperand(profileAlias, parts, field.Type)
}

func (r authRuleResolver) resolveRelation(_ authRuleCollection, field schema.FieldInput, _ string, profileAlias string, tail []string) (accessrule.PBOperand, error) {
	if field.Relation == nil {
		return accessrule.PBOperand{}, errors.New("invalid relation metadata")
	}
	target, ok := r.collections[field.Relation.TargetCollection]
	if !ok {
		return accessrule.PBOperand{}, errors.New("unknown relation target")
	}
	key := field.Name
	if field.Relation.Cardinality == schema.RelationMultiple {
		valueSQL := "arv.value::uuid"
		kind := accessrule.PBUUID
		from := fmt.Sprintf(`jsonb_array_elements_text(coalesce(%s.attributes->'%s','[]'::jsonb)) AS arv(value)`, profileAlias, key)
		predicate := "true"
		if len(tail) > 0 {
			resolved, err := r.resolve(target, "aru", "arp", strings.Join(tail, "."))
			if err != nil || resolved.Cardinality != accessrule.PBScalar {
				return accessrule.PBOperand{}, errors.New("invalid relation path")
			}
			from += ` JOIN pgbase_meta.users AS aru ON aru.id=arv.value::uuid LEFT JOIN pgbase_meta.user_auth_profiles AS arp ON arp.user_id=aru.id`
			predicate = fmt.Sprintf("aru.auth_collection_id='%s'::uuid", target.ID)
			valueSQL, kind = resolved.SQL, resolved.Kind
		}
		return accessrule.PBOperand{Kind: kind, Cardinality: accessrule.PBMany, Multi: &accessrule.PBMulti{From: from, Predicate: predicate, ValueSQL: valueSQL}}, nil
	}
	if len(tail) == 0 {
		return accessrule.PBOperand{SQL: fmt.Sprintf("(%s.attributes->>'%s')::uuid", profileAlias, key), Kind: accessrule.PBUUID, Cardinality: accessrule.PBScalar}, nil
	}
	resolved, err := r.resolve(target, "aru", "arp", strings.Join(tail, "."))
	if err != nil || resolved.Cardinality != accessrule.PBScalar {
		return accessrule.PBOperand{}, errors.New("invalid relation path")
	}
	resolved.SQL = fmt.Sprintf(`(SELECT %s FROM pgbase_meta.users AS aru LEFT JOIN pgbase_meta.user_auth_profiles AS arp ON arp.user_id=aru.id WHERE aru.auth_collection_id='%s'::uuid AND aru.id=(%s.attributes->>'%s')::uuid)`, resolved.SQL, target.ID, profileAlias, key)
	return resolved, nil
}

func authJSONOperand(alias string, parts []string, kind schema.FieldType) (accessrule.PBOperand, error) {
	for _, part := range parts {
		if !authRuleJSONSegment.MatchString(part) {
			return accessrule.PBOperand{}, errors.New("invalid JSON path")
		}
	}
	path := strings.Join(parts, ",")
	jsonSQL := fmt.Sprintf("%s.attributes#>'{%s}'", alias, path)
	textSQL := fmt.Sprintf("%s.attributes#>>'{%s}'", alias, path)
	operand := accessrule.PBOperand{Cardinality: accessrule.PBScalar}
	if kind == schema.TypeJSONB && len(parts) > 1 {
		operand.SQL, operand.Kind = textSQL, accessrule.PBText
		return operand, nil
	}
	if kind == schema.TypeJSONB {
		return accessrule.PBOperand{Kind: accessrule.PBText, Cardinality: accessrule.PBMany, Multi: &accessrule.PBMulti{
			From: fmt.Sprintf("jsonb_array_elements_text(coalesce(%s,'[]'::jsonb)) AS arj(value)", jsonSQL), ValueSQL: "arj.value",
		}}, nil
	}
	switch kind {
	case schema.TypeBoolean:
		operand.SQL, operand.Kind = "("+textSQL+")::boolean", accessrule.PBBool
	case schema.TypeInteger, schema.TypeBigInt, schema.TypeNumeric:
		operand.SQL, operand.Kind = "("+textSQL+")::numeric", accessrule.PBNumber
	case schema.TypeTimestampTZ, schema.TypeAutoDate:
		operand.SQL, operand.Kind = "("+textSQL+")::timestamptz", accessrule.PBTime
	case schema.TypeUUID:
		operand.SQL, operand.Kind = "("+textSQL+")::uuid", accessrule.PBUUID
	case schema.TypeGeoPoint:
		operand.SQL, operand.Kind = jsonSQL, accessrule.PBJSON
	default:
		operand.SQL, operand.Kind = textSQL, accessrule.PBText
	}
	return operand, nil
}

func scalarOperand(sql string, kind accessrule.PBKind, tail []string) (accessrule.PBOperand, error) {
	if len(tail) != 0 {
		return accessrule.PBOperand{}, errors.New("invalid scalar path")
	}
	return accessrule.PBOperand{SQL: sql, Kind: kind, Cardinality: accessrule.PBScalar}, nil
}

func authRuleField(fields []schema.FieldInput, name string) (schema.FieldInput, bool) {
	for _, field := range fields {
		if field.Name == name {
			return field, true
		}
	}
	return schema.FieldInput{}, false
}
