package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
)

func schemaPolicyChecksum(ctx context.Context, tx pgx.Tx, catalog Catalog, document SchemaDocument) (string, error) {
	type state struct {
		Name     string          `json:"name"`
		Enabled  bool            `json:"enabled"`
		Rules    json.RawMessage `json:"rules,omitempty"`
		Revision int64           `json:"revision"`
	}
	names := make([]string, 0, len(document.Collections)+len(catalog.Collections))
	for _, item := range document.Collections {
		names = append(names, item.Name)
	}
	if document.DeleteMissing {
		for name := range catalog.Collections {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	values := make([]state, 0, len(names))
	seenNames := map[string]bool{}
	for _, name := range names {
		if seenNames[name] {
			continue
		}
		seenNames[name] = true
		collection, ok := catalog.Collections[name]
		if !ok {
			continue
		}
		entry := state{Name: name}
		if err := tx.QueryRow(ctx, `select coalesce((select enabled from pgbase_meta.collection_access where collection_id=$1),false)`, collection.ID).Scan(&entry.Enabled); err != nil {
			return "", err
		}
		var rules []byte
		err := tx.QueryRow(ctx, `select rules,revision from pgbase_meta.collection_rules where collection_id=$1`, collection.ID).Scan(&rules, &entry.Revision)
		if err == nil {
			entry.Rules = rules
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		values = append(values, entry)
	}
	authState := []json.RawMessage{}
	for _, item := range document.AuthCollections {
		var raw []byte
		err := tx.QueryRow(ctx, `select jsonb_build_object(
			'name',c.name,
			'updated',c.updated_at,
			'policyUpdated',p.updated_at,
			'rules',jsonb_build_object('list',c.list_rule,'view',c.view_rule,'create',c.create_rule,'update',c.update_rule,'delete',c.delete_rule,'auth',c.auth_rule,'manage',c.manage_rule),
			'tokenPolicies',coalesce((select jsonb_agg(jsonb_build_object('purpose',t.purpose,'ttl',t.ttl_seconds,'updated',t.updated_at) order by t.purpose) from pgbase_meta.auth_token_policies t where t.auth_collection_id=c.id),'[]'::jsonb)
		) from pgbase_meta.auth_collections c join pgbase_meta.auth_collection_policies p on p.auth_collection_id=c.id where c.name=$1`, item.Name).Scan(&raw)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		if err == nil {
			authState = append(authState, raw)
		}
	}
	raw, err := json.Marshal(struct {
		Collections []state           `json:"collections"`
		Auth        []json.RawMessage `json:"auth"`
	}{values, authState})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
