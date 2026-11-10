//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	core "github.com/brickKit/mdm-employee/v3/internal/core"
	tightening "github.com/brickKit/mdm-employee/v3/internal/tightening"

	"github.com/brickKit/be-sdk-go/v3/evidence"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/projection"
	"github.com/brickKit/be-sdk-go/v3/recoverypb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// evidenceOf returns the records the test deployment's evidence store keeps about one object, by phase.
func (fx *fixture) evidenceOf(object string) map[string][]map[string]any {
	out := map[string][]map[string]any{}
	for _, raw := range fx.w.Evidence() {
		var r map[string]any
		if json.Unmarshal(raw, &r) != nil {
			continue
		}
		if o, _ := r["object"].(map[string]any); o != nil && o["id"] == object {
			out[r["phase"].(string)] = append(out[r["phase"].(string)], r)
		}
	}
	return out
}

func (fx *fixture) awaitEvidence(id, phase string, n int) map[string][]map[string]any {
	fx.t.Helper()
	var got map[string][]map[string]any
	require.Eventually(fx.t, func() bool { got = fx.evidenceOf(id); return len(got[phase]) >= n }, 10*time.Second,
		100*time.Millisecond, "%s: %d %s record(s)", id, n, phase)
	return got
}

// evidence (assembly protocol 15, be-protocol P12.15-P12.18): a termination, the end of an appointment and the end of
// a link write their intent before the change and their conclusion in its transaction; one that does not take place
// is cancelled; hires, appointments, links and a termination on a later date (nothing ends yet) write none; with the
// evidence store down the change goes on and its conclusion waits locally.
func (fx *fixture) evidence() {
	t := fx.t
	code, ivo := fx.alice.do("POST", "/api/employee/employees", map[string]any{"legal_entity_id": fx.le,
		"employee_no": "E0010", "name": "Ivo Bas", "hire_date": fx.madrid(-10)})
	require.Equal(t, 200, code, ivo)
	id := ivo["id"].(string)
	code, appt := fx.alice.do("POST", "/api/employee/employees/"+id+"/appointments", map[string]any{"position_id": fx.rep,
		"start_date": fx.madrid(-5)})
	require.Equal(t, 200, code, appt)
	code, link := fx.alice.do("POST", "/api/employee/employees/"+id+"/accounts", map[string]any{"account": "acct-ivo",
		"version": ivo["version"], "confirmation": "conf-ivo-1"})
	require.Equal(t, 200, code, link)
	assert.Empty(t, fx.evidenceOf(id), "a hire is no tightening")
	assert.Empty(t, fx.evidenceOf(appt["id"].(string)), "an appointment is no tightening")
	assert.Empty(t, fx.evidenceOf(link["id"].(string)), "a link is no tightening")

	// the end of a link: the intent (the version before), then the conclusion, one record in two phases
	code, ended := fx.alice.do("POST", "/api/employee/account-links/"+link["id"].(string)+"/end",
		map[string]any{"version": ivo["version"], "confirmation": "conf-ivo-2"})
	require.Equal(t, 200, code, ended)
	got := fx.awaitEvidence(link["id"].(string), "effective", 1)
	require.Len(t, got["intent"], 1)
	in, eff := got["intent"][0], got["effective"][0]
	assert.Equal(t, in["id"], eff["id"])
	assert.Equal(t, tightening.Category, in["category"])
	assert.Equal(t, map[string]any{"type": tightening.LinkType, "id": link["id"], "version": "1"}, in["object"])
	assert.Equal(t, "link_end", in["change"].(map[string]any)["kind"])
	assert.Equal(t, "acct-ivo", in["change"].(map[string]any)["account"])
	assert.NotNil(t, eff["position"])

	// the end of an appointment by hand
	code, endedAppt := fx.alice.do("POST", "/api/employee/appointments/"+appt["id"].(string)+"/end", nil)
	require.Equal(t, 200, code, endedAppt)
	got = fx.awaitEvidence(appt["id"].(string), "effective", 1)
	assert.Equal(t, "appointment_end", got["intent"][0]["change"].(map[string]any)["kind"])
	assert.Equal(t, "ended", got["intent"][0]["change"].(map[string]any)["reason"])
	assert.Equal(t, tightening.AppointmentType, got["intent"][0]["object"].(map[string]any)["type"])

	// a termination on a later date: nothing ends yet, no record; refused (before the hire date): cancelled
	code, _ = fx.alice.do("POST", "/api/employee/employees/"+id+"/terminate", map[string]any{"termination_date": fx.madrid(30)})
	require.Equal(t, 200, code)
	assert.Empty(t, fx.evidenceOf(id)["intent"], "a later termination ends nothing yet")
	code, _ = fx.alice.do("POST", "/api/employee/employees/"+id+"/withdraw-termination", nil)
	require.Equal(t, 200, code)
	code, _ = fx.alice.do("POST", "/api/employee/employees/"+id+"/terminate", map[string]any{"termination_date": fx.madrid(-20)})
	require.Equal(t, 400, code)
	got = fx.awaitEvidence(id, "cancelled", 1)
	assert.Len(t, got["intent"], 1)
	assert.Empty(t, got["effective"])

	// the evidence store down: the termination goes on; its conclusion waits in the evidence outbox until it is back
	fx.w.EvidenceDown(true)
	code, gone := fx.alice.do("POST", "/api/employee/employees/"+id+"/terminate", map[string]any{"termination_date": fx.madrid(0)})
	require.Equal(t, 200, code, gone)
	assert.Equal(t, "terminated", gone["state"], "the tightening is not held back")
	var waiting int
	require.NoError(t, fx.admin.QueryRow(fx.ctx, "SELECT count(*) FROM "+itSchema+".be_evidence_outbox WHERE phase = 'effective'").Scan(&waiting))
	assert.Positive(t, waiting, "the conclusion waits locally")
	fx.w.EvidenceDown(false)
	got = fx.awaitEvidence(id, "effective", 1)
	assert.Equal(t, "termination", got["effective"][0]["change"].(map[string]any)["kind"])
	assert.Equal(t, fx.madrid(0), got["effective"][0]["change"].(map[string]any)["termination_date"])
	fx.ivo, fx.ivoLink = id, link["id"].(string)
}

// replay (be-protocol P12.19-P12.21): the recovery tool replays this member's evidence: the database is put back as it
// was before a termination and the end of a link (their rows and their change history rows: a restore to T); the
// replay re-applies each as recorded (the termination ends the open appointments again) and again answers reflected.
func (fx *fixture) replay() {
	t, ctx := fx.t, context.Background()
	conn, err := grpc.NewClient(itSystem, grpc.WithTransportCredentials(credentials.NewTLS(fx.w.TLS(t, "ops/recover").ClientConfig(core.ID))))
	require.NoError(t, err)
	defer conn.Close()
	replay := func(raw []byte) string {
		t.Helper()
		outcome, _, err := evidence.ReplayClient{Conn: conn}.Replay(ctx, raw, "r1")
		require.NoError(t, err)
		return outcome
	}
	effective := func(object string) ([]byte, string) {
		t.Helper()
		for k, v := range fx.w.Evidence() {
			if strings.HasSuffix(k, "/effective") {
				var r map[string]any
				if json.Unmarshal(v, &r) == nil && r["object"].(map[string]any)["id"] == object {
					return v, r["id"].(string)
				}
			}
		}
		t.Fatalf("no effective record of %s", object)
		return nil, ""
	}
	forget := func(evidenceID string) {
		_, err := fx.admin.Exec(ctx, "DELETE FROM "+itSchema+".be_change_history WHERE evidence_id = $1", evidenceID)
		require.NoError(t, err)
	}

	// the termination: the employee back to active with an open appointment, as before it
	raw, eid := effective(fx.ivo)
	code, appt := fx.alice.do("POST", "/api/employee/employees/"+fx.eva+"/appointments", map[string]any{"position_id": fx.head,
		"start_date": fx.madrid(-1)}) // a fresh appointment to stand for one open at T
	require.Equal(t, 200, code, appt)
	_, err = fx.admin.Exec(ctx, "UPDATE "+itSchema+".appointment SET employee_id = $1 WHERE id = $2", fx.ivo, appt["id"])
	require.NoError(t, err)
	_, err = fx.admin.Exec(ctx, "UPDATE "+itSchema+".employee SET state = 'active', end_reason = NULL, termination_date = NULL WHERE id = $1", fx.ivo)
	require.NoError(t, err)
	forget(eid)
	assert.Equal(t, evidence.Applied, replay(raw), "re-applied as recorded")
	_, e := fx.alice.do("GET", "/api/employee/employees/"+fx.ivo, nil)
	assert.Equal(t, "terminated", e["state"])
	_, a := fx.alice.do("GET", "/api/employee/appointments/"+appt["id"].(string), nil)
	assert.Equal(t, "ended", a["state"], "the termination ends the open appointments again")
	assert.Equal(t, evidence.AlreadyReflected, replay(raw), "again: reflected")

	// the end of a link: back to active
	raw, eid = effective(fx.ivoLink)
	_, err = fx.admin.Exec(ctx, "UPDATE "+itSchema+".account_link SET state = 'active', ended_at = NULL, end_confirmation = NULL WHERE id = $1", fx.ivoLink)
	require.NoError(t, err)
	forget(eid)
	assert.Equal(t, evidence.Applied, replay(raw))
	var state string
	require.NoError(t, fx.admin.QueryRow(ctx, "SELECT state FROM "+itSchema+".account_link WHERE id = $1", fx.ivoLink).Scan(&state))
	assert.Equal(t, "ended", state)
	var audited int
	require.NoError(t, fx.admin.QueryRow(ctx, "SELECT count(*) FROM "+itSchema+".be_audit_log WHERE actor::text LIKE '%ops/recover%'").Scan(&audited))
	assert.Positive(t, audited, "audited with the recovery tool as the actor")

	// an object no longer there: dangling (for review)
	missing, err := evidence.Seal(map[string]any{"record_type": evidence.RecordType, "schema": "1.0", "id": ids.New().String(),
		"recorder": map[string]any{"component": core.ID, "instance": "i"}, "category": tightening.Category, "phase": "effective",
		"object": map[string]any{"type": tightening.EmployeeType, "id": ids.New().String(), "version": "1"},
		"change": map[string]any{"kind": "termination", "termination_date": fx.madrid(0)},
		"at":     "2026-11-05T10:00:00.000000Z", "position": map[string]any{"epoch": "1.1", "commit_seq": 1}})
	require.NoError(t, err)
	assert.Equal(t, evidence.Dangling, replay(missing.Record))
}

// source: the watermark stream's record read (P9.30): the authorization member reads the three kinds by ID (an
// unknown ID purged) and pages the snapshot; another component is refused.
func (fx *fixture) source() {
	t, ctx := fx.t, context.Background()
	dial := func(as string) *grpc.ClientConn {
		conn, err := grpc.NewClient(itSystem, grpc.WithTransportCredentials(credentials.NewTLS(fx.w.TLS(t, as).ClientConfig(core.ID))))
		require.NoError(t, err)
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	rc := projection.RecordClient{Conn: dial(core.Authz), Stream: core.Stream}
	res, _, err := rc.Read(ctx, []string{fx.ivo, fx.ivoLink, ids.New().String()})
	require.NoError(t, err)
	require.Len(t, res, 3)
	assert.Equal(t, projection.Exists, res[0].State)
	assert.Contains(t, string(res[0].Data), `"state":"terminated"`)
	assert.NotContains(t, string(res[0].Data), "personal", "no restricted data in the record read")
	assert.Equal(t, projection.Exists, res[1].State)
	assert.Equal(t, projection.Purged, res[2].State)
	page, next, _, err := rc.Snapshot(ctx, "")
	require.NoError(t, err)
	assert.NotEmpty(t, page)
	var all int
	for cursor := next; ; {
		all += len(page)
		if cursor == "" {
			break
		}
		page, cursor, _, err = rc.Snapshot(ctx, cursor)
		require.NoError(t, err)
	}
	var rows int
	require.NoError(t, fx.admin.QueryRow(ctx, "SELECT (SELECT count(*) FROM "+itSchema+".employee) + (SELECT count(*) FROM "+itSchema+
		".appointment) + (SELECT count(*) FROM "+itSchema+".account_link)").Scan(&rows))
	assert.Equal(t, rows, all, "the snapshot gives every record of the three kinds")
	_, _, err = projection.RecordClient{Conn: dial(core.Org), Stream: core.Stream}.Read(ctx, []string{fx.ivo})
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "not a reader: %v", err)
}

// recoveryChecks: a full recovery's checks (be-protocol chapter 14): the recovery tool only; the audit of a change
// names the object's version after it, and this member answers whether each kind of object is still there at it.
func (fx *fixture) recoveryChecks() {
	t, ctx := fx.t, context.Background()
	dial := func(as string) recoverypb.CheckServiceClient {
		conn, err := grpc.NewClient(itSystem, grpc.WithTransportCredentials(credentials.NewTLS(fx.w.TLS(t, as).ClientConfig(core.ID))))
		require.NoError(t, err)
		t.Cleanup(func() { conn.Close() })
		return recoverypb.NewCheckServiceClient(conn)
	}
	_, err := dial(core.Org).Check(ctx, &recoverypb.CheckRequest{Recovery: "r"})
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "the recovery tool only: %v", err)
	cs := dial("ops/recover")
	checked, err := cs.Check(ctx, &recoverypb.CheckRequest{Recovery: "r"})
	require.NoError(t, err)
	assert.NotEmpty(t, checked.GetResults())
	for typ, id := range map[string]string{core.Employees: fx.ivo, core.Links: fx.ivoLink} {
		var version string
		require.NoError(t, fx.admin.QueryRow(ctx, "SELECT target->>'version' FROM "+itSchema+".be_audit_log WHERE target->>'type' = $1 "+
			"AND target->>'id' = $2 ORDER BY ord DESC LIMIT 1", typ, id).Scan(&version), "the audit names the version of %s", typ)
		v, err := strconv.ParseInt(version, 10, 64)
		require.NoError(t, err)
		present, err := cs.Present(ctx, &recoverypb.PresentRequest{Recovery: "r", Objects: []*recoverypb.ObjectRef{
			{Type: typ, Id: id, Version: version}, {Type: typ, Id: id, Version: strconv.FormatInt(v+1, 10)},
			{Type: typ, Id: ids.New().String(), Version: "1"}}})
		require.NoError(t, err)
		var answers []string
		for _, a := range present.GetAnswers() {
			answers = append(answers, a.GetAnswer())
		}
		assert.Equal(t, []string{"present", "older", "absent"}, answers, typ)
	}
}
