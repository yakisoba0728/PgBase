package adminauth

import (
	"context"
	"net/netip"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) Administrator(ctx context.Context, id uuid.UUID) (Administrator, error) {
	if err := s.ready(); err != nil {
		return Administrator{}, err
	}
	return NewStore(s.pool).AdministratorByID(ctx, id)
}

func (s *Service) UpdateAdministratorEmail(ctx context.Context, id uuid.UUID, email string) (Administrator, error) {
	if err := s.ready(); err != nil {
		return Administrator{}, err
	}
	return NewStore(s.pool).UpdateAdministratorEmail(ctx, id, email, s.clock.Now())
}

func (s *Service) AdministratorIPPrefixes(ctx context.Context, id uuid.UUID) ([]string, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	return NewStore(s.pool).IPPrefixes(ctx, id)
}

func (s *Service) ReplaceAdministratorIPPrefixes(ctx context.Context, id uuid.UUID, values []string) ([]string, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if len(values) > 64 {
		return nil, ErrValidation
	}
	prefixes := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil || prefix.Bits() == 0 || prefix != prefix.Masked() {
			return nil, ErrValidation
		}
		value := prefix.String()
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		prefixes = append(prefixes, value)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		a, _ := netip.ParsePrefix(prefixes[i])
		b, _ := netip.ParsePrefix(prefixes[j])
		if a.Addr().BitLen() != b.Addr().BitLen() {
			return a.Addr().BitLen() < b.Addr().BitLen()
		}
		if a.Bits() != b.Bits() {
			return a.Bits() < b.Bits()
		}
		return a.Addr().Less(b.Addr())
	})
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error { return NewStore(tx).ReplaceIPPrefixes(ctx, id, prefixes) })
	if err != nil {
		return nil, err
	}
	return prefixes, nil
}
