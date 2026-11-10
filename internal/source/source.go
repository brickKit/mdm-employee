// Package source answers the record read of the watermark stream mdm.employee (be-protocol P9.30-P9.33): its three
// kinds of record, employees, appointments and account links, by ID and in ID order, each as its current full-state
// event carries it (no restricted personal data). Nothing is ever deleted here: an unknown ID is purged.
package source

import (
	"context"
	"slices"
	"strings"

	core "github.com/brickKit/mdm-employee/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/jackc/pgx/v5"
)

// kind is how one kind of record is read.
type kind struct {
	table, cols string
	scan        func(pgx.Row) (string, int64, any, error)
}

var kinds = []kind{
	{"employee", core.EmployeeCols, func(r pgx.Row) (string, int64, any, error) {
		v, err := core.ScanEmployee(r)
		return v.ID, v.Version, v, err
	}},
	{"appointment", core.AppointmentCols, func(r pgx.Row) (string, int64, any, error) {
		v, err := core.ScanAppointment(r)
		return v.ID, v.Version, v, err
	}},
	{"account_link", core.LinkCols, func(r pgx.Row) (string, int64, any, error) {
		v, err := core.ScanLink(r)
		return v.ID, v.Version, v, err
	}},
}

func query(ctx context.Context, tx db.Tx, k kind, where string, args ...any) ([]events.Record, error) {
	rows, err := tx.Query(ctx, `SELECT `+k.cols+` FROM `+k.table+` WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []events.Record
	for rows.Next() {
		id, version, data, err := k.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, events.Record{ID: id, Version: version, Data: data})
	}
	return out, rows.Err()
}

// Read answers every ID in order: exists with its version and data, or purged (never a record: nothing is deleted).
func Read(ctx context.Context, tx db.Tx, ids []string) ([]events.ReadResult, error) {
	var valid []string
	for _, id := range ids {
		if core.UUIDForm.MatchString(id) {
			valid = append(valid, id)
		}
	}
	found := map[string]events.Record{}
	if len(valid) > 0 {
		for _, k := range kinds {
			recs, err := query(ctx, tx, k, `id = ANY($1::uuid[])`, valid)
			if err != nil {
				return nil, err
			}
			for _, r := range recs {
				found[r.ID] = r
			}
		}
	}
	out := make([]events.ReadResult, 0, len(ids))
	for _, id := range ids {
		if r, ok := found[id]; ok {
			out = append(out, events.ReadResult{ID: id, State: events.Exists, Version: r.Version, Data: r.Data})
		} else {
			out = append(out, events.ReadResult{ID: id, State: events.Purged})
		}
	}
	return out, nil
}

// Page returns up to limit records of the three kinds whose ID is greater than after, by ID ascending (the IDs are
// UUIDv7 text: their byte order is their order here).
func Page(ctx context.Context, tx db.Tx, after string, limit int) ([]events.Record, error) {
	var all []events.Record
	for _, k := range kinds {
		recs, err := query(ctx, tx, k, `id::text > $1 ORDER BY id::text LIMIT $2`, after, limit)
		if err != nil {
			return nil, err
		}
		all = append(all, recs...)
	}
	slices.SortFunc(all, func(a, b events.Record) int { return strings.Compare(a.ID, b.ID) })
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}
