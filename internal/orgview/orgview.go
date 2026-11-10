// Package orgview is the projection of mdm/org that the employee component needs (protocol 02): legal entities (the
// time zone of their business dates), units (the org-tree path an appointment copies for the department dimension)
// and positions (an appointment hangs on one). The three are kept from mdm/org's events, aligned hourly and repaired
// from its source methods, aligned again when mdm/org's epoch changes. Whatever path writes a record (an event, a
// repair, an alignment), the Changed hook reacts: a unit's new path is copied onto its open appointments, and a
// retired unit or position queues the end of the appointments on it. The end is the appointments module's job (a
// tightening: its intent is written before, outside any transaction), never done here inside the write.
package orgview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	core "github.com/brickKit/mdm-employee/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/calls"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/projection"
	"github.com/brickKit/be-sdk-go/v3/work"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
)

// kind is one of mdm/org's aggregates as projected here.
type kind struct {
	proj      projection.Projection
	aggregate orgv3.Aggregate
	prefix    string // its event types' prefix
}

// View is one member's projection of mdm/org (core.OrgView): its three kinds carry hooks bound to the member, so a
// view is never shared between members of a process.
type View struct {
	m                               *core.Base
	legalEntities, units, positions kind
}

// New builds the member's view.
func New(m *core.Base) *View {
	v := &View{m: m,
		legalEntities: kind{projection.Projection{Name: "org_legal_entities"}, orgv3.Aggregate_AGGREGATE_LEGAL_ENTITY, "mdm.legal_entity."},
		units:         kind{projection.Projection{Name: "org_units"}, orgv3.Aggregate_AGGREGATE_ORG_UNIT, "mdm.org_unit."},
		positions:     kind{projection.Projection{Name: "org_positions"}, orgv3.Aggregate_AGGREGATE_POSITION, "mdm.org_position."},
	}
	v.units.proj.Changed = v.unitChanged
	v.positions.proj.Changed = v.positionChanged
	return v
}

func (v *View) all() []*kind { return []*kind{&v.legalEntities, &v.units, &v.positions} }

// EventTypes are the subscription's types: every event of the three, later actions and majors too (protocol P6.33).
var EventTypes = []string{"mdm.legal_entity.>", "mdm.org_unit.>", "mdm.org_position.>"}

// Job names of the projection's upkeep.
const (
	JobAlign   = "align-org"
	JobRepair  = "repair-org"
	JobRealign = "realign-org"
)

// OnEvent receives one of mdm/org's events: the projection's state table decides. An action this version does not
// know (a later mdm/org's) changes the record somehow, but its payload's shape is not known: the row is marked
// incomplete without taking anything from it, and a repair fetches it (protocol P6.33, protocol 02 PJ36).
func (v *View) OnEvent(ctx context.Context, tx db.Tx, e events.Received) error {
	m := v.m
	var k *kind
	for _, c := range v.all() {
		if strings.HasPrefix(e.Type, c.prefix) {
			k = c
		}
	}
	if k == nil {
		return nil // not one of the three: nothing here depends on it
	}
	in := projection.Input{RecordID: e.RecordID, Kind: string(e.Projection), Version: e.Version, Impl: e.Impl,
		Epoch: e.Epoch, Data: e.Data}
	if e.Type != k.prefix+"changed.v1" {
		in.Kind, in.Data = projection.Partial, json.RawMessage(`{}`)
	}
	out, err := k.proj.Apply(ctx, tx, in)
	if err == nil && out.Skipped == projection.NewerEpoch {
		return realignSoon(m, ctx, tx)
	}
	if err != nil || !out.Changed || !out.Fetch {
		return err
	}
	_, err = m.RT.Work.Enqueue(ctx, tx, JobRepair, struct{}{})
	return err
}

// unitChanged copies a unit's path onto the appointments that copied it, and queues the end of the open appointments
// in a retired unit.
func (v *View) unitChanged(ctx context.Context, tx db.Tx, id string) error {
	var u core.OrgUnit
	if ok, err := get(ctx, tx, &v.units, id, &u); err != nil || !ok {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE appointment SET unit_path = $2 WHERE unit_id = $1 AND unit_path <> $2`, id,
		u.Path); err != nil {
		return err
	}
	if u.State == "retired" {
		return endSoon(v.m, ctx, tx, core.Retirement{Kind: "unit", ID: id})
	}
	return nil
}

// positionChanged queues the end of the open appointments on a retired position.
func (v *View) positionChanged(ctx context.Context, tx db.Tx, id string) error {
	var p core.OrgPosition
	if ok, err := get(ctx, tx, &v.positions, id, &p); err != nil || !ok || p.State != "retired" {
		return err
	}
	return endSoon(v.m, ctx, tx, core.Retirement{Kind: "position", ID: id})
}

// endSoon queues the appointments module's job when an open appointment hangs on the retired record.
func endSoon(m *core.Base, ctx context.Context, tx db.Tx, r core.Retirement) error {
	col := r.Kind + "_id"
	var open bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM appointment WHERE `+col+` = $1 AND state <> 'ended')`,
		r.ID).Scan(&open); err != nil || !open {
		return err
	}
	_, err := m.RT.Work.Enqueue(ctx, tx, core.JobEndOnRetirement, r)
	return err
}

// get reads one complete record of a kind from the projection into v.
func get(ctx context.Context, tx db.Tx, k *kind, id string, v any) (bool, error) {
	state, _, data, ok, err := k.proj.Get(ctx, tx, id)
	if err != nil || !ok || state != projection.Complete {
		return false, err
	}
	return true, json.Unmarshal(data, v)
}

// lookup answers a record of a kind from the projection, filling it from mdm/org when the projection does not have it
// yet (a record created a moment ago, its event not here yet). Not found: false.
func lookup(m *core.Base, ctx context.Context, k *kind, id string, v any) (bool, error) {
	var found bool
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		found, err = get(ctx, tx, k, id, v)
		return err
	})
	if err != nil || found {
		return found, err
	}
	res, from, err := source{m, k}.Read(ctx, []string{id})
	if err != nil {
		return false, err
	}
	if len(res) != 1 || res[0].State != projection.Exists {
		return false, nil
	}
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		_, err := k.proj.Apply(ctx, tx, projection.Input{RecordID: id, Kind: projection.Full, Version: res[0].Version,
			Impl: from.Impl, Epoch: from.Epoch, Data: res[0].Data})
		return err
	})
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(res[0].Data, v)
}

// LegalEntity, Unit and Position answer a record from the projection (filling it from mdm/org when missing).
func (v *View) LegalEntity(ctx context.Context, id string) (core.OrgLegalEntity, bool, error) {
	var r core.OrgLegalEntity
	ok, err := lookup(v.m, ctx, &v.legalEntities, id, &r)
	return r, ok, err
}

func (v *View) Unit(ctx context.Context, id string) (core.OrgUnit, bool, error) {
	var r core.OrgUnit
	ok, err := lookup(v.m, ctx, &v.units, id, &r)
	return r, ok, err
}

func (v *View) Position(ctx context.Context, id string) (core.OrgPosition, bool, error) {
	var r core.OrgPosition
	ok, err := lookup(v.m, ctx, &v.positions, id, &r)
	return r, ok, err
}

// source is mdm/org's source methods for one aggregate, as the projection reads them.
type source struct {
	m *core.Base
	k *kind
}

func (s source) client() (orgv3.SourceServiceClient, error) {
	c, ok := s.m.RT.GRPC(core.Org)
	if !ok {
		return nil, errs.New(errs.ReasonDependencyUnavailable, core.Org+" is not reachable")
	}
	return orgv3.NewSourceServiceClient(c), nil
}

// identityOf is who answered (P9.18): mdm/org's stamp; its source methods answer from its primary only.
func identityOf(st *orgv3.Stamp) projection.Identity {
	return projection.Identity{Deployment: st.GetDeployment(), Impl: st.GetInstance(), Epoch: st.GetEpoch(), Primary: true}
}

func (s source) Snapshot(ctx context.Context, cursor string) ([]projection.Record, string, projection.Identity, error) {
	c, err := s.client()
	if err != nil {
		return nil, "", projection.Identity{}, err
	}
	res, err := c.Snapshot(ctx, &orgv3.SnapshotRequest{Aggregate: s.k.aggregate, Cursor: cursor}, calls.Idempotent())
	if err != nil {
		return nil, "", projection.Identity{}, err
	}
	var out []projection.Record
	for _, r := range res.GetRecords() {
		out = append(out, projection.Record{ID: r.GetId(), Version: r.GetVersion(), Data: r.GetData()})
	}
	return out, res.GetNextCursor(), identityOf(res.GetStamp()), nil
}

// Read reads in batches of MaxBatch; every batch must come from the same source, or the answer is not one.
func (s source) Read(ctx context.Context, ids []string) ([]projection.ReadResult, projection.Identity, error) {
	var from projection.Identity
	c, err := s.client()
	if err != nil {
		return nil, from, err
	}
	var out []projection.ReadResult
	for first := true; len(ids) > 0; first = false {
		n := min(len(ids), core.MaxBatch)
		res, err := c.Read(ctx, &orgv3.ReadRequest{Aggregate: s.k.aggregate, Ids: ids[:n]}, calls.Idempotent())
		if err != nil {
			return nil, from, err
		}
		if id := identityOf(res.GetStamp()); first {
			from = id
		} else if id != from {
			return nil, from, fmt.Errorf("%w: one read answered by %+v and by %+v", projection.ErrOtherSource, from, id)
		}
		for _, r := range res.GetResults() {
			st := projection.Purged
			switch r.GetState() {
			case orgv3.ReadState_READ_STATE_EXISTS:
				st = projection.Exists
			case orgv3.ReadState_READ_STATE_TOMBSTONE:
				st = projection.Gone
			}
			out = append(out, projection.ReadResult{ID: r.GetId(), State: st, Version: r.GetVersion(), Data: r.GetData()})
		}
		ids = ids[n:]
	}
	return out, from, nil
}

// realignSoon queues one alignment (one at a time: the row in org_realign), in the event's transaction; the
// alignment takes the source's current state, the skipped event's change with it.
func realignSoon(m *core.Base, ctx context.Context, tx db.Tx) error {
	tag, err := tx.Exec(ctx, "INSERT INTO org_realign DEFAULT VALUES ON CONFLICT DO NOTHING")
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	_, err = m.RT.Work.Enqueue(ctx, tx, JobRealign, struct{}{})
	return err
}

// Realign is the queued alignment: the row goes first, so an event of the new epoch arriving meanwhile queues another.
func (v *View) Realign(ctx context.Context, r *work.Run) error {
	if err := v.m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
		_, err := tx.Exec(ctx, "DELETE FROM org_realign")
		return err
	}); err != nil {
		return err
	}
	return v.Align(ctx, r)
}

// Align re-fetches every record of the three (P9.12), then repairs the rows waiting for a fetch: the safety net under
// the events.
func (v *View) Align(ctx context.Context, r *work.Run) error {
	var all []error
	for _, k := range v.all() {
		all = append(all, k.proj.Align(ctx, v.m.RT.DB, source{v.m, k}))
	}
	if err := errors.Join(all...); err != nil {
		return err
	}
	return v.Repair(ctx, r)
}

// Repair fetches the rows of the three waiting for a fetch.
func (v *View) Repair(ctx context.Context, _ *work.Run) error {
	var all []error
	for _, k := range v.all() {
		_, err := k.proj.Repair(ctx, v.m.RT.DB, source{v.m, k})
		all = append(all, err)
	}
	return errors.Join(all...)
}
