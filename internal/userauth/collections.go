package userauth

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/database"
)

var DefaultCollectionID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
var authCollectionName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type AuthCollection struct {
	ID                  uuid.UUID
	Name                string
	RegistrationEnabled bool
	ProfileSchema       map[string]string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type CreateAuthCollectionInput struct {
	ID                  uuid.UUID
	Name                string
	RegistrationEnabled bool
	ProfileSchema       map[string]string
}
type UpdateAuthCollectionInput struct {
	RegistrationEnabled bool
	ProfileSchema       map[string]string
}

func (s *Service) ConfigureDefaultRegistration(enabled bool) { s.defaultRegistration = enabled }

func (s *Service) CreateAuthCollection(ctx context.Context, input CreateAuthCollectionInput) (AuthCollection, error) {
	if input.ID == uuid.Nil || !authCollectionName.MatchString(input.Name) || !validProfileSchema(input.ProfileSchema) {
		return AuthCollection{}, ErrValidation
	}
	if input.ProfileSchema == nil {
		input.ProfileSchema = map[string]string{}
	}
	schema, _ := json.Marshal(input.ProfileSchema)
	var value AuthCollection
	var raw []byte
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('pgbase-auth-collection-limit'))`); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `select count(*) from pgbase_meta.auth_collections`).Scan(&count); err != nil {
			return err
		}
		if count >= 100 {
			return ErrConflict
		}
		return tx.QueryRow(ctx, `insert into pgbase_meta.auth_collections(id,name,registration_enabled,profile_schema,created_at,updated_at) values($1,$2,$3,$4,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) returning id,name,registration_enabled,profile_schema,created_at,updated_at`, input.ID, input.Name, input.RegistrationEnabled, schema).Scan(&value.ID, &value.Name, &value.RegistrationEnabled, &raw, &value.CreatedAt, &value.UpdatedAt)
	})
	if database.Classify(err) == database.KindConflict {
		return AuthCollection{}, ErrConflict
	}
	if err != nil {
		return AuthCollection{}, err
	}
	_ = json.Unmarshal(raw, &value.ProfileSchema)
	return value, nil
}

func (s *Service) UpdateAuthCollection(ctx context.Context, id uuid.UUID, input UpdateAuthCollectionInput) (AuthCollection, error) {
	if id == uuid.Nil || !validProfileSchema(input.ProfileSchema) {
		return AuthCollection{}, ErrValidation
	}
	if input.ProfileSchema == nil {
		input.ProfileSchema = map[string]string{}
	}
	schema, _ := json.Marshal(input.ProfileSchema)
	var value AuthCollection
	var raw []byte
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `select id from pgbase_meta.auth_collections where id=$1 for update`, id).Scan(&value.ID); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `select p.attributes from pgbase_meta.user_auth_profiles p join pgbase_meta.users u on u.id=p.user_id where u.auth_collection_id=$1 for update of p`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var profileRaw []byte
			var profile map[string]any
			if rows.Scan(&profileRaw) != nil || json.Unmarshal(profileRaw, &profile) != nil || !validateProfile(profile, input.ProfileSchema) {
				return ErrConflict
			}
		}
		if rows.Err() != nil {
			return rows.Err()
		}
		return tx.QueryRow(ctx, `update pgbase_meta.auth_collections set registration_enabled=$2,profile_schema=$3,updated_at=CURRENT_TIMESTAMP where id=$1 returning id,name,registration_enabled,profile_schema,created_at,updated_at`, id, input.RegistrationEnabled, schema).Scan(&value.ID, &value.Name, &value.RegistrationEnabled, &raw, &value.CreatedAt, &value.UpdatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthCollection{}, ErrNotFound
	}
	if err != nil {
		return AuthCollection{}, err
	}
	_ = json.Unmarshal(raw, &value.ProfileSchema)
	return value, nil
}

func (s *Service) DeleteAuthCollection(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return ErrValidation
	}
	if id == DefaultCollectionID {
		return ErrConflict
	}
	tag, err := s.pool.Exec(ctx, `delete from pgbase_meta.auth_collections where id=$1`, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ErrConflict
	}
	if database.Classify(err) == database.KindConflict {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) ListAuthCollections(ctx context.Context) ([]AuthCollection, error) {
	rows, err := s.pool.Query(ctx, `select id,name,registration_enabled,profile_schema,created_at,updated_at from pgbase_meta.auth_collections order by name limit 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]AuthCollection, 0)
	for rows.Next() {
		var v AuthCollection
		var raw []byte
		if err := rows.Scan(&v.ID, &v.Name, &v.RegistrationEnabled, &raw, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &v.ProfileSchema); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

func (s *Service) GetAuthCollection(ctx context.Context, name string) (AuthCollection, error) {
	if !authCollectionName.MatchString(name) {
		return AuthCollection{}, ErrValidation
	}
	var v AuthCollection
	var raw []byte
	err := s.pool.QueryRow(ctx, `select id,name,registration_enabled,profile_schema,created_at,updated_at from pgbase_meta.auth_collections where name=$1`, name).Scan(&v.ID, &v.Name, &v.RegistrationEnabled, &raw, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthCollection{}, ErrNotFound
	}
	if err != nil {
		return AuthCollection{}, err
	}
	if json.Unmarshal(raw, &v.ProfileSchema) != nil {
		return AuthCollection{}, errors.New("invalid stored auth collection")
	}
	return v, nil
}

func validProfileSchema(schema map[string]string) bool {
	if len(schema) > 64 {
		return false
	}
	for name, kind := range schema {
		if !authCollectionName.MatchString(name) {
			return false
		}
		switch kind {
		case "boolean", "integer", "numeric", "text", "timestamptz", "uuid", "jsonb":
		default:
			return false
		}
	}
	return true
}

func (s *Service) SetProfile(ctx context.Context, userID uuid.UUID, attributes map[string]any) (map[string]any, error) {
	if userID == uuid.Nil || len(attributes) > 64 {
		return nil, ErrValidation
	}
	raw, err := json.Marshal(attributes)
	if err != nil {
		return nil, ErrValidation
	}
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var rawSchema []byte
		if err := tx.QueryRow(ctx, `select c.profile_schema from pgbase_meta.users u join pgbase_meta.auth_collections c on c.id=u.auth_collection_id where u.id=$1 and u.active for share of c`, userID).Scan(&rawSchema); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		var schema map[string]string
		if json.Unmarshal(rawSchema, &schema) != nil || !validateProfile(attributes, schema) {
			return ErrValidation
		}
		_, err := tx.Exec(ctx, `insert into pgbase_meta.user_auth_profiles(user_id,attributes,updated_at) values($1,$2,CURRENT_TIMESTAMP) on conflict(user_id) do update set attributes=excluded.attributes,updated_at=excluded.updated_at`, userID, raw)
		return err
	})
	if err != nil {
		return nil, err
	}
	return attributes, nil
}

func (s *Service) GetProfile(ctx context.Context, userID uuid.UUID) (map[string]any, error) {
	if userID == uuid.Nil {
		return nil, ErrValidation
	}
	var raw []byte
	err := s.pool.QueryRow(ctx, `select attributes from pgbase_meta.user_auth_profiles where user_id=$1`, userID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("invalid stored auth profile")
	}
	return value, nil
}

func validateProfile(attributes map[string]any, schema map[string]string) bool {
	for name, value := range attributes {
		kind, ok := schema[name]
		if !ok {
			return false
		}
		switch kind {
		case "boolean":
			if _, ok := value.(bool); !ok {
				return false
			}
		case "integer":
			n, ok := value.(float64)
			if !ok || n != float64(int64(n)) {
				return false
			}
		case "numeric":
			if _, ok := value.(float64); !ok {
				return false
			}
		case "text":
			if v, ok := value.(string); !ok || len(v) > 4096 {
				return false
			}
		case "timestamptz":
			v, ok := value.(string)
			if !ok {
				return false
			}
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				return false
			}
		case "uuid":
			v, ok := value.(string)
			if !ok {
				return false
			}
			if _, err := uuid.Parse(v); err != nil {
				return false
			}
		case "jsonb":
			if _, err := json.Marshal(value); err != nil {
				return false
			}
		default:
			return false
		}
	}
	raw, err := json.Marshal(attributes)
	return err == nil && len(raw) <= 64<<10
}
