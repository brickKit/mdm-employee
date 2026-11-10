//go:build integration

package itest

import (
	"encoding/json"
	"time"

	core "github.com/brickKit/mdm-employee/v3/internal/core"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/sectest"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appointments (design.md, security 04): an appointment on a position of an active unit of the employee's legal
// entity (today: active; later: planned); never two overlapping on one position for one person; a transfer ends the
// old one (transfer, the day before) and makes a new one, never changing it in place; the note changes in place; an
// end by hand; the effective-dates job; a unit's move rewrites the copied path; a retired position ends what hangs on
// it; a termination ends every open appointment; the department dimension scopes the reads.
func (fx *fixture) appointments() {
	t := fx.t
	hire := func(no, name string) string {
		code, e := fx.alice.do("POST", "/api/employee/employees", map[string]any{"legal_entity_id": fx.le, "employee_no": no,
			"name": name, "hire_date": fx.madrid(-30)})
		require.Equal(t, 200, code, e)
		return e["id"].(string)
	}
	appoint := func(emp, position, start string, end any) (int, map[string]any) {
		return fx.alice.do("POST", "/api/employee/employees/"+emp+"/appointments", map[string]any{"position_id": position,
			"start_date": start, "end_date": end})
	}
	eva := hire("E0005", "Eva Soto")

	// on a position: the unit and the legal entity are the position's; today: active
	code, rep := appoint(eva, fx.rep, fx.madrid(0), nil)
	require.Equal(t, 200, code, rep)
	assert.Equal(t, "active", rep["state"])
	assert.Equal(t, fx.sales, rep["unit_id"])
	assert.Equal(t, fx.le, rep["legal_entity_id"])
	var path string
	require.NoError(t, fx.admin.QueryRow(fx.ctx, "SELECT unit_path FROM "+itSchema+".appointment WHERE id = $1", rep["id"]).Scan(&path))
	assert.Equal(t, fx.le+"/"+fx.sales, path, "the unit's path copied for the department dimension")

	// never two overlapping on one position for one person; an unknown position: refused
	code, overlap := appoint(eva, fx.rep, fx.madrid(5), nil)
	assert.Equal(t, 409, code)
	assert.Equal(t, "APPOINTMENT_OVERLAP", overlap["reason"])
	code, unknown := appoint(eva, ids.New().String(), fx.madrid(0), nil)
	assert.Equal(t, 409, code)
	assert.Equal(t, "ORG_NOT_KNOWN", unknown["reason"])

	// later: planned; a second position at the same time is fine (a concurrent appointment)
	code, head := appoint(eva, fx.head, fx.madrid(10), nil)
	require.Equal(t, 200, code, head)
	assert.Equal(t, "planned", head["state"])
	_, mine := fx.alice.do("GET", "/api/employee/employees/"+eva+"/appointments", nil)
	assert.Len(t, mine["appointments"], 2)
	_, holders := fx.alice.do("GET", "/api/employee/positions/"+fx.rep+"/holders", nil)
	require.Len(t, holders["holders"], 1)
	assert.Equal(t, eva, at(holders, "holders", 0, "employee", "id"))

	// the note changes in place; the planned one ends by hand
	code, noted := fx.alice.do("PATCH", "/api/employee/appointments/"+rep["id"].(string), map[string]any{"version": 1, "note": "key accounts"})
	require.Equal(t, 200, code, noted)
	assert.EqualValues(t, 2, noted["version"])
	code, ended := fx.alice.do("POST", "/api/employee/appointments/"+head["id"].(string)+"/end", nil)
	require.Equal(t, 200, code, ended)
	assert.Equal(t, "ended", ended["state"])
	assert.Equal(t, "ended", ended["end_reason"])

	// a transfer: the old one ends the day before (transfer), a new one on the new position; never in place
	code, moved := fx.alice.do("POST", "/api/employee/employees/"+eva+"/transfer", map[string]any{
		"from_appointment_id": rep["id"], "position_id": fx.clerk, "date": fx.madrid(0)})
	require.Equal(t, 200, code, moved)
	assert.Equal(t, rep["id"], at(moved, "ended", "id"))
	assert.Equal(t, "ended", at(moved, "ended", "state"))
	assert.Equal(t, "transfer", at(moved, "ended", "end_reason"))
	assert.Equal(t, fx.madrid(-1), at(moved, "ended", "end_date"))
	assert.NotEqual(t, rep["id"], at(moved, "appointment", "id"))
	assert.Equal(t, fx.ops, at(moved, "appointment", "unit_id"))
	assert.Equal(t, "active", at(moved, "appointment", "state"))
	clerk := at(moved, "appointment", "id").(string)

	// the department dimension: a reader of the sales subtree sees the sales appointment, not the operations one
	fx.w.Grant(sectest.Bob, sdkauthz.Segment{ResourceType: core.Appointments, Operation: core.OpApptRead,
		Atoms: map[string]sdkauthz.Atom{core.DimLegalEntity: {Kind: "all"},
			core.DimDepartment: {Kind: "tree", Items: []sdkauthz.TreeItem{{Mode: "subtree", Path: fx.le + "/" + fx.sales}}}},
		ReadFields: core.AppointmentFields, Valid: sdkauthz.Window{From: "2026-01-01T00:00:00Z"}})
	bob := client{t, fx.w.Bearer(sectest.Bob)}
	require.Eventually(t, func() bool {
		c, _ := bob.do("GET", "/api/employee/appointments/"+rep["id"].(string), nil)
		return c == 200
	},
		15*time.Second, 200*time.Millisecond, "bob reads the sales appointment")
	code, _ = bob.do("GET", "/api/employee/appointments/"+clerk, nil)
	assert.Equal(t, 404, code, "not in bob's subtree")

	// a unit moves (mdm/org's event): the open appointments' copied path follows
	fx.change(orgv3.Aggregate_AGGREGATE_ORG_UNIT, core.UnitChanged, fx.unit(fx.ops, fx.le+"/"+fx.sales+"/"+fx.ops, "active", 5))
	require.Eventually(t, func() bool {
		var p string
		_ = fx.admin.QueryRow(fx.ctx, "SELECT unit_path FROM "+itSchema+".appointment WHERE id = $1", clerk).Scan(&p)
		return p == fx.le+"/"+fx.sales+"/"+fx.ops
	}, 15*time.Second, 100*time.Millisecond, "the moved unit's path copied")
	require.Eventually(t, func() bool { c, _ := bob.do("GET", "/api/employee/appointments/"+clerk, nil); return c == 200 },
		15*time.Second, 200*time.Millisecond, "now under the sales subtree: bob reads it")

	// the effective-dates job: a planned one begins, an open one whose last day passed ends (expired)
	code, later := appoint(eva, fx.head, fx.madrid(20), fx.madrid(25))
	require.Equal(t, 200, code, later)
	_, err := fx.admin.Exec(fx.ctx, "UPDATE "+itSchema+".appointment SET start_date = $2, end_date = $3 WHERE id = $1",
		later["id"], fx.madrid(-3), fx.madrid(-1))
	require.NoError(t, err)
	fx.due("appointment-effective")
	require.Eventually(t, func() bool {
		_, a := fx.alice.do("GET", "/api/employee/appointments/"+later["id"].(string), nil)
		return a["state"] == "ended" && a["end_reason"] == "expired"
	}, 15*time.Second, 200*time.Millisecond, "the planned appointment whose days passed ends expired")

	// a retired position (mdm/org's event) ends the open appointments on it
	fx.change(orgv3.Aggregate_AGGREGATE_POSITION, core.PositionChanged, fx.position(fx.clerk, fx.ops, "OPS-CLERK", "retired", 6))
	require.Eventually(t, func() bool {
		_, a := fx.alice.do("GET", "/api/employee/appointments/"+clerk, nil)
		return a["state"] == "ended" && a["end_reason"] == "position_retired"
	}, 15*time.Second, 200*time.Millisecond, "the appointment on the retired position ended")
	code, refused := appoint(eva, fx.clerk, fx.madrid(0), nil)
	assert.Equal(t, 409, code, "no appointment on a retired position")
	assert.Equal(t, "ORG_NOT_KNOWN", refused["reason"])

	// a termination ends every open appointment
	finn := hire("E0006", "Finn Vidal")
	code, f1 := appoint(finn, fx.rep, fx.madrid(0), nil)
	require.Equal(t, 200, code, f1)
	code, f2 := appoint(finn, fx.head, fx.madrid(3), nil)
	require.Equal(t, 200, code, f2)
	code, _ = fx.alice.do("POST", "/api/employee/employees/"+finn+"/terminate", map[string]any{"termination_date": fx.madrid(0)})
	require.Equal(t, 200, code)
	for _, a := range []string{f1["id"].(string), f2["id"].(string)} {
		_, got := fx.alice.do("GET", "/api/employee/appointments/"+a, nil)
		assert.Equal(t, "ended", got["state"])
		assert.Equal(t, "termination", got["end_reason"])
	}
	code, _ = appoint(finn, fx.rep, fx.madrid(0), nil)
	assert.Equal(t, 409, code, "no appointment after the termination")

	// the events: the full state, the employee as subject
	var raw []byte
	require.NoError(t, fx.admin.QueryRow(fx.ctx, "SELECT subjects FROM "+itSchema+".be_outbox WHERE type = $1 AND record_id = $2 LIMIT 1",
		core.AppointmentChanged, clerk).Scan(&raw))
	var subjects []string
	require.NoError(t, json.Unmarshal(raw, &subjects))
	assert.Equal(t, []string{core.EmployeeSubject(eva)}, subjects)
	fx.eva = eva
}
