package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/swayam5342/dextro/internal/provider/gdrive"
)

// syncBatch limits how many accounts are asked at once, so a server with many
// accounts doesn't open them all together.
const syncBatch = 10

// SyncResult says how a quota sync went.
type SyncResult struct {
	Checked     int
	Unreachable int
}

// Unreachable is an account the last sync couldn't reach, for the admin to follow
// up with its owner.
type Unreachable struct {
	OwnerEmail string
	Summary    string
	CheckedAt  time.Time
}

// Health is what the last sync saw across the whole server. It's made of totals,
// so it says nothing about what anyone stores.
type Health struct {
	Accounts    int64
	UsedBytes   int64
	Unreachable []Unreachable
}

// SyncQuotas asks every linked account how full it is and saves the answer. An
// account that can't be reached is a result, not a failure; an error here means
// the sync itself couldn't run.
func (s *Service) SyncQuotas(ctx context.Context) (SyncResult, error) {
	records, err := s.store.ListAllConnectionsForQuotaSync(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	var result SyncResult
	for batch := range slices.Chunk(records, syncBatch) {
		for _, checked := range s.probeAll(ctx, batch) {
			// A shutdown cuts these calls off, which says nothing about the accounts. It
			// cancels ctx, so this write fails too and nothing false is saved.
			reached := checked.err == nil
			if err := s.store.RecordConnectionCheck(ctx, checked.record.ID, reached, checked.quota.Used); err != nil {
				return result, err
			}
			result.Checked++
			if !reached {
				result.Unreachable++
			}
		}
	}
	return result, nil
}

// Health returns what the last sync saw. Only the admin's status page calls it.
func (s *Service) Health(ctx context.Context) (Health, error) {
	health, err := s.store.ConnectionTotals(ctx)
	if err != nil {
		return Health{}, err
	}
	rows, err := s.store.ListUnreachableConnections(ctx)
	if err != nil {
		return Health{}, err
	}
	health.Unreachable = make([]Unreachable, 0, len(rows))
	for _, row := range rows {
		var settings gdrive.Settings
		if err := json.Unmarshal(row.Settings, &settings); err != nil {
			return Health{}, fmt.Errorf("decode settings: %w", err)
		}
		health.Unreachable = append(health.Unreachable, Unreachable{
			OwnerEmail: row.OwnerEmail,
			Summary:    settings.Email,
			CheckedAt:  row.CheckedAt,
		})
	}
	return health, nil
}
