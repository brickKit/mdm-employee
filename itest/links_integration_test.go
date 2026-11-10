//go:build integration

package itest

import (
	"encoding/json"

	core "github.com/brickKit/mdm-employee/v3/internal/core"

	authzv3 "github.com/brickKit/contract-infra-authz/v3/gen/go/be/authz/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// links (security 02, 08): an account links to an employee only with a confirmation the authorization member holds
// (asked with CheckLinkChange before the transaction: none, 202 and nothing changed; refused, PERMISSION_DENIED;
// unprovable now, 202 processing); the employee's version the confirmation binds; a confirmation used once here; an
// account linked to one employee at a time, an employee to several accounts; the end of a link needs one too; the
// last-admin warning (D62) on a termination; a termination leaves the links, and no new link after it.
func (fx *fixture) links() {
	t := fx.t
	code, gil := fx.alice.do("POST", "/api/employee/employees", map[string]any{"legal_entity_id": fx.le,
		"employee_no": "E0007", "name": "Gil Pons", "hire_date": fx.madrid(-10)})
	require.Equal(t, 200, code, gil)
	id, version := gil["id"].(string), gil["version"]
	link := func(emp, account string, version any, confirmation string) (int, map[string]any) {
		body := map[string]any{"account": account, "version": version}
		if confirmation != "" {
			body["confirmation"] = confirmation
		}
		return fx.alice.do("POST", "/api/employee/employees/"+emp+"/accounts", body)
	}

	// no confirmation: 202, nothing changed; the authorization member was asked with the employee's version
	code, waiting := link(id, "acct-gil-1", version, "")
	require.Equal(t, 202, code, waiting)
	assert.Equal(t, "CONFIRMATION_REQUIRED", waiting["reason"])
	_, none := fx.alice.do("GET", "/api/employee/employees/"+id+"/accounts", nil)
	assert.Empty(t, none["links"])
	asked := fx.w.LinkChecks()
	require.NotEmpty(t, asked)
	last := asked[len(asked)-1]
	assert.Equal(t, id, last.GetEmployee())
	assert.Equal(t, "acct-gil-1", last.GetAccount())
	assert.Equal(t, "link", last.GetAction())
	assert.EqualValues(t, version, last.GetEmployeeVersion())

	// with one: linked, the confirmation on the link
	code, l1 := link(id, "acct-gil-1", version, "conf-1")
	require.Equal(t, 200, code, l1)
	assert.Equal(t, "active", l1["state"])
	assert.Equal(t, "conf-1", l1["confirmation"])

	// a confirmation is used once here (the authorization member still finds it unused until it applies the link)
	code, reused := link(id, "acct-gil-2", version, "conf-1")
	assert.Equal(t, 409, code)
	assert.Equal(t, "CONFIRMATION_USED", reused["reason"])
	// a second account of the same employee
	code, l2 := link(id, "acct-gil-2", version, "conf-2")
	require.Equal(t, 200, code, l2)
	_, both := fx.alice.do("GET", "/api/employee/employees/"+id+"/accounts", nil)
	assert.Len(t, both["links"], 2)

	// an account is linked to one employee at a time; the version the confirmation binds must be the employee's
	code, hana := fx.alice.do("POST", "/api/employee/employees", map[string]any{"legal_entity_id": fx.le,
		"employee_no": "E0008", "name": "Hana Roig", "hire_date": fx.madrid(-10)})
	require.Equal(t, 200, code, hana)
	code, taken := link(hana["id"].(string), "acct-gil-1", hana["version"], "conf-3")
	assert.Equal(t, 409, code)
	assert.Equal(t, "ACCOUNT_LINKED", taken["reason"])
	code, stale := link(hana["id"].(string), "acct-hana", int64(99), "conf-4")
	assert.Equal(t, 409, code)
	assert.Equal(t, "VERSION_CONFLICT", stale["reason"])

	// refused by the authorization member: denied; not provable now: 202 processing; nothing changed either way
	fx.w.LinkCheck(func(*authzv3.CheckLinkChangeRequest) *authzv3.CheckLinkChangeResponse {
		return &authzv3.CheckLinkChangeResponse{Verdict: authzv3.AccountChangeVerdict_ACCOUNT_CHANGE_VERDICT_REFUSED, Reason: "the requester's own"}
	})
	code, denied := link(hana["id"].(string), "acct-hana", hana["version"], "conf-5")
	assert.Equal(t, 403, code)
	assert.Equal(t, "PERMISSION_DENIED", denied["reason"])
	fx.w.LinkCheck(func(*authzv3.CheckLinkChangeRequest) *authzv3.CheckLinkChangeResponse {
		return &authzv3.CheckLinkChangeResponse{Verdict: authzv3.AccountChangeVerdict_ACCOUNT_CHANGE_VERDICT_UNPROVABLE}
	})
	code, later := link(hana["id"].(string), "acct-hana", hana["version"], "conf-6")
	assert.Equal(t, 202, code)
	assert.Equal(t, "processing", later["outcome"])
	fx.w.LinkCheck(nil)
	_, hanas := fx.alice.do("GET", "/api/employee/employees/"+hana["id"].(string)+"/accounts", nil)
	assert.Empty(t, hanas["links"])

	// the employee of an account
	_, whose := fx.alice.do("GET", "/api/employee/accounts/acct-gil-1/employee", nil)
	assert.Equal(t, id, whose["id"])

	// the end of a link needs a confirmation too; the end of an admin's account warns (D62)
	code, wait := fx.alice.do("POST", "/api/employee/account-links/"+l2["id"].(string)+"/end", map[string]any{"version": version})
	assert.Equal(t, 202, code, wait)
	fx.w.Admins("acct-gil-2")
	code, ended := fx.alice.do("POST", "/api/employee/account-links/"+l2["id"].(string)+"/end",
		map[string]any{"version": version, "confirmation": "conf-7"})
	require.Equal(t, 200, code, ended)
	assert.Equal(t, "ended", ended["state"])
	assert.Equal(t, "conf-7", ended["end_confirmation"])
	assert.Equal(t, true, ended["last_admin_warning"])
	end := fx.w.LinkChecks()[len(fx.w.LinkChecks())-1]
	assert.Equal(t, "unlink", end.GetAction())
	code, _ = fx.alice.do("GET", "/api/employee/accounts/acct-gil-2/employee", nil)
	assert.Equal(t, 404, code, "an ended link answers no employee")

	// a termination warns when a linked account is an admin, never refuses; the links stay; no new link after it
	fx.w.Admins("acct-gil-1")
	code, gone := fx.alice.do("POST", "/api/employee/employees/"+id+"/terminate", map[string]any{"termination_date": fx.madrid(0)})
	require.Equal(t, 200, code, gone)
	assert.Equal(t, "terminated", gone["state"])
	assert.Equal(t, true, gone["last_admin_warning"])
	fx.w.Admins()
	_, after := fx.alice.do("GET", "/api/employee/employees/"+id+"/accounts", nil)
	assert.Equal(t, "active", at(after, "links", 0, "state"), "the termination leaves the link")
	code, refused := link(id, "acct-gil-3", gone["version"], "conf-8")
	assert.Equal(t, 409, code)
	assert.Equal(t, "STATE_CONFLICT", refused["reason"])

	// the events: the employee and the account as subjects
	var raw []byte
	require.NoError(t, fx.admin.QueryRow(fx.ctx, "SELECT subjects FROM "+itSchema+".be_outbox WHERE type = $1 AND record_id = $2 LIMIT 1",
		core.LinkChanged, l1["id"]).Scan(&raw))
	var subjects []string
	require.NoError(t, json.Unmarshal(raw, &subjects))
	assert.ElementsMatch(t, []string{core.EmployeeSubject(id), core.AccountSubject("acct-gil-1")}, subjects)
	fx.gil = id
}
