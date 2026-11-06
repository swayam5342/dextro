package storage

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/provider"
)

// quotaTimeout is how long a slow account can hold up an upload before it's
// treated as broken for this request.
const quotaTimeout = 10 * time.Second

// Errors for when an upload has nowhere to go.
var (
	ErrNoStorage          = errors.New("no accounts linked")
	ErrStorageFull        = errors.New("no account has room")
	ErrStorageUnavailable = errors.New("no account can be reached")
)

// Chosen is the account an upload should go to.
type Chosen struct {
	ConnectionID uuid.UUID
	Provider     provider.Provider
}

// AccountUsage is one account in the pool. Available is false if the account
// couldn't be reached, which usually means its token has expired. Reserved is held
// by uploads that haven't finished.
type AccountUsage struct {
	Connection
	Available bool
	Limit     int64
	Used      int64
	Reserved  int64
}

// Pool adds up a user's working accounts. Limit and Free mean nothing if
// Unlimited is true, because one of the accounts reports no limit.
type Pool struct {
	Accounts  []AccountUsage
	Used      int64
	Limit     int64
	Free      int64
	Unlimited bool
}

// probed is the result of asking one account how full it is.
type probed struct {
	record   record
	provider provider.Provider
	quota    provider.Quota
	err      error
}

func (s *Service) probe(ctx context.Context, r record) probed {
	result := probed{record: r}
	result.provider, result.err = s.open(ctx, r)
	if result.err != nil {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, quotaTimeout)
	defer cancel()
	result.quota, result.err = result.provider.Quota(ctx)
	return result
}

// probeAll asks every account at once, so that a slow one costs one timeout
// rather than one timeout each.
func (s *Service) probeAll(ctx context.Context, records []record) []probed {
	results := make([]probed, len(records))
	var wg sync.WaitGroup
	for i, r := range records {
		wg.Go(func() { results[i] = s.probe(ctx, r) })
	}
	wg.Wait()
	return results
}

// hasRoom reports whether a file fits. A negative size means the length isn't
// known, which needs at least some room.
func hasRoom(quota provider.Quota, size int64) bool {
	free, limited := quota.Free()
	if !limited {
		return true
	}
	if size < 0 {
		return free > 0
	}
	return free >= size
}

// moreRoomFirst orders accounts with no limit first, then by free space.
func moreRoomFirst(a, b probed) int {
	aFree, aLimited := a.quota.Free()
	bFree, bLimited := b.quota.Free()
	if aLimited != bLimited {
		if bLimited {
			return -1
		}
		return 1
	}
	return cmp.Compare(bFree, aFree)
}

// Choose picks the account an upload should go to. An account the user picked is
// used or refused, never swapped for another. Otherwise the account with the most
// free space wins, and accounts that can't be reached are passed over so one
// expired token doesn't stop every upload. Space held by unfinished uploads
// doesn't count as free.
func (s *Service) Choose(ctx context.Context, userID uuid.UUID, preferred *uuid.UUID, size int64) (Chosen, error) {
	candidates, err := s.candidates(ctx, userID, preferred)
	if err != nil {
		return Chosen{}, err
	}
	reserved, err := s.store.ReservedBytes(ctx, userID)
	if err != nil {
		return Chosen{}, err
	}
	return pick(candidates, reserved, size)
}

// Reservation is an upload that needs space held for it.
type Reservation struct {
	ID           uuid.UUID
	FolderID     *uuid.UUID
	Name         string
	ContentType  string
	Size         int64
	LastModified int64
}

// Reserve chooses an account as Choose does and holds the space on it, so that two
// uploads can't both count on the same room. Asking Google is slow, so it happens
// first, and only the choosing takes turns.
func (s *Service) Reserve(ctx context.Context, userID uuid.UUID, preferred *uuid.UUID, r Reservation) (Chosen, error) {
	candidates, err := s.candidates(ctx, userID, preferred)
	if err != nil {
		return Chosen{}, err
	}
	var chosen Chosen
	err = s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.LockUploadReservations(ctx, userID); err != nil {
			return err
		}
		reserved, err := tx.ReservedBytes(ctx, userID)
		if err != nil {
			return err
		}
		if chosen, err = pick(candidates, reserved, r.Size); err != nil {
			return err
		}
		return tx.CreateUploadSession(ctx, userID, chosen.ConnectionID, r)
	})
	if err != nil {
		return Chosen{}, err
	}
	return chosen, nil
}

// candidates asks the accounts an upload could go to how full they are.
func (s *Service) candidates(ctx context.Context, userID uuid.UUID, preferred *uuid.UUID) ([]probed, error) {
	if preferred != nil {
		r, err := s.store.GetConnectionForOwner(ctx, userID, *preferred)
		if err != nil {
			return nil, err
		}
		result := s.probe(ctx, r)
		if result.err != nil {
			return nil, fmt.Errorf("%w: %w", ErrStorageUnavailable, result.err)
		}
		return []probed{result}, nil
	}
	records, err := s.store.ListConnectionsForOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, ErrNoStorage
	}
	results := s.probeAll(ctx, records)
	var failures []error
	working := make([]probed, 0, len(results))
	for _, result := range results {
		if result.err != nil {
			failures = append(failures, result.err)
			continue
		}
		working = append(working, result)
	}
	if len(working) == 0 {
		return nil, fmt.Errorf("%w: %w", ErrStorageUnavailable, errors.Join(failures...))
	}
	return working, nil
}

// pick takes the account with the most room once reservations are counted.
func pick(candidates []probed, reserved map[uuid.UUID]int64, size int64) (Chosen, error) {
	counted := slices.Clone(candidates)
	for i := range counted {
		counted[i].quota.Used += reserved[counted[i].record.ID]
	}
	slices.SortStableFunc(counted, moreRoomFirst)
	best := counted[0]
	if !hasRoom(best.quota, size) {
		return Chosen{}, ErrStorageFull
	}
	return Chosen{ConnectionID: best.record.ID, Provider: best.provider}, nil
}

// Pool reports how full each of a user's accounts is, and the total. Accounts that
// can't be reached are flagged and left out of the total.
func (s *Service) Pool(ctx context.Context, userID uuid.UUID) (Pool, error) {
	records, err := s.store.ListConnectionsForOwner(ctx, userID)
	if err != nil {
		return Pool{}, err
	}
	reserved, err := s.store.ReservedBytes(ctx, userID)
	if err != nil {
		return Pool{}, err
	}
	pool := Pool{Accounts: make([]AccountUsage, 0, len(records))}
	for _, result := range s.probeAll(ctx, records) {
		connection, err := withSummary(result.record)
		if err != nil {
			return Pool{}, err
		}
		account := AccountUsage{Connection: connection, Available: result.err == nil}
		if account.Available {
			account.Limit, account.Used = result.quota.Limit, result.quota.Used
			account.Reserved = reserved[result.record.ID]
			free, limited := result.quota.Free()
			free = max(free-account.Reserved, 0)
			pool.Used += account.Used
			pool.Limit += account.Limit
			pool.Free += free
			pool.Unlimited = pool.Unlimited || !limited
		}
		pool.Accounts = append(pool.Accounts, account)
	}
	return pool, nil
}
