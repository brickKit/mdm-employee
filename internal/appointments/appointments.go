// Package appointments is the appointments (design.md, security 04): an employee on a position of mdm/org for a
// period of the legal entity's business dates. An appointment is a qualification instance that is never reused: the
// legal entity, the unit and the position never change in place (a transfer ends the old one and makes a new one);
// only the note does. One person never holds one position twice at a time. An appointment ends by hand, by a
// transfer, by its last day passing (the effective-dates job), by a termination (Ended, wired as core.Base.Terminated)
// or by its position or unit retiring (EndOnRetirement, the job the mdm/org projection queues).
package appointments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	core "github.com/brickKit/mdm-employee/v3/internal/core"
	tightening "github.com/brickKit/mdm-employee/v3/internal/tightening"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/bizdate"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/evidence"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/list"
	"github.com/brickKit/be-sdk-go/v3/work"
)

// JobEffective begins the planned appointments whose first day came and ends the ones whose last day passed.
const JobEffective = "appointment-effective"

const jobActor = "job:" + JobEffective

func facts(legalEntity, unitPath string) sdkauthz.Facts {
	return sdkauthz.Facts{core.DimLegalEntity: sdkauthz.Value(legalEntity), core.DimDepartment: sdkauthz.Value(unitPath)}
}

// admit checks the caller for an operation on an appointment (its legal entity and its unit's path).
func admit(c *httpx.Context, op, legalEntity, unitPath string) (context.Context, string, error) {
	s, _ := sdkauthz.From(c.Ctx())
	ctx, _, err := s.Check(c.Ctx(), op, facts(legalEntity, unitPath))
	if err != nil {
		return ctx, "", err
	}
	return ctx, core.Principal(ctx), nil
}

// today is the legal entity's business date (its time zone from the mdm/org projection).
func today(m *core.Base, ctx context.Context, legalEntity string) (string, error) {
	le, ok, err := m.Org.LegalEntity(ctx, legalEntity)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", core.ErrOrgUnknown.New("legal entity " + legalEntity + " is not known")
	}
	return bizdate.Today(le.Timezone)
}

func dayBefore(d string) string {
	t, _ := time.Parse(time.DateOnly, d)
	return t.AddDate(0, 0, -1).Format(time.DateOnly)
}

// target is a position to appoint to, checked against the mdm/org projection: active, in an active unit of the
// employee's legal entity.
type target struct {
	position core.OrgPosition
	unit     core.OrgUnit
}

func targetOf(m *core.Base, ctx context.Context, positionID, legalEntity string) (target, error) {
	if !core.UUIDForm.MatchString(positionID) {
		return target{}, core.Invalid("position_id", "position_id is a position's ID")
	}
	p, ok, err := m.Org.Position(ctx, positionID)
	if err != nil {
		return target{}, err
	}
	if !ok || p.State != "active" {
		return target{}, core.ErrOrgUnknown.New("position " + positionID + " is not known here, or not active")
	}
	if p.LegalEntityID != legalEntity {
		return target{}, core.Invalid("position_id", "the position belongs to another legal entity than the employee's")
	}
	u, ok, err := m.Org.Unit(ctx, p.UnitID)
	if err != nil {
		return target{}, err
	}
	if !ok || u.State != "active" {
		return target{}, core.ErrOrgUnknown.New("the position's unit " + p.UnitID + " is not known here, or not active")
	}
	return target{position: p, unit: u}, nil
}

// insert makes a new appointment after checking the employee (locked, not terminated) and the overlap.
func insert(m *core.Base, ctx context.Context, tx db.Tx, actor string, emp core.Employee, t target, start string,
	end *string, note, today string) (core.Appointment, error) {
	if emp.State == "terminated" {
		return core.Appointment{}, core.ErrState.New("the employee is terminated: no new appointments")
	}
	var overlap bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM appointment WHERE employee_id = $1 AND position_id = $2
		AND state <> 'ended' AND start_date <= COALESCE($4::date, 'infinity') AND $3::date <= COALESCE(end_date, 'infinity'))`,
		emp.ID, t.position.ID, start, end).Scan(&overlap); err != nil {
		return core.Appointment{}, err
	}
	if overlap {
		return core.Appointment{}, core.ErrOverlap.New("the employee holds the position for part of that period")
	}
	state := "active"
	if start > today {
		state = "planned"
	}
	id := ids.New().String()
	if _, err := tx.Exec(ctx, `INSERT INTO appointment (id, employee_id, legal_entity_id, unit_id, unit_path, position_id,
		start_date, end_date, state, note) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, id, emp.ID, emp.LegalEntityID,
		t.unit.ID, t.unit.Path, t.position.ID, start, end, state, note); err != nil {
		return core.Appointment{}, err
	}
	a, err := core.GetAppointment(ctx, tx, id, false)
	if err != nil {
		return a, err
	}
	return a, Published(m, ctx, tx, actor, a)
}

// Published writes the appointment's event and its audit record, in the transaction that changed it.
func Published(m *core.Base, ctx context.Context, tx db.Tx, actor string, a core.Appointment) error {
	if err := core.Emit(m, ctx, tx, core.AppointmentChanged, a.ID, a.Version, a, core.EmployeeSubject(a.EmployeeID)); err != nil {
		return err
	}
	return core.Change(m, ctx, tx, actor, core.OpApptEdit, core.Appointments, a.ID, a)
}

// employeeOf reads the employee of a request (its legal entity, for the checks before the transaction).
func employeeOf(m *core.Base, ctx context.Context, id string) (core.Employee, error) {
	if !core.UUIDForm.MatchString(id) {
		return core.Employee{}, core.NotFound("employee")
	}
	var e core.Employee
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		e, err = core.GetEmployee(ctx, tx, id, false)
		return err
	})
	if errors.Is(err, core.ErrNone) {
		return e, core.NotFound("employee")
	}
	return e, err
}

type appointIn struct {
	PositionID string  `json:"position_id"`
	StartDate  string  `json:"start_date"`
	EndDate    *string `json:"end_date"`
	Note       string  `json:"note"`
}

// Appoint makes an appointment on a position (today or before: active; later: planned).
func Appoint(m *core.Base, c *httpx.Context) error {
	var in appointIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if !core.DateForm.MatchString(in.StartDate) {
		return core.Invalid("start_date", "a date, YYYY-MM-DD")
	}
	if in.EndDate != nil && (!core.DateForm.MatchString(*in.EndDate) || *in.EndDate < in.StartDate) {
		return core.Invalid("end_date", "a date, YYYY-MM-DD, not before start_date")
	}
	emp, err := employeeOf(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	t, err := targetOf(m, c.Ctx(), in.PositionID, emp.LegalEntityID)
	if err != nil {
		return err
	}
	ctx, actor, err := admit(c, core.OpApptEdit, emp.LegalEntityID, t.unit.Path)
	if err != nil {
		return err
	}
	now, err := today(m, ctx, emp.LegalEntityID)
	if err != nil {
		return err
	}
	var out core.Appointment
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		locked, err := core.GetEmployee(ctx, tx, emp.ID, true) // one change of an employee's appointments at a time
		if err != nil {
			return err
		}
		out, err = insert(m, ctx, tx, actor, locked, t, in.StartDate, in.EndDate, in.Note, now)
		return err
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

// end ends an open appointment as of a last day, for a reason, in a transaction that locked it.
func end(m *core.Base, ctx context.Context, tx db.Tx, actor string, a core.Appointment, last, reason string) (core.Appointment, error) {
	if last < dayBefore(a.StartDate) {
		last = dayBefore(a.StartDate) // a planned one that never began
	}
	if _, err := tx.Exec(ctx, `UPDATE appointment SET state = 'ended', end_date = $2, end_reason = $3, version = version + 1,
		updated_at = now() WHERE id = $1`, a.ID, last, reason); err != nil {
		return a, err
	}
	out, err := core.GetAppointment(ctx, tx, a.ID, false)
	if err != nil {
		return out, err
	}
	return out, Published(m, ctx, tx, actor, out)
}

// ending is the evidence of one appointment's end (a tightening): its intent written before the transaction, from
// the appointment read without a lock; conclude in the transaction; settle after it.
type ending struct {
	t         *tightening.Tightening[core.Appointment]
	before    core.Appointment
	p         *evidence.Pending
	concluded bool
}

func intent(m *core.Base, ctx context.Context, before core.Appointment, reason, last string) *ending {
	return intentOf(m, ctx, before, tightening.AppointmentEnd(reason, last))
}

func intentOf(m *core.Base, ctx context.Context, before core.Appointment, t *tightening.Tightening[core.Appointment]) *ending {
	return &ending{t: t, before: before, p: tightening.Intent(ctx, m.Ev, t, before.ID, before.Version, before)}
}

func (e *ending) conclude(ctx context.Context, tx db.Tx, after core.Appointment) error {
	var err error
	e.concluded, err = tightening.Conclude(ctx, tx, e.p, e.t, e.before, after)
	return err
}

// settle cancels the intent unless the end was concluded and its transaction committed (err nil).
func (e *ending) settle(ctx context.Context, err error) {
	tightening.Settle(ctx, e.p, e.concluded && err == nil)
}

// appointmentOf reads an appointment and checks the caller for op on it.
func appointmentOf(m *core.Base, c *httpx.Context, op string) (context.Context, string, core.Appointment, error) {
	id := c.Param("id")
	if !core.UUIDForm.MatchString(id) {
		return nil, "", core.Appointment{}, core.NotFound("appointment")
	}
	var a core.Appointment
	var path string
	err := m.RT.DB.Tx(c.Ctx(), func(ctx context.Context, tx db.Tx) error {
		var err error
		if a, err = core.GetAppointment(ctx, tx, id, false); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT unit_path FROM appointment WHERE id = $1`, id).Scan(&path)
	})
	if errors.Is(err, core.ErrNone) {
		return nil, "", a, core.NotFound("appointment")
	}
	if err != nil {
		return nil, "", a, err
	}
	ctx, actor, err := admit(c, op, a.LegalEntityID, path)
	return ctx, actor, a, err
}

func GetAppointment(m *core.Base, c *httpx.Context) error {
	_, _, a, err := appointmentOf(m, c, core.OpApptRead)
	if err != nil {
		return err
	}
	return c.JSON(200, a)
}

// change locks the appointment and applies a change to it (in the caller's admitted context).
func change(m *core.Base, ctx context.Context, id string, version *int64, apply func(tx db.Tx, a core.Appointment) (core.Appointment, error)) (core.Appointment, error) {
	var out core.Appointment
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		cur, err := core.GetAppointment(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if version != nil && *version != cur.Version {
			return core.ErrVersion.New(fmt.Sprintf("the appointment is at version %d", cur.Version))
		}
		out, err = apply(tx, cur)
		return err
	})
	return out, err
}

// UpdateAppointment changes the note, the only field changed in place.
func UpdateAppointment(m *core.Base, c *httpx.Context) error {
	var in struct {
		Version *int64  `json:"version"`
		Note    *string `json:"note"`
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	ctx, actor, a, err := appointmentOf(m, c, core.OpApptEdit)
	if err != nil {
		return err
	}
	out, err := change(m, ctx, a.ID, in.Version, func(tx db.Tx, cur core.Appointment) (core.Appointment, error) {
		if in.Note == nil || *in.Note == cur.Note {
			return cur, nil
		}
		if _, err := tx.Exec(ctx, `UPDATE appointment SET note = $2, version = version + 1, updated_at = now() WHERE id = $1`,
			cur.ID, *in.Note); err != nil {
			return cur, err
		}
		next, err := core.GetAppointment(ctx, tx, cur.ID, false)
		if err != nil {
			return next, err
		}
		return next, Published(m, ctx, tx, actor, next)
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

// EndAppointment ends an appointment by hand: its last day today or before ends it now; a later one is recorded and
// the job ends it then (expired).
func EndAppointment(m *core.Base, c *httpx.Context) error {
	var in struct {
		Version *int64 `json:"version"`
		EndDate string `json:"end_date"`
	}
	if c.Request().ContentLength > 0 {
		if err := c.BindJSON(&in); err != nil {
			return err
		}
	}
	if in.EndDate != "" && !core.DateForm.MatchString(in.EndDate) {
		return core.Invalid("end_date", "a date, YYYY-MM-DD")
	}
	ctx, actor, a, err := appointmentOf(m, c, core.OpApptEdit)
	if err != nil {
		return err
	}
	now, err := today(m, ctx, a.LegalEntityID)
	if err != nil {
		return err
	}
	last := in.EndDate
	if last == "" {
		last = now
	}
	var ev *ending
	if last <= now { // ends now
		ev = intent(m, ctx, a, "ended", last)
	} else { // a later last day: recorded now (a restore must not forget it)
		ev = intentOf(m, ctx, a, tightening.ScheduledEnd(last))
	}
	out, err := change(m, ctx, a.ID, in.Version, func(tx db.Tx, cur core.Appointment) (core.Appointment, error) {
		if cur.State == "ended" {
			return cur, core.ErrState.New("the appointment has ended")
		}
		if last <= now {
			after, err := end(m, ctx, tx, actor, cur, last, "ended")
			if err != nil {
				return after, err
			}
			return after, ev.conclude(ctx, tx, after)
		}
		if _, err := tx.Exec(ctx, `UPDATE appointment SET end_date = $2, version = version + 1, updated_at = now()
			WHERE id = $1`, cur.ID, last); err != nil {
			return cur, err
		}
		next, err := core.GetAppointment(ctx, tx, cur.ID, false)
		if err != nil {
			return next, err
		}
		if err := ev.conclude(ctx, tx, next); err != nil {
			return next, err
		}
		return next, Published(m, ctx, tx, actor, next)
	})
	ev.settle(ctx, err)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

// Transfer ends one appointment (transfer, the day before) and makes the new one, in one change. The date is today or
// before: a later transfer is a last day on the old appointment and a planned new one.
func Transfer(m *core.Base, c *httpx.Context) error {
	var in struct {
		FromAppointmentID string `json:"from_appointment_id"`
		PositionID        string `json:"position_id"`
		Date              string `json:"date"`
		Note              string `json:"note"`
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if !core.DateForm.MatchString(in.Date) {
		return core.Invalid("date", "a date, YYYY-MM-DD")
	}
	if !core.UUIDForm.MatchString(in.FromAppointmentID) {
		return core.Invalid("from_appointment_id", "an appointment's ID")
	}
	emp, err := employeeOf(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	t, err := targetOf(m, c.Ctx(), in.PositionID, emp.LegalEntityID)
	if err != nil {
		return err
	}
	// a transfer changes both appointments: the old one's department and the new one's are both checked (an admin of
	// the new department alone would end an appointment outside their scope)
	fromPath, err := pathOf(m, c.Ctx(), in.FromAppointmentID, emp.ID)
	if err != nil {
		return err
	}
	if _, _, err := admit(c, core.OpApptEdit, emp.LegalEntityID, fromPath); err != nil {
		return err
	}
	ctx, actor, err := admit(c, core.OpApptEdit, emp.LegalEntityID, t.unit.Path)
	if err != nil {
		return err
	}
	now, err := today(m, ctx, emp.LegalEntityID)
	if err != nil {
		return err
	}
	if in.Date > now {
		return core.Invalid("date", "a transfer is today or before: a later one is an end date on the appointment and a new planned one")
	}
	var ev *ending
	if pre, err := read(m, ctx, in.FromAppointmentID); err == nil && pre.EmployeeID == emp.ID {
		ev = intent(m, ctx, pre, "transfer", dayBefore(in.Date))
	}
	var ended, made core.Appointment
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		locked, err := core.GetEmployee(ctx, tx, emp.ID, true)
		if err != nil {
			return err
		}
		from, err := core.GetAppointment(ctx, tx, in.FromAppointmentID, true)
		if errors.Is(err, core.ErrNone) || err == nil && from.EmployeeID != emp.ID {
			return core.Invalid("from_appointment_id", "not an appointment of this employee")
		}
		if err != nil {
			return err
		}
		if from.State == "ended" {
			return core.ErrState.New("the appointment to transfer from has ended")
		}
		var path string
		if err := tx.QueryRow(ctx, `SELECT unit_path FROM appointment WHERE id = $1`, from.ID).Scan(&path); err != nil {
			return err
		}
		if path != fromPath { // its unit moved since it was checked: ask again
			return core.ErrVersion.New("the appointment's department moved since the request was checked")
		}
		if ended, err = end(m, ctx, tx, actor, from, dayBefore(in.Date), "transfer"); err != nil {
			return err
		}
		if ev != nil {
			if err := ev.conclude(ctx, tx, ended); err != nil {
				return err
			}
		}
		made, err = insert(m, ctx, tx, actor, locked, t, in.Date, nil, in.Note, now)
		return err
	})
	if ev != nil {
		ev.settle(ctx, err)
	}
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"ended": ended, "appointment": made})
}

var appointmentList = list.Spec{Operation: core.OpApptRead, Alias: "a", IDColumn: "id", IDKind: list.UUID,
	Columns: []list.Column{
		{Field: "start_date", Column: "start_date", Kind: list.Date, Sortable: true},
		{Field: "employee_id", Column: "employee_id", Kind: list.UUID, Filterable: true},
		{Field: "position_id", Column: "position_id", Kind: list.UUID, Filterable: true},
		{Field: "state", Column: "state", Kind: list.Text, Filterable: true}}}

func page(m *core.Base, c *httpx.Context, filters []list.Filter) ([]core.Appointment, error) {
	ctx, q, err := core.Build(c, appointmentList, list.Request{Filters: filters, Sort: []list.Order{{Field: "start_date"}}})
	if err != nil {
		return nil, err
	}
	out, _, err := list.Page(ctx, m.RT.DB, q, "appointment", core.AppointmentCols, core.ScanAppointment,
		func(a core.Appointment) ([]string, string) { return []string{a.StartDate}, a.ID })
	return out, err
}

// ListAppointments lists an employee's appointments the caller may see, the open ones first, then by start date.
func ListAppointments(m *core.Base, c *httpx.Context) error {
	if !core.UUIDForm.MatchString(c.Param("id")) {
		return core.NotFound("employee")
	}
	filters := []list.Filter{{Field: "employee_id", Op: "eq", Values: []string{c.Param("id")}}}
	if s := c.Query("state"); s != "" {
		filters = append(filters, list.Filter{Field: "state", Op: "eq", Values: []string{s}})
	}
	out, err := page(m, c, filters)
	if err != nil {
		return err
	}
	open := make([]core.Appointment, 0, len(out))
	var closed []core.Appointment
	for _, a := range out {
		if a.State == "ended" {
			closed = append(closed, a)
		} else {
			open = append(open, a)
		}
	}
	return c.JSON(200, map[string]any{"appointments": append(open, closed...)})
}

// Holders lists the active appointments on a position the caller may see, each with its employee.
func Holders(m *core.Base, c *httpx.Context) error {
	if !core.UUIDForm.MatchString(c.Param("id")) {
		return core.NotFound("position")
	}
	out, err := page(m, c, []list.Filter{{Field: "position_id", Op: "eq", Values: []string{c.Param("id")}},
		{Field: "state", Op: "eq", Values: []string{"active"}}})
	if err != nil {
		return err
	}
	holders := make([]map[string]any, 0, len(out))
	err = m.RT.DB.Tx(c.Ctx(), func(ctx context.Context, tx db.Tx) error {
		for _, a := range out {
			e, err := core.GetEmployee(ctx, tx, a.EmployeeID, false)
			if err != nil {
				return err
			}
			holders = append(holders, map[string]any{"appointment": a, "employee": e})
		}
		return nil
	})
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"holders": holders})
}

// Ended ends every open appointment of an employee terminated as of a date (core.Base.Terminated), in the
// termination's transaction.
func Ended(m *core.Base) func(ctx context.Context, tx db.Tx, actor, employee, date string) error {
	return func(ctx context.Context, tx db.Tx, actor, employee, date string) error {
		ids, err := openIDs(ctx, tx, `employee_id = $1`, employee)
		if err != nil {
			return err
		}
		for _, id := range ids {
			a, err := core.GetAppointment(ctx, tx, id, true)
			if err != nil {
				return err
			}
			if _, err := end(m, ctx, tx, actor, a, date, "termination"); err != nil {
				return err
			}
		}
		return nil
	}
}

func openIDs(ctx context.Context, tx db.Tx, where string, arg any) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT id::text FROM appointment WHERE state <> 'ended' AND `+where+` ORDER BY id`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// EndOnRetirement ends the open appointments on a retired position or unit (the mdm/org projection queues it): their
// last day is today in their legal entity.
func EndOnRetirement(m *core.Base, ctx context.Context, r *work.Run) error {
	var ret core.Retirement
	if err := json.Unmarshal(r.Args, &ret); err != nil {
		return err
	}
	reason, col := "position_retired", "position_id"
	if ret.Kind == "unit" {
		reason, col = "unit_retired", "unit_id"
	}
	var open []string
	if err := m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		open, err = openIDs(ctx, tx, col+` = $1`, ret.ID)
		return err
	}); err != nil {
		return err
	}
	for _, id := range open {
		now, err := todayOf(m, ctx, id) // outside the transaction: it may ask mdm/org
		if err != nil {
			return err
		}
		pre, err := read(m, ctx, id)
		if err != nil {
			return err
		}
		ev := intent(m, ctx, pre, reason, now)
		err = m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
			a, err := core.GetAppointment(ctx, tx, id, true)
			if err != nil || a.State == "ended" {
				return err
			}
			after, err := end(m, ctx, tx, "job:"+core.JobEndOnRetirement, a, now, reason)
			if err != nil {
				return err
			}
			return ev.conclude(ctx, tx, after)
		})
		ev.settle(ctx, err)
		if err != nil {
			return err
		}
	}
	return nil
}

// todayOf is the business date of an appointment's legal entity, read outside any transaction (the legal entity's
// time zone may have to come from mdm/org).
func todayOf(m *core.Base, ctx context.Context, id string) (string, error) {
	var le string
	if err := m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
		return tx.QueryRow(ctx, `SELECT legal_entity_id::text FROM appointment WHERE id = $1`, id).Scan(&le)
	}); err != nil {
		return "", err
	}
	return today(m, ctx, le)
}

// Effective begins the planned appointments whose first day came and ends the open ones whose last day passed
// (expired), legal entity by legal entity.
func Effective(m *core.Base, ctx context.Context, _ *work.Run) error {
	var due []string
	if err := m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		due, err = openIDs(ctx, tx, `(state = 'planned' OR end_date IS NOT NULL) AND $1`, true)
		return err
	}); err != nil {
		return err
	}
	var all []error
	for _, id := range due {
		now, err := todayOf(m, ctx, id) // outside the transaction: it may ask mdm/org
		if err != nil {
			all = append(all, err)
			continue
		}
		var ev *ending
		if pre, err := read(m, ctx, id); err == nil && pre.State != "ended" && pre.EndDate != nil && *pre.EndDate < now {
			ev = intent(m, ctx, pre, "expired", *pre.EndDate)
		}
		err = m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
			a, err := core.GetAppointment(ctx, tx, id, true)
			if err != nil || a.State == "ended" {
				return err
			}
			switch {
			case a.EndDate != nil && *a.EndDate < now:
				after, err := end(m, ctx, tx, jobActor, a, *a.EndDate, "expired")
				if err != nil || ev == nil {
					return err
				}
				return ev.conclude(ctx, tx, after)
			case a.State == "planned" && a.StartDate <= now:
				if _, err := tx.Exec(ctx, `UPDATE appointment SET state = 'active', version = version + 1, updated_at = now()
					WHERE id = $1`, a.ID); err != nil {
					return err
				}
				next, err := core.GetAppointment(ctx, tx, a.ID, false)
				if err != nil {
					return err
				}
				return Published(m, ctx, tx, jobActor, next)
			}
			return nil
		})
		if ev != nil {
			ev.settle(ctx, err)
		}
		all = append(all, err)
	}
	return errors.Join(all...)
}

// pathOf is the department path an employee's appointment was admitted under (its unit's path as copied); one that is
// not the employee's is invalid. Read before the transaction, checked again under the lock (a unit that moved since
// is a conflict: the caller asks again).
func pathOf(m *core.Base, ctx context.Context, id, employee string) (string, error) {
	var a core.Appointment
	var path string
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		if a, err = core.GetAppointment(ctx, tx, id, false); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT unit_path FROM appointment WHERE id = $1`, id).Scan(&path)
	})
	if errors.Is(err, core.ErrNone) || err == nil && a.EmployeeID != employee {
		return "", core.Invalid("from_appointment_id", "not an appointment of this employee")
	}
	return path, err
}

// read reads an appointment in a transaction of its own (the state an intent is written from).
func read(m *core.Base, ctx context.Context, id string) (core.Appointment, error) {
	var a core.Appointment
	err := m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		a, err = core.GetAppointment(ctx, tx, id, false)
		return err
	})
	return a, err
}

// ReplayEnd ends the appointment again as the evidence records it (its reason and last day), after a full recovery.
// Already ended: nothing to do. Not there: evidence.ErrDangling.
// ReplayScheduledEnd sets an appointment's later last day again, as the evidence records it, after a full recovery:
// the effective-dates job ends it when it comes. Ended, or an earlier last day set: nothing to do. Not there:
// evidence.ErrDangling.
func ReplayScheduledEnd(m *core.Base, ctx context.Context, tx db.Tx, actor, id, last string) error {
	a, err := core.GetAppointment(ctx, tx, id, true)
	if errors.Is(err, core.ErrNone) {
		return fmt.Errorf("appointment %s: %w", id, evidence.ErrDangling)
	}
	if err != nil || a.State == "ended" || a.EndDate != nil && *a.EndDate <= last {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE appointment SET end_date = $2, version = version + 1, updated_at = now()
		WHERE id = $1`, id, last); err != nil {
		return err
	}
	next, err := core.GetAppointment(ctx, tx, id, false)
	if err != nil {
		return err
	}
	return Published(m, ctx, tx, actor, next)
}

func ReplayEnd(m *core.Base, ctx context.Context, tx db.Tx, actor, id, reason, last string) error {
	a, err := core.GetAppointment(ctx, tx, id, true)
	if errors.Is(err, core.ErrNone) {
		return fmt.Errorf("appointment %s: %w", id, evidence.ErrDangling)
	}
	if err != nil || a.State == "ended" {
		return err
	}
	_, err = end(m, ctx, tx, actor, a, last, reason)
	return err
}
