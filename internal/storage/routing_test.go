package storage

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/audit"
)

func (f *fixture) route(t *testing.T, userID uuid.UUID, mode RoutingMode, order ...uuid.UUID) {
	t.Helper()
	if _, err := f.service.SetRouting(f.ctx, userID, mode, order, ""); err != nil {
		t.Fatalf("SetRouting: %v", err)
	}
}

// chooseMany records where a run of small uploads goes.
func (f *fixture) chooseMany(t *testing.T, userID uuid.UUID, times int) []uuid.UUID {
	t.Helper()
	got := make([]uuid.UUID, 0, times)
	for range times {
		got = append(got, f.choose(t, userID, nil, 1024))
	}
	return got
}

func wantAccounts(t *testing.T, got []uuid.UUID, want ...Connection) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d choices, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i].ID {
			t.Errorf("upload %d went to %s, want %s", i, got[i], want[i].Name)
		}
	}
}

func testRoundRobinTakesTurns(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	b := f.linked(t, userID, "b@example.org", 15*gib, 14*gib)
	c := f.linked(t, userID, "c@example.org", 15*gib, 7*gib)
	f.route(t, userID, RouteRoundRobin)
	wantAccounts(t, f.chooseMany(t, userID, 4), a, b, c, a)
}

func testRoundRobinFollowsOrder(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	b := f.linked(t, userID, "b@example.org", 15*gib, 0)
	f.route(t, userID, RouteRoundRobin, b.ID, a.ID)
	wantAccounts(t, f.chooseMany(t, userID, 3), b, a, b)
}

func testRoundRobinSkipsFull(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	f.linked(t, userID, "full@example.org", 15*gib, 15*gib)
	c := f.linked(t, userID, "c@example.org", 15*gib, 0)
	f.route(t, userID, RouteRoundRobin)
	wantAccounts(t, f.chooseMany(t, userID, 3), a, c, a)
}

func testRoundRobinSkipsBroken(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	f.linked(t, userID, "broken@example.org", 15*gib, 0)
	c := f.linked(t, userID, "c@example.org", 15*gib, 0)
	f.account("broken@example.org").QuotaErr = errors.New("token expired")
	f.route(t, userID, RouteRoundRobin)
	wantAccounts(t, f.chooseMany(t, userID, 3), a, c, a)
}

func testRoundRobinSkipsDraining(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	b := f.linked(t, userID, "b@example.org", 15*gib, 0)
	c := f.linked(t, userID, "c@example.org", 15*gib, 0)
	if err := f.service.store.SetConnectionDrainingForOwner(f.ctx, userID, b.ID, true); err != nil {
		t.Fatalf("SetConnectionDrainingForOwner: %v", err)
	}
	f.route(t, userID, RouteRoundRobin)
	wantAccounts(t, f.chooseMany(t, userID, 3), a, c, a)
}

// A picked account is the user's own decision, so it doesn't use up a turn.
func testChosenAccountKeepsTheTurn(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	b := f.linked(t, userID, "b@example.org", 15*gib, 0)
	c := f.linked(t, userID, "c@example.org", 15*gib, 0)
	f.route(t, userID, RouteRoundRobin)
	wantAccounts(t, f.chooseMany(t, userID, 1), a)
	if got := f.choose(t, userID, &c.ID, 1024); got != c.ID {
		t.Errorf("chose %s, want the picked account", got)
	}
	wantAccounts(t, f.chooseMany(t, userID, 1), b)
}

func testRoundRobinSurvivesRemoval(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	b := f.linked(t, userID, "b@example.org", 15*gib, 0)
	f.route(t, userID, RouteRoundRobin)
	wantAccounts(t, f.chooseMany(t, userID, 2), a, b)
	if err := f.service.Delete(f.ctx, userID, b.ID, ""); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantAccounts(t, f.chooseMany(t, userID, 2), a, a)
}

func reservation(size int64) Reservation {
	return Reservation{ID: uuid.New(), Name: "file.bin", ContentType: "application/octet-stream", Size: size, LastModified: 1}
}

// Uploads started together must not all land on the same turn.
func testRoundRobinSharesParallelUploads(t *testing.T, f *fixture) {
	userID := f.user(t)
	accounts := []Connection{
		f.linked(t, userID, "a@example.org", 15*gib, 0),
		f.linked(t, userID, "b@example.org", 15*gib, 0),
		f.linked(t, userID, "c@example.org", 15*gib, 0),
	}
	f.route(t, userID, RouteRoundRobin)

	var mu sync.Mutex
	counts := map[uuid.UUID]int{}
	var wg sync.WaitGroup
	for range 9 {
		wg.Go(func() {
			chosen, err := f.service.Reserve(f.ctx, userID, nil, reservation(1024))
			if err != nil {
				t.Errorf("Reserve: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			counts[chosen.ConnectionID]++
		})
	}
	wg.Wait()
	for _, account := range accounts {
		if counts[account.ID] != 3 {
			t.Errorf("%s got %d uploads, want 3", account.Name, counts[account.ID])
		}
	}
}

// reserveAvoiding records where an upload goes when some accounts are busy.
func (f *fixture) reserveAvoiding(t *testing.T, userID uuid.UUID, busy ...Connection) uuid.UUID {
	t.Helper()
	r := reservation(1024)
	for _, account := range busy {
		r.Busy = append(r.Busy, account.ID)
	}
	chosen, err := f.service.Reserve(f.ctx, userID, nil, r)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	return chosen.ConnectionID
}

// A slow account fills up with uploads, and must not take the rest of them too.
func testRoundRobinPassesOverBusy(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	b := f.linked(t, userID, "b@example.org", 15*gib, 0)
	c := f.linked(t, userID, "c@example.org", 15*gib, 0)
	f.route(t, userID, RouteRoundRobin)
	got := []uuid.UUID{
		f.reserveAvoiding(t, userID, a),
		f.reserveAvoiding(t, userID, a),
		f.reserveAvoiding(t, userID, a),
	}
	wantAccounts(t, got, b, c, b)
}

// Busy only means later. The upload still has to go somewhere.
func testRoundRobinUsesBusyWhenNothingElseCan(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	b := f.linked(t, userID, "b@example.org", 15*gib, 0)
	full := f.linked(t, userID, "full@example.org", 15*gib, 15*gib)
	f.route(t, userID, RouteRoundRobin)
	if got := f.reserveAvoiding(t, userID, a, b); got != a.ID {
		t.Errorf("with the free account full, went to %s, want the next turn", got)
	}
	if got := f.reserveAvoiding(t, userID, a, b, full); got != b.ID {
		t.Errorf("with every account busy, went to %s, want the next turn", got)
	}
}

// The other modes are promises about where files end up, so being busy can't change them.
func testOnlyRoundRobinPassesOverBusy(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	f.linked(t, userID, "b@example.org", 15*gib, 7*gib)
	for _, mode := range []RoutingMode{RouteMostSpace, RoutePriority} {
		f.route(t, userID, mode)
		if got := f.reserveAvoiding(t, userID, a); got != a.ID {
			t.Errorf("%s: went to %s, want a", mode, got)
		}
	}
}

func testPriorityUsesTheOrder(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	b := f.linked(t, userID, "b@example.org", 15*gib, 14*gib)
	f.route(t, userID, RoutePriority, b.ID, a.ID)
	wantAccounts(t, f.chooseMany(t, userID, 3), b, b, b)
}

func testPriorityOverflows(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	b := f.linked(t, userID, "b@example.org", 15*gib, 14*gib)
	f.route(t, userID, RoutePriority, b.ID, a.ID)
	if got := f.choose(t, userID, nil, 2*gib); got != a.ID {
		t.Errorf("chose %s, want the next account in the order", got)
	}
}

// Space held by an unfinished upload isn't free, whichever way an upload is routed.
func testPriorityCountsReservations(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 14*gib)
	b := f.linked(t, userID, "b@example.org", 15*gib, 0)
	f.route(t, userID, RoutePriority, a.ID, b.ID)
	first, err := f.service.Reserve(f.ctx, userID, nil, reservation(gib-1024))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	second, err := f.service.Reserve(f.ctx, userID, nil, reservation(gib-1024))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if first.ConnectionID != a.ID || second.ConnectionID != b.ID {
		t.Errorf("went to %s then %s, want a then b", first.ConnectionID, second.ConnectionID)
	}
}

func testPriorityListsNewAccountsLast(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	b := f.linked(t, userID, "b@example.org", 15*gib, 0)
	f.route(t, userID, RoutePriority, b.ID, a.ID)
	c := f.linked(t, userID, "c@example.org", 15*gib, 0)
	routing, err := f.service.Routing(f.ctx, userID)
	if err != nil {
		t.Fatalf("Routing: %v", err)
	}
	if want := []uuid.UUID{b.ID, a.ID, c.ID}; !slices.Equal(routing.Order, want) {
		t.Errorf("order = %v, want %v", routing.Order, want)
	}
}

func testAllFullIsStillFull(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.linked(t, userID, "a@example.org", 15*gib, 15*gib)
	f.linked(t, userID, "b@example.org", 15*gib, 15*gib)
	for _, mode := range []RoutingMode{RouteMostSpace, RouteRoundRobin, RoutePriority} {
		f.route(t, userID, mode)
		if _, err := f.service.Choose(f.ctx, userID, nil, 1024); !errors.Is(err, ErrStorageFull) {
			t.Errorf("%s: err = %v, want ErrStorageFull", mode, err)
		}
	}
}

func testRoutingDefault(t *testing.T, f *fixture) {
	userID := f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	routing, err := f.service.Routing(f.ctx, userID)
	if err != nil {
		t.Fatalf("Routing: %v", err)
	}
	if routing.Mode != RouteMostSpace || !slices.Equal(routing.Order, []uuid.UUID{a.ID}) || routing.Last != nil {
		t.Errorf("routing = %+v", routing)
	}
}

func testRoutingRefusesBadInput(t *testing.T, f *fixture) {
	userID, stranger := f.user(t), f.user(t)
	a := f.linked(t, userID, "a@example.org", 15*gib, 0)
	theirs := f.linked(t, stranger, "theirs@example.org", 15*gib, 0)

	if _, err := f.service.SetRouting(f.ctx, userID, "fastest", nil, ""); !errors.Is(err, ErrInvalidRouting) {
		t.Errorf("unknown mode: err = %v", err)
	}
	if _, err := f.service.SetRouting(f.ctx, userID, RoutePriority, []uuid.UUID{a.ID, a.ID}, ""); !errors.Is(err, ErrInvalidRouting) {
		t.Errorf("repeated account: err = %v", err)
	}
	if _, err := f.service.SetRouting(f.ctx, userID, RoutePriority, []uuid.UUID{theirs.ID}, ""); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("someone else's account: err = %v", err)
	}
}

func testRoutingIsPerUser(t *testing.T, f *fixture) {
	userID, stranger := f.user(t), f.user(t)
	f.linked(t, userID, "a@example.org", 15*gib, 0)
	f.route(t, userID, RouteRoundRobin)
	routing, err := f.service.Routing(f.ctx, stranger)
	if err != nil {
		t.Fatalf("Routing: %v", err)
	}
	if routing.Mode != RouteMostSpace || len(routing.Order) != 0 {
		t.Errorf("a stranger sees %+v", routing)
	}
}

func testRoutingIsAudited(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.route(t, userID, RouteRoundRobin)
	var count int
	err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE action = $1 AND actor_id = $2`,
		audit.ActionRoutingChanged, userID).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("audit events = %d, want 1", count)
	}
}
