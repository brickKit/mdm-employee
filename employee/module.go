package employee

import (
	"context"
	"time"

	appointments "github.com/brickKit/mdm-employee/v3/internal/appointments"
	core "github.com/brickKit/mdm-employee/v3/internal/core"
	employees "github.com/brickKit/mdm-employee/v3/internal/employees"
	links "github.com/brickKit/mdm-employee/v3/internal/links"
	orgview "github.com/brickKit/mdm-employee/v3/internal/orgview"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/runtime"
	"github.com/brickKit/be-sdk-go/v3/work"
	authzv3 "github.com/brickKit/contract-infra-authz/v3/gen/go/be/authz/v3"
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
	b.Terminated = appointments.Ended(b)
	b.AdminWarning = links.AdminWarning(b)
	// the authorization member's system surface: a link's confirmation is checked there (security 08)
	conn, ok := rt.GRPCAt(core.Authz, rt.Config.String("AUTHZ_GRPC_URL"))
	if !ok {
		return nil, errs.New(errs.ReasonRequestInvalid, "AUTHZ_GRPC_URL cannot be dialled: account links are checked there")
	}
	b.Authz = authzv3.NewAccountServiceClient(conn)
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
	j.Add(work.Job{Name: employees.JobEffective, Kind: work.Singleton, Effect: work.Local, Interval: time.Minute,
		MaxAttempts: work.Unlimited, Handle: m.job(employees.Effective)})
	j.Add(work.Job{Name: appointments.JobEffective, Kind: work.Singleton, Effect: work.Local, Interval: time.Minute,
		MaxAttempts: work.Unlimited, Handle: m.job(appointments.Effective)})
	j.Add(work.Job{Name: core.JobEndOnRetirement, Kind: work.Queue, Effect: work.Local, MaxAttempts: work.Unlimited,
		Handle: m.job(appointments.EndOnRetirement)})
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
	h := m.handler
	add("GET", "/api/employee/employees", core.OpEmployeeRead, h(employees.ListEmployees))
	add("POST", "/api/employee/employees", core.OpEmployeeEdit, h(employees.Hire))
	add("GET", "/api/employee/employees/:id", core.OpEmployeeRead, h(employees.GetEmployee))
	add("PATCH", "/api/employee/employees/:id", core.OpEmployeeEdit, h(employees.UpdateEmployee))
	add("POST", "/api/employee/employees/:id/terminate", core.OpEmployeeEdit, h(employees.Terminate))
	add("POST", "/api/employee/employees/:id/withdraw-termination", core.OpEmployeeEdit, h(employees.WithdrawTermination))
	add("POST", "/api/employee/employees/:id/cancel-hire", core.OpEmployeeEdit, h(employees.CancelHire))
	add("GET", "/api/employee/employees/:id/appointments", core.OpApptRead, h(appointments.ListAppointments))
	add("POST", "/api/employee/employees/:id/appointments", core.OpApptEdit, h(appointments.Appoint))
	add("POST", "/api/employee/employees/:id/transfer", core.OpApptEdit, h(appointments.Transfer))
	add("GET", "/api/employee/appointments/:id", core.OpApptRead, h(appointments.GetAppointment))
	add("PATCH", "/api/employee/appointments/:id", core.OpApptEdit, h(appointments.UpdateAppointment))
	add("POST", "/api/employee/appointments/:id/end", core.OpApptEdit, h(appointments.EndAppointment))
	add("GET", "/api/employee/positions/:id/holders", core.OpApptRead, h(appointments.Holders))
	add("GET", "/api/employee/employees/:id/accounts", core.OpLinkRead, h(links.ListLinks))
	add("POST", "/api/employee/employees/:id/accounts", core.OpLinkEdit, h(links.LinkAccount))
	add("POST", "/api/employee/account-links/:id/end", core.OpLinkEdit, h(links.EndLink))
	add("GET", "/api/employee/accounts/:account/employee", core.OpLinkRead, h(links.EmployeeOfAccount))
	for _, u := range []struct{ method, path, op string }{
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

func (m *Module) job(f func(*core.Base, context.Context, *work.Run) error) func(context.Context, *work.Run) error {
	return func(ctx context.Context, r *work.Run) error { return f(m.b, ctx, r) }
}
