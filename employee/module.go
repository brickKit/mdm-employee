package employee

import (
	"context"
	"time"

	core "github.com/brickKit/mdm-employee/v3/internal/core"
	orgview "github.com/brickKit/mdm-employee/v3/internal/orgview"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/runtime"
	"github.com/brickKit/be-sdk-go/v3/work"
)

// Module is the employee component as the launcher and the shells run it: it only assembles the business modules
// under internal/ (their map is internal/AGENTS.md).
type Module struct {
	b   *core.Base
	org *orgview.View
}

// New builds the module and this member's view of mdm/org.
func New(rt *runtime.Runtime) (*Module, error) {
	b := &core.Base{RT: rt, Ev: rt.Evidence}
	v := orgview.New(b)
	b.Org = v
	return &Module{b: b, org: v}, nil
}

// Subscriptions declares the projection's input: every event of mdm/org's legal entities, units and positions.
func (m *Module) Subscriptions(s *events.Subscriptions) error {
	s.Add(events.Sub{Name: "org-events", Streams: []string{core.OrgStream}, Types: orgview.EventTypes,
		Handle: func(ctx context.Context, tx db.Tx, e events.Received) error { return m.org.OnEvent(ctx, tx, e) }})
	return nil
}

// Jobs declares the background work (protocol 01).
func (m *Module) Jobs(j *work.Jobs) error {
	j.Add(work.Job{Name: orgview.JobAlign, Kind: work.Singleton, Effect: work.Local, Interval: time.Hour,
		MaxAttempts: work.Unlimited, Handle: m.org.Align})
	j.Add(work.Job{Name: orgview.JobRepair, Kind: work.Queue, Effect: work.IdempotentExternal,
		MaxAttempts: work.Unlimited, Handle: m.org.Repair})
	j.Add(work.Job{Name: orgview.JobRealign, Kind: work.Queue, Effect: work.IdempotentExternal,
		MaxAttempts: work.Unlimited, Handle: m.org.Realign})
	return nil
}

// Routes registers the user surface (contract/openapi.yaml).
func (m *Module) Routes(r *httpx.Routes) error {
	add := func(method, path, op string, h httpx.Handler) {
		r.Add(httpx.Route{Method: method, Path: path, Operation: op, Handle: h})
	}
	for _, u := range []struct{ method, path, op string }{
		{"GET", "/api/employee/employees", core.OpEmployeeRead},
		{"POST", "/api/employee/employees", core.OpEmployeeEdit},
		{"GET", "/api/employee/employees/:id", core.OpEmployeeRead},
		{"PATCH", "/api/employee/employees/:id", core.OpEmployeeEdit},
		{"POST", "/api/employee/employees/:id/terminate", core.OpEmployeeEdit},
		{"POST", "/api/employee/employees/:id/withdraw-termination", core.OpEmployeeEdit},
		{"POST", "/api/employee/employees/:id/cancel-hire", core.OpEmployeeEdit},
		{"GET", "/api/employee/employees/:id/appointments", core.OpApptRead},
		{"POST", "/api/employee/employees/:id/appointments", core.OpApptEdit},
		{"POST", "/api/employee/employees/:id/transfer", core.OpApptEdit},
		{"GET", "/api/employee/appointments/:id", core.OpApptRead},
		{"PATCH", "/api/employee/appointments/:id", core.OpApptEdit},
		{"POST", "/api/employee/appointments/:id/end", core.OpApptEdit},
		{"GET", "/api/employee/positions/:id/holders", core.OpApptRead},
		{"GET", "/api/employee/employees/:id/accounts", core.OpLinkRead},
		{"POST", "/api/employee/employees/:id/accounts", core.OpLinkEdit},
		{"POST", "/api/employee/account-links/:id/end", core.OpLinkEdit},
		{"GET", "/api/employee/accounts/:account/employee", core.OpLinkRead},
		// in the contract, not in this version (D75)
		{"POST", "/api/employee/employees/:id/payee", core.OpEmployeeEdit},
		{"POST", "/api/employee/imports", core.OpEmployeeEdit},
		{"POST", "/api/employee/me/change-requests", core.OpEmployeeRead},
	} {
		add(u.method, u.path, u.op, core.Unsupported)
	}
	return nil
}

// handler adapts a module's handler, which takes the shared base first, to what the SDK registers.
func (m *Module) handler(f func(*core.Base, *httpx.Context) error) httpx.Handler {
	return func(c *httpx.Context) error { return f(m.b, c) }
}
