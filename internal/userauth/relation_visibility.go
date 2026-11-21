package userauth

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func authRecordPrincipal(id uuid.UUID) identity.Subject {
	if id == uuid.Nil {
		return identity.Subject{Kind: identity.Anonymous}
	}
	return identity.Subject{Kind: identity.User, ID: id}
}

// Keep authorization on the complete internal record, then filter relation
// values before converting them to public IDs. All documents share one target
// lookup per collection and the caller's transaction snapshot.
func (s *Service) projectAuthorizedAuthRelationDocuments(ctx context.Context, tx pgx.Tx, documents []map[string]any, fields []schema.FieldInput, principal identity.Subject) error {
	if !principal.Valid() {
		return ErrInvalidCredentials
	}
	if principal.Kind == identity.Administrator {
		return s.projectAuthRelationDocuments(ctx, tx, documents, fields)
	}
	targetIDs := make(map[string]map[uuid.UUID]bool)
	collect := func(name string, id uuid.UUID) (string, error) {
		if targetIDs[name] == nil {
			targetIDs[name] = make(map[uuid.UUID]bool)
		}
		targetIDs[name][id] = true
		return id.String(), nil
	}
	for _, document := range documents {
		if err := rewriteAuthRelationIdentities(document, fields, collect); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(targetIDs))
	for name := range targetIDs {
		names = append(names, name)
	}
	sort.Strings(names)
	var actorCollectionID uuid.UUID
	if len(names) != 0 && principal.Kind == identity.User {
		if err := tx.QueryRow(ctx, `select auth_collection_id from pgbase_meta.users where id=$1 and active`, principal.ID).Scan(&actorCollectionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// An authorized self-deactivation can commit before its response
				// is projected. Return that result without relation access for
				// the now-inactive actor, rather than reporting a failed write.
				return s.projectAuthRelationsWithVisibility(ctx, tx, documents, fields, nil)
			}
			return err
		}
	}
	visible := make(map[string]map[uuid.UUID]bool)
	targets := make(map[string]relationIdentityTarget, len(names))
	var catalog *schema.Catalog
	for _, name := range names {
		target, err := loadRelationIdentityTarget(ctx, tx, name)
		if err != nil {
			return err
		}
		targets[name] = target
		if target.kind != "auth" && catalog == nil {
			// Pin the schema before locking any target auth policy, matching
			// schema transitions that also update auth collection metadata.
			store := schema.NewStore(tx)
			version, err := store.PinnedSchemaVersion(ctx)
			if err != nil {
				return err
			}
			loaded, err := store.ReadCatalog(ctx, version)
			if err != nil {
				return err
			}
			catalog = &loaded
		}
	}
	for _, name := range names {
		target := targets[name]
		var err error
		ids := make([]uuid.UUID, 0, len(targetIDs[name]))
		for id := range targetIDs[name] {
			ids = append(ids, id)
		}
		if target.kind == "auth" {
			visible[name], err = visibleAuthRelationIDs(ctx, tx, target.id, actorCollectionID, principal, ids)
		} else {
			collection, ok := catalog.Collections[name]
			if !ok || collection.ID != target.id {
				return schema.ErrConflict
			}
			visible[name], err = query.VisibleRelationIDs(ctx, tx, *catalog, collection, principal, ids, accessrule.RequestContextFromContext(ctx))
			if errors.Is(err, recordaccess.ErrDenied) {
				err = nil
			}
		}
		if err != nil {
			return err
		}
	}
	return s.projectAuthRelationsWithVisibility(ctx, tx, documents, fields, visible)
}

func (s *Service) projectAuthRelationsWithVisibility(ctx context.Context, tx pgx.Tx, documents []map[string]any, fields []schema.FieldInput, visible map[string]map[uuid.UUID]bool) error {
	for _, document := range documents {
		for _, field := range fields {
			if field.Type != schema.TypeRelation || field.Relation == nil || document[field.Name] == nil {
				continue
			}
			set := visible[field.Relation.TargetCollection]
			if field.Relation.Cardinality == schema.RelationSingle {
				id, _ := uuid.Parse(document[field.Name].(string))
				if !set[id] {
					document[field.Name] = nil
				}
				continue
			}
			values := make([]any, 0)
			for _, raw := range document[field.Name].([]any) {
				id, _ := uuid.Parse(raw.(string))
				if set[id] {
					values = append(values, raw)
				}
			}
			document[field.Name] = values
		}
	}
	return s.projectAuthRelationDocuments(ctx, tx, documents, fields)
}

func visibleAuthRelationIDs(ctx context.Context, tx pgx.Tx, collectionID, actorCollectionID uuid.UUID, principal identity.Subject, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	visible := make(map[uuid.UUID]bool)
	var rule *string
	if err := tx.QueryRow(ctx, `select view_rule from pgbase_meta.auth_collections where id=$1 for share`, collectionID).Scan(&rule); err != nil {
		return nil, err
	}
	if rule == nil || len(ids) == 0 {
		return visible, nil
	}
	predicate, args, err := compileAuthRecordRuleForActorInTx(ctx, tx, collectionID, actorCollectionID, *rule, AuthRuleContext{Principal: principal}, 2)
	if err != nil {
		return nil, err
	}
	args = append([]any{collectionID, ids}, args...)
	rows, err := tx.Query(ctx, `select u.id from pgbase_meta.users u left join pgbase_meta.user_auth_profiles p on p.user_id=u.id where u.auth_collection_id=$1 and u.id=any($2::uuid[]) and (`+predicate+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		visible[id] = true
	}
	return visible, rows.Err()
}
