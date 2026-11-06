package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/db"
	"github.com/swayam5342/tributary/internal/db/dbtest"
)

const gib = 1 << 30

// linked connects an account that has this much of its limit in use.
func (f *fixture) linked(t *testing.T, userID uuid.UUID, email string, limit, used int64) Connection {
	t.Helper()
	account := f.account(email)
	account.Limit, account.ExtraUsed = limit, used
	return f.connect(t, userID, email)
}

// TestPool shares one Postgres between its subtests, emptying it each time.
func TestPool(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	tests := []struct {
		name string
		run  func(*testing.T, *fixture)
	}{
		{"uploads go to the account with the most free space", testChoosesMostFree},
		{"an account with no limit counts as the most free", testUnlimitedWins},
		{"a full account is passed over", testSkipsFullAccount},
		{"a broken account is passed over", testSkipsBrokenAccount},
		{"an unknown size still needs some room", testUnknownSize},
		{"an empty file fits anywhere", testEmptyFileFits},
		{"a file bigger than every account is refused", testTooBigForAnyAccount},
		{"no accounts at all is its own error", testNoAccounts},
		{"every account broken is its own error", testAllBroken},
		{"a chosen account is used even with less space", testChosenAccount},
		{"a chosen account must have room", testChosenAccountFull},
		{"a chosen account must be working", testChosenAccountBroken},
		{"someone else's account can't be chosen", testChosenAccountIsScoped},
		{"the pool only uses the user's own accounts", testPoolIsScoped},
		{"the pool view adds accounts up", testPoolUsage},
		{"the pool view flags broken accounts", testPoolFlagsBroken},
		{"the pool view has no total with an unlimited account", testPoolUnlimited},
		{"round robin takes the accounts in turn", testRoundRobinTakesTurns},
		{"round robin follows the order", testRoundRobinFollowsOrder},
		{"round robin skips a full account", testRoundRobinSkipsFull},
		{"round robin skips a broken account", testRoundRobinSkipsBroken},
		{"round robin skips an account being emptied", testRoundRobinSkipsDraining},
		{"a chosen account does not use up a turn", testChosenAccountKeepsTheTurn},
		{"round robin carries on after an account is removed", testRoundRobinSurvivesRemoval},
		{"round robin shares out uploads started together", testRoundRobinSharesParallelUploads},
		{"priority uses the first account in the order", testPriorityUsesTheOrder},
		{"priority moves on when a file does not fit", testPriorityOverflows},
		{"priority counts space held by unfinished uploads", testPriorityCountsReservations},
		{"accounts linked later go to the end of the order", testPriorityListsNewAccountsLast},
		{"every mode reports a full pool", testAllFullIsStillFull},
		{"routing starts as most free space", testRoutingDefault},
		{"routing refuses bad input", testRoutingRefusesBadInput},
		{"routing belongs to one user", testRoutingIsPerUser},
		{"changing routing is audited", testRoutingIsAudited},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `TRUNCATE users, audit_events CASCADE`); err != nil {
				t.Fatalf("reset database: %v", err)
			}
			tt.run(t, newFixture(t, pool))
		})
	}
}

func (f *fixture) choose(t *testing.T, userID uuid.UUID, preferred *uuid.UUID, size int64) uuid.UUID {
	t.Helper()
	chosen, err := f.service.Choose(f.ctx, userID, preferred, size)
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	if chosen.Provider == nil {
		t.Fatal("Choose returned no provider")
	}
	return chosen.ConnectionID
}

func testChoosesMostFree(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.linked(t, userID, "nearly-full@example.org", 15*gib, 14*gib)
	roomy := f.linked(t, userID, "roomy@example.org", 15*gib, 1*gib)
	f.linked(t, userID, "half@example.org", 15*gib, 7*gib)
	if got := f.choose(t, userID, nil, 1024); got != roomy.ID {
		t.Errorf("chose %s, want the roomiest account", got)
	}
}

func testUnlimitedWins(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.linked(t, userID, "big@example.org", 2000*gib, 0)
	unlimited := f.linked(t, userID, "unlimited@example.org", 0, 900*gib)
	if got := f.choose(t, userID, nil, 1024); got != unlimited.ID {
		t.Errorf("chose %s, want the account with no limit", got)
	}
}

func testSkipsFullAccount(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.linked(t, userID, "full@example.org", 15*gib, 15*gib)
	open := f.linked(t, userID, "open@example.org", 15*gib, 14*gib)
	if got := f.choose(t, userID, nil, gib/2); got != open.ID {
		t.Errorf("chose %s, want the account with room", got)
	}
}

func testSkipsBrokenAccount(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.linked(t, userID, "broken@example.org", 15*gib, 0)
	working := f.linked(t, userID, "working@example.org", 15*gib, 14*gib)
	f.account("broken@example.org").QuotaErr = errors.New("token expired")
	if got := f.choose(t, userID, nil, 1024); got != working.ID {
		t.Errorf("chose %s, want the working account", got)
	}
}

func testUnknownSize(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.linked(t, userID, "full@example.org", 15*gib, 15*gib)
	if _, err := f.service.Choose(f.ctx, userID, nil, -1); !errors.Is(err, ErrStorageFull) {
		t.Errorf("error = %v, want ErrStorageFull", err)
	}
	roomy := f.linked(t, userID, "roomy@example.org", 15*gib, 0)
	if got := f.choose(t, userID, nil, -1); got != roomy.ID {
		t.Errorf("chose %s, want the roomiest account", got)
	}
}

func testEmptyFileFits(t *testing.T, f *fixture) {
	userID := f.user(t)
	full := f.linked(t, userID, "full@example.org", 15*gib, 15*gib)
	if got := f.choose(t, userID, nil, 0); got != full.ID {
		t.Errorf("chose %s", got)
	}
}

// Free space adds up across accounts, but a file has to fit in one of them.
func testTooBigForAnyAccount(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.linked(t, userID, "one@example.org", 15*gib, 5*gib)
	f.linked(t, userID, "two@example.org", 15*gib, 5*gib)
	if _, err := f.service.Choose(f.ctx, userID, nil, 11*gib); !errors.Is(err, ErrStorageFull) {
		t.Errorf("error = %v, want ErrStorageFull", err)
	}
}

func testNoAccounts(t *testing.T, f *fixture) {
	if _, err := f.service.Choose(f.ctx, f.user(t), nil, 1024); !errors.Is(err, ErrNoStorage) {
		t.Errorf("error = %v, want ErrNoStorage", err)
	}
}

func testAllBroken(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.linked(t, userID, "broken@example.org", 15*gib, 0)
	f.account("broken@example.org").QuotaErr = errors.New("token expired")
	if _, err := f.service.Choose(f.ctx, userID, nil, 1024); !errors.Is(err, ErrStorageUnavailable) {
		t.Errorf("error = %v, want ErrStorageUnavailable", err)
	}
}

func testChosenAccount(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.linked(t, userID, "roomy@example.org", 15*gib, 0)
	small := f.linked(t, userID, "small@example.org", 15*gib, 14*gib)
	if got := f.choose(t, userID, &small.ID, 1024); got != small.ID {
		t.Errorf("chose %s, want the account the user picked", got)
	}
}

func testChosenAccountFull(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.linked(t, userID, "roomy@example.org", 15*gib, 0)
	full := f.linked(t, userID, "full@example.org", 15*gib, 15*gib)
	// The user asked for this account, so it must not quietly go elsewhere.
	if _, err := f.service.Choose(f.ctx, userID, &full.ID, 1024); !errors.Is(err, ErrStorageFull) {
		t.Errorf("error = %v, want ErrStorageFull", err)
	}
}

func testChosenAccountBroken(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.linked(t, userID, "roomy@example.org", 15*gib, 0)
	broken := f.linked(t, userID, "broken@example.org", 15*gib, 0)
	f.account("broken@example.org").QuotaErr = errors.New("token expired")
	if _, err := f.service.Choose(f.ctx, userID, &broken.ID, 1024); !errors.Is(err, ErrStorageUnavailable) {
		t.Errorf("error = %v, want ErrStorageUnavailable", err)
	}
}

func testChosenAccountIsScoped(t *testing.T, f *fixture) {
	mine, theirs := f.user(t), f.user(t)
	f.linked(t, mine, "mine@example.org", 15*gib, 0)
	other := f.linked(t, theirs, "theirs@example.org", 15*gib, 0)
	if _, err := f.service.Choose(f.ctx, mine, &other.ID, 1024); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("error = %v, want ErrConnectionNotFound", err)
	}
}

func testPoolIsScoped(t *testing.T, f *fixture) {
	mine, theirs := f.user(t), f.user(t)
	own := f.linked(t, mine, "mine@example.org", 15*gib, 14*gib)
	f.linked(t, theirs, "theirs@example.org", 15*gib, 0)
	if got := f.choose(t, mine, nil, 1024); got != own.ID {
		t.Errorf("chose %s, want the user's own account", got)
	}
	usage, err := f.service.Pool(f.ctx, mine)
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	if len(usage.Accounts) != 1 {
		t.Errorf("accounts = %+v", usage.Accounts)
	}
}

func testPoolUsage(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.linked(t, userID, "one@example.org", 15*gib, 5*gib)
	f.linked(t, userID, "two@example.org", 15*gib, 1*gib)
	usage, err := f.service.Pool(f.ctx, userID)
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	if len(usage.Accounts) != 2 || usage.Used != 6*gib || usage.Limit != 30*gib || usage.Free != 24*gib {
		t.Errorf("usage = %+v", usage)
	}
}

func testPoolFlagsBroken(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.linked(t, userID, "working@example.org", 15*gib, 5*gib)
	f.linked(t, userID, "broken@example.org", 15*gib, 1*gib)
	f.account("broken@example.org").QuotaErr = errors.New("token expired")
	usage, err := f.service.Pool(f.ctx, userID)
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	if len(usage.Accounts) != 2 {
		t.Fatalf("accounts = %+v", usage.Accounts)
	}
	broken := 0
	for _, account := range usage.Accounts {
		if !account.Available {
			broken++
			if account.Summary != "broken@example.org" {
				t.Errorf("flagged %q", account.Summary)
			}
		}
	}
	if broken != 1 {
		t.Errorf("flagged accounts = %d, want 1", broken)
	}
	if usage.Used != 5*gib || usage.Limit != 15*gib {
		t.Errorf("a broken account was counted: %+v", usage)
	}
}

func testPoolUnlimited(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.linked(t, userID, "one@example.org", 15*gib, 5*gib)
	f.linked(t, userID, "unlimited@example.org", 0, 1*gib)
	usage, err := f.service.Pool(f.ctx, userID)
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	if !usage.Unlimited || usage.Used != 6*gib {
		t.Errorf("usage = %+v", usage)
	}
}
