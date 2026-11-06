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

	"github.com/swayam5342/tributary/internal/provider"
)

// quotaTimeout is how long a slow account can hold up an upload before it's
// treated as broken for this request.
const quotaTimeout = 10 * time.Second

// Errors for when an upload has nowhere to go.
var (
	ErrNoStorage          = errors.New("no accounts linked")
	ErrStorageFull        = errors.New("no account has room")
	ErrStorageUnavailable = errors.New("no account can be reached")
	// ErrAccountDraining means someone picked an account that's being emptied.
	ErrAccountDraining = errors.New("account is being emptied")
	// ErrNoOtherAccount means a file has nowhere to move to.
	ErrNoOtherAccount = errors.New("no other account to move to")
)

// Chosen is the account an upload should go to.
type Chosen struct {
	ConnectionID uuid.UUID
	Provider     provider.Provider
}

// AccountUsage is one account in the pool. Available is false if the account
// couldn't be reached, which usually means its token has expired. Reserved is held
// by unfinished uploads and files waiting to move here.
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
// used or refused, never swapped for another. Otherwise the user's routing decides,
// and accounts that can't be reached are passed over so one expired token doesn't
// stop every upload. Space held for uploads and moves doesn't count as free.
func (s *Service) Choose(ctx context.Context, userID uuid.UUID, preferred *uuid.UUID, size int64) (Chosen, error) {
	return s.claim(ctx, userID, preferred, size, func(*Store, uuid.UUID) error { return nil })
}

// claim chooses an account and runs hold in the same transaction. Asking Google is
// slow, so it happens first, and only the choosing takes turns. Taking turns is also
// what stops uploads started together from reading the same round robin position.
func (s *Service) claim(ctx context.Context, userID uuid.UUID, preferred *uuid.UUID, size int64, hold func(tx *Store, connectionID uuid.UUID) error) (Chosen, error) {
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
		routing, err := tx.UploadRouting(ctx, userID)
		if err != nil {
			return err
		}
		if chosen, err = pick(candidates, reserved, size, routing); err != nil {
			return err
		}
		// An account the user picked isn't a turn.
		if preferred == nil && routing.Mode == RouteRoundRobin {
			if err := tx.SetLastUploadConnection(ctx, userID, chosen.ConnectionID); err != nil {
				return err
			}
		}
		return hold(tx, chosen.ConnectionID)
	})
	if err != nil {
		return Chosen{}, err
	}
	return chosen, nil
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
// uploads can't both count on the same room.
func (s *Service) Reserve(ctx context.Context, userID uuid.UUID, preferred *uuid.UUID, r Reservation) (Chosen, error) {
	return s.claim(ctx, userID, preferred, r.Size, func(tx *Store, connectionID uuid.UUID) error {
		return tx.CreateUploadSession(ctx, userID, connectionID, r)
	})
}

// TransferFile is a file about to move out of its account.
type TransferFile struct {
	ID     uuid.UUID
	Source uuid.UUID
	Size   int64
}

// TransferRequest is a set of files to move. Target picks the account; left nil, they
// spread across the others. Drain marks an account as being emptied, along with them.
type TransferRequest struct {
	Target *uuid.UUID
	Drain  *uuid.UUID
	Files  []TransferFile
}

// ReserveTransfers chooses where each file goes and holds the space, or refuses the
// lot. A half-emptied account helps nobody, so it's better to find out now than to
// fail hours in. It returns how many files were queued.
func (s *Service) ReserveTransfers(ctx context.Context, userID uuid.UUID, request TransferRequest) (int, error) {
	var candidates []probed
	if len(request.Files) > 0 {
		var err error
		if candidates, err = s.candidates(ctx, userID, request.Target); err != nil {
			return 0, err
		}
	}
	queued := 0
	err := s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.LockUploadReservations(ctx, userID); err != nil {
			return err
		}
		reserved, err := tx.ReservedBytes(ctx, userID)
		if err != nil {
			return err
		}
		counted := withReserved(candidates, reserved)
		// The largest first, because they're the hardest to place.
		files := slices.SortedStableFunc(slices.Values(request.Files), func(a, b TransferFile) int {
			return cmp.Compare(b.Size, a.Size)
		})
		for _, file := range files {
			target, err := place(counted, file)
			if err != nil {
				return err
			}
			added, err := tx.CreateFileTransfer(ctx, userID, target, file)
			if err != nil {
				return err
			}
			if added {
				queued++
			}
		}
		if request.Drain != nil {
			return tx.SetConnectionDrainingForOwner(ctx, userID, *request.Drain, true)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return queued, nil
}

// place takes the account with the most room that isn't where the file already is,
// and counts the file as being there.
func place(counted []probed, file TransferFile) (uuid.UUID, error) {
	slices.SortStableFunc(counted, moreRoomFirst)
	other := false
	for i := range counted {
		if counted[i].record.ID == file.Source {
			continue
		}
		other = true
		if hasRoom(counted[i].quota, file.Size) {
			counted[i].quota.Used += file.Size
			return counted[i].record.ID, nil
		}
	}
	if !other {
		return uuid.Nil, ErrNoOtherAccount
	}
	return uuid.Nil, ErrStorageFull
}

// StopDraining puts an account back in use.
func (s *Service) StopDraining(ctx context.Context, userID, id uuid.UUID) error {
	return s.store.SetConnectionDrainingForOwner(ctx, userID, id, false)
}

// candidates asks the accounts an upload could go to how full they are.
func (s *Service) candidates(ctx context.Context, userID uuid.UUID, preferred *uuid.UUID) ([]probed, error) {
	if preferred != nil {
		r, err := s.store.GetConnectionForOwner(ctx, userID, *preferred)
		if err != nil {
			return nil, err
		}
		if r.Draining {
			return nil, ErrAccountDraining
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
	// An account being emptied has the most room, and would fill straight back up.
	records = slices.DeleteFunc(records, func(r record) bool { return r.Draining })
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

// withReserved counts what's held on each account as already used.
func withReserved(candidates []probed, reserved map[uuid.UUID]int64) []probed {
	counted := slices.Clone(candidates)
	for i := range counted {
		counted[i].quota.Used += reserved[counted[i].record.ID]
	}
	return counted
}

// pick takes the first account with room, in the order routing tries them.
func pick(candidates []probed, reserved map[uuid.UUID]int64, size int64, routing Routing) (Chosen, error) {
	for _, account := range arrange(withReserved(candidates, reserved), routing) {
		if hasRoom(account.quota, size) {
			return Chosen{ConnectionID: account.record.ID, Provider: account.provider}, nil
		}
	}
	return Chosen{}, ErrStorageFull
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
			// Nothing new can go in an account being emptied.
			if !account.Draining {
				pool.Free += free
				pool.Unlimited = pool.Unlimited || !limited
			}
		}
		pool.Accounts = append(pool.Accounts, account)
	}
	return pool, nil
}
