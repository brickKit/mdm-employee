//go:build integration

package itest

import (
	"encoding/json"
	"time"

	core "github.com/brickKit/mdm-employee/v3/internal/core"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/bizdate"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/sectest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// madrid is a business date in the test legal entity's time zone, days from today.
func (fx *fixture) madrid(days int) string {
	today, err := bizdate.Today("Europe/Madrid")
	require.NoError(fx.t, err)
	d, err := time.Parse(time.DateOnly, today)
	require.NoError(fx.t, err)
	return d.AddDate(0, 0, days).Format(time.DateOnly)
}

// employees: hiring (today: active; later: pre_hire), the number the legal entity's once, an unknown legal entity
// refused; the restricted personal data only for a grant that reads it, never in an event or a list; changes by
// version; a hire that has not begun cancelled; a termination on a later date carried on as active until then and
// withdrawn, one today terminating at once; the effective-dates job carrying out a hire and a termination that came
// due.
func (fx *fixture) employees() {
	t := fx.t
	hire := func(no, name, date string, personal map[string]any) (int, map[string]any) {
		body := map[string]any{"legal_entity_id": fx.le, "employee_no": no, "name": name, "hire_date": date}
		if personal != nil {
			body["personal"] = personal
		}
		return fx.alice.do("POST", "/api/employee/employees", body)
	}

	// today: active; the personal data in the answer for a caller who may read it
	code, ana := hire("E0001", "Ana García", fx.madrid(0), map[string]any{"birth_date": "1990-04-02", "national_id": "X1234567"})
	require.Equal(t, 200, code, ana)
	assert.Equal(t, "active", ana["state"])
	assert.Equal(t, fx.le, ana["legal_entity_id"])
	assert.EqualValues(t, 1, ana["version"])
	assert.Equal(t, "1990-04-02", at(ana, "personal", "birth_date"))
	fx.ana = ana["id"].(string)

	// the same hire again answers it; the number is the legal entity's once
	code, again := hire("E0001", "Ana García", fx.madrid(0), nil)
	require.Equal(t, 200, code, again)
	assert.Equal(t, fx.ana, again["id"])
	code, taken := hire("E0001", "Someone Else", fx.madrid(0), nil)
	assert.Equal(t, 409, code)
	assert.Equal(t, "EMPLOYEE_NO_TAKEN", taken["reason"])

	// a later date: pre_hire; an unknown legal entity: refused, nothing guessed
	code, ben := hire("E0002", "Ben Ruiz", fx.madrid(30), nil)
	require.Equal(t, 200, code, ben)
	assert.Equal(t, "pre_hire", ben["state"])
	code, unknown := fx.alice.do("POST", "/api/employee/employees", map[string]any{"legal_entity_id": ids.New().String(),
		"employee_no": "E0009", "name": "Nobody", "hire_date": fx.madrid(0)})
	assert.Equal(t, 409, code)
	assert.Equal(t, "ORG_NOT_KNOWN", unknown["reason"])

	// a reader without the personal data's field: the field is masked, not missing (P8.31)
	fx.w.Grant(sectest.Bob, sdkauthz.Segment{ResourceType: core.Employees, Operation: core.OpEmployeeRead,
		Atoms:      map[string]sdkauthz.Atom{core.DimLegalEntity: {Kind: "in", Values: []string{fx.le}}},
		ReadFields: core.EmployeeFields, Valid: sdkauthz.Window{From: "2026-01-01T00:00:00Z"}})
	bob := client{t, fx.w.Bearer(sectest.Bob)}
	var seen map[string]any
	require.Eventually(t, func() bool {
		c, out := bob.do("GET", "/api/employee/employees/"+fx.ana, nil)
		seen = out
		return c == 200
	},
		15*time.Second, 200*time.Millisecond, "bob's grant reaches the member")
	assert.Nil(t, seen["personal"])
	assert.Contains(t, seen["_masked"], "personal")
	assert.Equal(t, "Ana García", seen["name"])
	_, full := fx.alice.do("GET", "/api/employee/employees/"+fx.ana, nil)
	assert.Equal(t, "X1234567", at(full, "personal", "national_id"))

	// changes by version; the number, the legal entity and the state are not changed here
	code, renamed := fx.alice.do("PATCH", "/api/employee/employees/"+fx.ana, map[string]any{"version": 1,
		"name": "Ana García López", "names": map[string]string{"zh-CN": "安娜"}})
	require.Equal(t, 200, code, renamed)
	assert.EqualValues(t, 2, renamed["version"])
	code, stale := fx.alice.do("PATCH", "/api/employee/employees/"+fx.ana, map[string]any{"version": 1, "name": "x"})
	assert.Equal(t, 409, code)
	assert.Equal(t, "VERSION_CONFLICT", stale["reason"])

	// the list: by legal entity and keyword, by number; no personal data in a list
	_, page := fx.alice.do("GET", "/api/employee/employees?legal_entity_id="+fx.le, nil)
	list := page["employees"].([]any)
	require.Len(t, list, 2)
	assert.Equal(t, "E0001", at(list[0], "employee_no"))
	assert.Nil(t, at(list[0], "personal"))
	_, found := fx.alice.do("GET", "/api/employee/employees?q=E0002", nil)
	assert.Len(t, found["employees"], 1)

	// a hire that has not begun is cancelled; one that has, is not
	code, cancelled := fx.alice.do("POST", "/api/employee/employees/"+ben["id"].(string)+"/cancel-hire", nil)
	require.Equal(t, 200, code, cancelled)
	assert.Equal(t, "terminated", cancelled["state"])
	assert.Equal(t, "cancelled", cancelled["end_reason"])
	code, refused := fx.alice.do("POST", "/api/employee/employees/"+fx.ana+"/cancel-hire", nil)
	assert.Equal(t, 409, code)
	assert.Equal(t, "STATE_CONFLICT", refused["reason"])

	// a termination on a later date: still active, the date recorded; withdrawn before it comes
	code, later := fx.alice.do("POST", "/api/employee/employees/"+fx.ana+"/terminate", map[string]any{"termination_date": fx.madrid(10)})
	require.Equal(t, 200, code, later)
	assert.Equal(t, "active", later["state"])
	assert.Equal(t, fx.madrid(10), later["termination_date"])
	code, withdrawn := fx.alice.do("POST", "/api/employee/employees/"+fx.ana+"/withdraw-termination", nil)
	require.Equal(t, 200, code, withdrawn)
	assert.Nil(t, withdrawn["termination_date"])
	assert.Equal(t, "active", withdrawn["state"])

	// the effective-dates job: a hire and a termination that came due are carried out
	code, cara := hire("E0003", "Cara Gil", fx.madrid(5), nil)
	require.Equal(t, 200, code, cara)
	code, dan := hire("E0004", "Dan Mora", fx.madrid(0), nil)
	require.Equal(t, 200, code, dan)
	code, _ = fx.alice.do("POST", "/api/employee/employees/"+dan["id"].(string)+"/terminate", map[string]any{"termination_date": fx.madrid(5)})
	require.Equal(t, 200, code)
	// time passes: the dates come due
	_, err := fx.admin.Exec(fx.ctx, "UPDATE "+itSchema+".employee SET hire_date = $2 WHERE id = $1", cara["id"], fx.madrid(0))
	require.NoError(t, err)
	_, err = fx.admin.Exec(fx.ctx, "UPDATE "+itSchema+".employee SET termination_date = $2 WHERE id = $1", dan["id"], fx.madrid(0))
	require.NoError(t, err)
	fx.due("employee-effective")
	require.Eventually(t, func() bool {
		_, c := fx.alice.do("GET", "/api/employee/employees/"+cara["id"].(string), nil)
		_, d := fx.alice.do("GET", "/api/employee/employees/"+dan["id"].(string), nil)
		return c["state"] == "active" && d["state"] == "terminated" && d["end_reason"] == "terminated"
	}, 15*time.Second, 200*time.Millisecond, "the hire and the termination carried out")

	// a termination today: at once; and again: refused
	code, gone := fx.alice.do("POST", "/api/employee/employees/"+fx.ana+"/terminate", map[string]any{"termination_date": fx.madrid(0)})
	require.Equal(t, 200, code, gone)
	assert.Equal(t, "terminated", gone["state"])
	assert.Equal(t, "terminated", gone["end_reason"])
	assert.Equal(t, false, gone["last_admin_warning"], "no account linked: nothing to warn about")
	code, twice := fx.alice.do("POST", "/api/employee/employees/"+fx.ana+"/terminate", map[string]any{"termination_date": fx.madrid(0)})
	assert.Equal(t, 409, code)
	assert.Equal(t, "STATE_CONFLICT", twice["reason"])

	// the events: the full state with the employee as subject, never the personal data
	rows, err := fx.admin.Query(fx.ctx, "SELECT payload, subjects FROM "+itSchema+".be_outbox WHERE type = $1 AND record_id = $2",
		core.EmployeeChanged, fx.ana)
	require.NoError(t, err)
	n := 0
	for rows.Next() {
		var payload, subjectsRaw []byte
		require.NoError(t, rows.Scan(&payload, &subjectsRaw))
		var subjects []string
		require.NoError(t, json.Unmarshal(subjectsRaw, &subjects))
		var data map[string]any
		require.NoError(t, json.Unmarshal(payload, &data))
		assert.NotContains(t, data, "personal")
		assert.NotContains(t, string(payload), "X1234567")
		assert.Equal(t, []string{core.EmployeeSubject(fx.ana)}, subjects)
		n++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, 5, n, "hired, renamed, terminated later, withdrawn, terminated")
}
