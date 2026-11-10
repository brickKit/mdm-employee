//go:build integration

package itest

import (
	"encoding/json"
	"time"

	core "github.com/brickKit/mdm-employee/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/events"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// orgProjection: mdm/org's events reach the three projections (legal entities, units, positions); an action this
// version does not know marks the record incomplete and a repair fetches it from mdm/org's source methods; a full
// alignment (hourly in a deployment, here run at once) takes every record the stand-in has.
func (fx *fixture) orgProjection() {
	t := fx.t
	eventually := func(projection, id string, want func(string, map[string]any) bool, why string) {
		t.Helper()
		require.Eventually(t, func() bool { s, d := fx.projected(projection, id); return want(s, d) }, 15*time.Second,
			100*time.Millisecond, why)
	}
	complete := func(s string, _ map[string]any) bool { return s == "complete" }
	at := func(version float64) func(string, map[string]any) bool { // the version the event or the repair brought
		return func(s string, d map[string]any) bool { return s == "complete" && d["version"] == version }
	}
	// a starting member aligns first (its next alignment is an hour away): the records are there at the stand-in's
	// first versions; the checks below wait for the versions the events bring, not for what that alignment left
	require.Eventually(t, func() bool {
		var later bool
		err := fx.admin.QueryRow(fx.ctx, "SELECT run_at > now() + interval '30 minutes' FROM "+itSchema+
			".be_work_item WHERE id = 'align-org' AND state = 'pending'").Scan(&later)
		return err == nil && later
	}, 15*time.Second, 100*time.Millisecond, "the first alignment done")

	// the events
	fx.change(orgv3.Aggregate_AGGREGATE_LEGAL_ENTITY, core.LegalEntityChanged, map[string]any{"id": fx.le, "code": "ES01",
		"state": "active", "timezone": "Europe/Madrid", "version": int64(2)})
	fx.change(orgv3.Aggregate_AGGREGATE_ORG_UNIT, core.UnitChanged, fx.unit(fx.sales, fx.le+"/"+fx.sales, "active", 2))
	fx.change(orgv3.Aggregate_AGGREGATE_POSITION, core.PositionChanged, fx.position(fx.rep, fx.sales, "SALES-REP", "active", 2))
	eventually("org_legal_entities", fx.le, func(s string, d map[string]any) bool {
		return s == "complete" && d["timezone"] == "Europe/Madrid" && d["version"] == float64(2)
	}, "the legal entity's event")
	eventually("org_units", fx.sales, at(2), "the unit's event")
	eventually("org_positions", fx.rep, at(2), "the position's event")

	// an action a later mdm/org has, its payload of a shape this version does not know (even sent as a full state):
	// nothing is taken from it; incomplete, then repaired from the source methods (P6.33, PJ36)
	fx.org.put(orgv3.Aggregate_AGGREGATE_POSITION, fx.position(fx.clerk, fx.ops, "OPS-CLERK", "active", 3))
	fx.publishOrg("mdm.org_position.renamed.v1", fx.clerk, 3, events.Full, json.RawMessage(`{"new_name":"Clerk"}`))
	eventually("org_positions", fx.clerk, func(s string, d map[string]any) bool {
		return s == "complete" && d["code"] == "OPS-CLERK" && d["version"] == float64(3)
	}, "the unknown action repaired")

	// the alignment takes what no event brought (the stand-in's records that were never published)
	require.Eventually(t, func() bool {
		tag, err := fx.admin.Exec(fx.ctx, "UPDATE "+itSchema+".be_work_item SET run_at = now() WHERE id = 'align-org' AND state = 'pending'")
		return err == nil && tag.RowsAffected() == 1
	}, 10*time.Second, 100*time.Millisecond, "the singleton align-org made due now")
	eventually("org_units", fx.ops, complete, "the unit no event brought, aligned")
	eventually("org_positions", fx.head, complete, "the position no event brought, aligned")
	state, data := fx.projected("org_units", fx.ops)
	assert.Equal(t, "complete", state)
	assert.Equal(t, fx.le+"/"+fx.ops, data["path"])
}
