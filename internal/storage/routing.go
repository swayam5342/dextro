package storage

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/audit"
)

// RoutingMode is how an upload that doesn't name an account chooses one.
type RoutingMode string

// The ways an upload can choose an account.
const (
	// RouteMostSpace uses the account with the most free space.
	RouteMostSpace RoutingMode = "most_space"
	// RouteRoundRobin takes the accounts in turn.
	RouteRoundRobin RoutingMode = "round_robin"
	// RoutePriority uses the first account in the order that has room.
	RoutePriority RoutingMode = "priority"
)

// ErrInvalidRouting means the mode is unknown or the order repeats an account.
var ErrInvalidRouting = errors.New("invalid routing")

// Routing is how a user's uploads choose an account. Last is the account round
// robin used most recently.
type Routing struct {
	Mode  RoutingMode
	Order []uuid.UUID
	Last  *uuid.UUID
}

// Routing reports a user's routing, with every linked account in Order.
func (s *Service) Routing(ctx context.Context, userID uuid.UUID) (Routing, error) {
	routing, err := s.store.UploadRouting(ctx, userID)
	if err != nil {
		return Routing{}, err
	}
	records, err := s.store.ListConnectionsForOwner(ctx, userID)
	if err != nil {
		return Routing{}, err
	}
	inOrder(records, routing.Order, func(r record) uuid.UUID { return r.ID })
	routing.Order = make([]uuid.UUID, 0, len(records))
	for _, r := range records {
		routing.Order = append(routing.Order, r.ID)
	}
	return routing, nil
}

// SetRouting saves a user's routing. Accounts left out of order come after the rest.
func (s *Service) SetRouting(ctx context.Context, userID uuid.UUID, mode RoutingMode, order []uuid.UUID, ip string) (Routing, error) {
	if !slices.Contains([]RoutingMode{RouteMostSpace, RouteRoundRobin, RoutePriority}, mode) {
		return Routing{}, ErrInvalidRouting
	}
	seen := make(map[uuid.UUID]bool, len(order))
	for _, id := range order {
		if seen[id] {
			return Routing{}, ErrInvalidRouting
		}
		seen[id] = true
	}
	err := s.store.InTx(ctx, func(tx *Store) error {
		records, err := tx.ListConnectionsForOwner(ctx, userID)
		if err != nil {
			return err
		}
		for _, id := range order {
			if !slices.ContainsFunc(records, func(r record) bool { return r.ID == id }) {
				return ErrConnectionNotFound
			}
		}
		if err := tx.SetUploadRouting(ctx, userID, mode, order); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{
			ActorID:  userID,
			Action:   audit.ActionRoutingChanged,
			IP:       ip,
			Metadata: map[string]any{"mode": string(mode)},
		})
	})
	if err != nil {
		return Routing{}, err
	}
	return s.Routing(ctx, userID)
}

// inOrder sorts items by where they appear in order, in place. Anything missing from
// it goes last and keeps the order it had.
func inOrder[T any](items []T, order []uuid.UUID, id func(T) uuid.UUID) {
	rank := func(item T) int {
		if i := slices.Index(order, id(item)); i >= 0 {
			return i
		}
		return len(order)
	}
	slices.SortStableFunc(items, func(a, b T) int { return cmp.Compare(rank(a), rank(b)) })
}

// arrange puts accounts in the order an upload should try them. If the account round
// robin used last can't be used now, the turn starts again from the top. Round robin
// takes busy accounts last, so a slow one can't collect every upload. The other modes
// promise where files end up, so being busy doesn't change them.
func arrange(accounts []probed, routing Routing, busy []uuid.UUID) []probed {
	if routing.Mode == RouteMostSpace {
		slices.SortStableFunc(accounts, moreRoomFirst)
		return accounts
	}
	inOrder(accounts, routing.Order, func(p probed) uuid.UUID { return p.record.ID })
	if routing.Mode != RouteRoundRobin {
		return accounts
	}
	if routing.Last != nil {
		last := slices.IndexFunc(accounts, func(p probed) bool { return p.record.ID == *routing.Last })
		accounts = slices.Concat(accounts[last+1:], accounts[:last+1])
	}
	isBusy := func(p probed) bool { return slices.Contains(busy, p.record.ID) }
	slices.SortStableFunc(accounts, func(a, b probed) int {
		switch {
		case isBusy(a) == isBusy(b):
			return 0
		case isBusy(a):
			return 1
		}
		return -1
	})
	return accounts
}
