package recordaccess

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type RulePolicy struct {
	Collection string           `json:"collection"`
	Rules      accessrule.Rules `json:"rules"`
	Revision   int64            `json:"revision"`
}

type RuleService struct {
	pool    *database.Pool
	schemas *schema.Service
}

func NewRuleService(pool *database.Pool, schemas *schema.Service) *RuleService {
	return &RuleService{pool, schemas}
}

func (s *RuleService) Get(ctx context.Context, name string) (RulePolicy, error) {
	var result RulePolicy
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		catalog, err := s.schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return err
		}
		collection, ok := catalog.Collections[name]
		if !ok {
			return ErrNotFound
		}
		rules, revision, err := LoadRules(ctx, tx, collection.ID)
		result = RulePolicy{name, rules, revision}
		return err
	})
	return result, err
}

func (s *RuleService) Set(ctx context.Context, name string, rules accessrule.Rules) (RulePolicy, error) {
	result := RulePolicy{Collection: name, Rules: rules}
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		catalog, err := s.schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return err
		}
		collection, ok := catalog.Collections[name]
		if !ok {
			return ErrNotFound
		}
		// Schema imports use the collection row as the common lock even before
		// either optional policy row exists. This prevents concurrent first
		// inserts from escaping FOR UPDATE on an empty collection_access/rules set.
		if _, err := tx.Exec(ctx, `SELECT id FROM pgbase_meta.collections WHERE id=$1 FOR UPDATE`, collection.ID); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `SELECT 1 FROM pgbase_meta.collection_access WHERE collection_id=$1 FOR UPDATE`, collection.ID); err != nil {
			return err
		}
		if err := ValidateRules(catalog, collection, rules); err != nil {
			return err
		}
		raw, err := json.Marshal(rules)
		if err != nil || len(raw) > 16384 {
			return accessrule.ErrInvalid
		}
		return tx.QueryRow(ctx, `INSERT INTO pgbase_meta.collection_rules(collection_id,rules) VALUES($1,$2::jsonb)
   ON CONFLICT(collection_id) DO UPDATE SET rules=excluded.rules,revision=pgbase_meta.collection_rules.revision+1,updated_at=clock_timestamp()
   RETURNING revision`, collection.ID, string(raw)).Scan(&result.Revision)
	})
	return result, err
}

// ValidateRules checks a rule policy against one transaction-pinned catalog.
// It has no database side effects, so schema migrations can validate their
// final projected catalog before publishing any policy rows.
func ValidateRules(catalog schema.Catalog, collection schema.Collection, rules accessrule.Rules) error {
	for _, action := range []string{"list", "view", "create", "update", "delete"} {
		rule, _ := rules.Action(action)
		principal := identity.Subject{Kind: identity.User, ID: uuid.New()}
		var request *accessrule.RequestContext
		if action == "create" || action == "update" {
			request = &accessrule.RequestContext{Present: map[string]bool{}, Changed: map[string]bool{}}
		}
		if _, _, err := compileRecordRule(catalog, collection, rule, principal, ruleCompileOptions{
			request: request, prospectiveOwner: action == "create", requestFallback: request != nil,
		}); err != nil {
			return err
		}
	}
	return nil
}

// LoadRules pins the current rules to the read/write transaction. Absent rules
// preserve the existing owner-only contract. Collection access remains an
// independent opt-in gate.
func LoadRules(ctx context.Context, tx pgx.Tx, collectionID uuid.UUID) (accessrule.Rules, int64, error) {
	var raw []byte
	var revision int64
	err := tx.QueryRow(ctx, `SELECT rules,revision FROM pgbase_meta.collection_rules WHERE collection_id=$1 FOR SHARE`, collectionID).Scan(&raw, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return accessrule.OwnerRules(), 0, nil
	}
	if err != nil {
		return accessrule.Rules{}, 0, err
	}
	rules, err := accessrule.Parse(raw)
	return rules, revision, err
}
