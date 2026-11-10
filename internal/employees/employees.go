// Package employees is the employee's lifecycle (design.md's state table): hire (pre_hire when the hire date is after
// the legal entity's today, active otherwise), the names and the restricted personal data, a termination on a date
// (today or before: at once, ending what the termination ends beyond the employee through Base.Terminated; later: the
// date recorded, carried out by the effective-dates job, withdrawable until then), a hire cancelled before it began.
// Dates are the employing legal entity's business dates (its time zone from the mdm/org projection).
package employees

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	core "github.com/brickKit/mdm-employee/v3/internal/core"
	tightening "github.com/brickKit/mdm-employee/v3/internal/tightening"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/bizdate"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/evidence"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/list"
	"github.com/brickKit/be-sdk-go/v3/search"
	"github.com/brickKit/be-sdk-go/v3/work"
)

// JobEffective is the singleton that carries out the hires and the terminations whose date came (every minute).
const JobEffective = "employee-effective"

type employeeIn struct {
	Version       *int64             `json:"version"`
	LegalEntityID string             `json:"legal_entity_id"`
	EmployeeNo    string             `json:"employee_no"`
	Name          *string            `json:"name"`
	Names         *map[string]string `json:"names"`
	HireDate      string             `json:"hire_date"`
	Personal      *core.Personal     `json:"personal"`
}

func checkPersonal(p *core.Personal) error {
	if p == nil {
		return nil
	}
	if p.BirthDate != nil && !core.DateForm.MatchString(*p.BirthDate) {
		return core.Invalid("personal.birth_date", "a date, YYYY-MM-DD")
	}
	if p.NationalID != nil && strings.TrimSpace(*p.NationalID) == "" {
		return core.Invalid("personal.national_id", "empty: give null to clear it")
	}
	return nil
}

// Today is the legal entity's business date (its time zone from mdm/org); an unknown legal entity: ORG_NOT_KNOWN.
func Today(m *core.Base, ctx context.Context, legalEntity string) (string, error) {
	le, ok, err := m.Org.LegalEntity(ctx, legalEntity)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", core.ErrOrgUnknown.New("legal entity " + legalEntity + " is not known")
	}
	return bizdate.Today(le.Timezone)
}

func facts(legalEntity string) sdkauthz.Facts {
	return sdkauthz.Facts{core.DimLegalEntity: sdkauthz.Value(legalEntity)}
}

// admit checks the caller for an operation on a record of a legal entity.
func admit(c *httpx.Context, op, legalEntity string) (context.Context, string, sdkauthz.Decision, error) {
	s, _ := sdkauthz.From(c.Ctx())
	ctx, d, err := s.Check(c.Ctx(), op, facts(legalEntity))
	if err != nil {
		return ctx, "", d, err
	}
	return ctx, core.Principal(ctx), d, nil
}

// Hire takes a new employee; the same hire again (number, name and date) answers it.
func Hire(m *core.Base, c *httpx.Context) error {
	var in employeeIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	switch {
	case !core.UUIDForm.MatchString(in.LegalEntityID):
		return core.Invalid("legal_entity_id", "legal_entity_id is a legal entity's ID")
	case !core.NumberForm.MatchString(in.EmployeeNo):
		return core.Invalid("employee_no", "employee_no is upper case letters, digits, _ and -, at most 32")
	case in.Name == nil || strings.TrimSpace(*in.Name) == "":
		return core.Invalid("name", "name is required")
	case !core.DateForm.MatchString(in.HireDate):
		return core.Invalid("hire_date", "a date, YYYY-MM-DD")
	}
	names := map[string]string{}
	if in.Names != nil {
		if err := core.CheckNames(*in.Names); err != nil {
			return err
		}
		names = *in.Names
	}
	if err := checkPersonal(in.Personal); err != nil {
		return err
	}
	ctx, actor, _, err := admit(c, core.OpEmployeeEdit, in.LegalEntityID)
	if err != nil {
		return err
	}
	today, err := Today(m, ctx, in.LegalEntityID)
	if err != nil {
		return err
	}
	state := "active"
	if in.HireDate > today {
		state = "pre_hire"
	}
	personal := in.Personal
	if personal == nil {
		personal = &core.Personal{}
	}
	var out core.Employee
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var existing string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM employee WHERE legal_entity_id = $1 AND employee_no = $2`,
			in.LegalEntityID, in.EmployeeNo).Scan(&existing); err == nil {
			cur, err := core.GetEmployee(ctx, tx, existing, false)
			if err != nil {
				return err
			}
			if cur.Name == *in.Name && cur.HireDate == in.HireDate {
				out = cur // the same hire again answers it
				return nil
			}
			return core.ErrNumber.New("employee number " + in.EmployeeNo + " belongs to another employee")
		}
		id := ids.New().String()
		nb, _ := json.Marshal(names)
		if _, err := tx.Exec(ctx, `INSERT INTO employee (id, legal_entity_id, employee_no, name, names, state, hire_date,
			birth_date, national_id, search, search_version) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, id,
			in.LegalEntityID, in.EmployeeNo, *in.Name, nb, state, in.HireDate, personal.BirthDate, personal.NationalID,
			core.SearchOf(in.EmployeeNo, *in.Name, names), search.Version); err != nil {
			return err
		}
		var err error
		if out, err = core.GetEmployee(ctx, tx, id, false); err != nil {
			return err
		}
		return Published(m, ctx, tx, actor, core.OpEmployeeEdit, out)
	})
	if err != nil {
		return err
	}
	return read(m, c, ctx, out.ID)
}

// Published writes the employee's event and its audit record, in the transaction that changed it.
func Published(m *core.Base, ctx context.Context, tx db.Tx, actor, op string, e core.Employee) error {
	if err := core.Emit(m, ctx, tx, core.EmployeeChanged, e.ID, e.Version, e, core.EmployeeSubject(e.ID)); err != nil {
		return err
	}
	return core.Change(m, ctx, tx, actor, op, core.Employees, e.ID, e)
}

// read answers an employee to the caller: the personal data only when the caller's grant reads its field (P8.31).
func read(m *core.Base, c *httpx.Context, ctx context.Context, id string) error {
	out, extra, err := withPersonal(m, ctx, id)
	if err != nil {
		return err
	}
	return answer(c, out, extra)
}

func answer(c *httpx.Context, e map[string]any, extra map[string]any) error {
	s, _ := sdkauthz.From(c.Ctx())
	_, d, err := s.Check(c.Ctx(), core.OpEmployeeRead, facts(e["legal_entity_id"].(string)))
	if err != nil {
		return err
	}
	shaped := sdkauthz.Mask(core.ResourceOf(core.Employees), e, d.Fields)
	for k, v := range extra {
		shaped[k] = v
	}
	return c.JSON(200, shaped)
}

// withPersonal reads an employee with its restricted personal data, as a map the mask shapes.
func withPersonal(m *core.Base, ctx context.Context, id string) (map[string]any, map[string]any, error) {
	var e core.Employee
	var p core.Personal
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		if e, err = core.GetEmployee(ctx, tx, id, false); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT birth_date::text, national_id FROM employee WHERE id = $1`, id).Scan(&p.BirthDate,
			&p.NationalID)
	})
	if errors.Is(err, core.ErrNone) {
		return nil, nil, core.NotFound("employee")
	}
	if err != nil {
		return nil, nil, err
	}
	e.Personal = &p
	b, _ := json.Marshal(e)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out, nil, nil
}

// legalEntityOf is the employee's legal entity, for the authorization check before anything else is read.
func legalEntityOf(m *core.Base, ctx context.Context, id string) (string, error) {
	if !core.UUIDForm.MatchString(id) {
		return "", core.NotFound("employee")
	}
	var le string
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		return tx.QueryRow(ctx, `SELECT legal_entity_id::text FROM employee WHERE id = $1`, id).Scan(&le)
	})
	if err != nil {
		return "", core.NotFound("employee")
	}
	return le, nil
}

func GetEmployee(m *core.Base, c *httpx.Context) error {
	le, err := legalEntityOf(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	ctx, _, _, err := admit(c, core.OpEmployeeRead, le)
	if err != nil {
		return err
	}
	return read(m, c, ctx, c.Param("id"))
}

// change locks the employee, applies the change and writes, publishes and audits it if it changed; the answer is the
// employee read for the caller, with extra keys. A change that may tighten (tight, built with the legal entity's
// today) writes its intent before, from the employee read without a lock, and its conclusion in the transaction when
// it did tighten; otherwise the intent is cancelled.
func change(m *core.Base, c *httpx.Context, version *int64, tight func(today string) *tightening.Tightening[core.Employee],
	apply func(ctx context.Context, tx db.Tx, e *core.Employee, p *core.Personal, today string) error, extra map[string]any) error {
	id := c.Param("id")
	le, err := legalEntityOf(m, c.Ctx(), id)
	if err != nil {
		return err
	}
	ctx, actor, _, err := admit(c, core.OpEmployeeEdit, le)
	if err != nil {
		return err
	}
	today, err := Today(m, ctx, le)
	if err != nil {
		return err
	}
	var t *tightening.Tightening[core.Employee]
	var pending *evidence.Pending
	if tight != nil {
		t = tight(today)
		if pre, err := employee(m, ctx, id); err == nil {
			pending = tightening.Intent(ctx, m.Ev, t, id, pre.Version, pre)
		}
	}
	concluded := false
	defer func() { tightening.Settle(ctx, pending, concluded) }()
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		concluded = false
		cur, err := core.GetEmployee(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if version != nil && *version != cur.Version {
			return core.ErrVersion.New(fmt.Sprintf("the employee is at version %d", cur.Version))
		}
		var p core.Personal
		if err := tx.QueryRow(ctx, `SELECT birth_date::text, national_id FROM employee WHERE id = $1`, id).Scan(&p.BirthDate,
			&p.NationalID); err != nil {
			return err
		}
		next, np := cur, p
		if err := apply(ctx, tx, &next, &np, today); err != nil {
			return err
		}
		if same(cur, next) && same(p, np) {
			return nil
		}
		if err := write(ctx, tx, next, np); err != nil {
			return err
		}
		out, err := core.GetEmployee(ctx, tx, id, false)
		if err != nil {
			return err
		}
		if concluded, err = tightening.Conclude(ctx, tx, pending, t, cur, next); err != nil {
			return err
		}
		return Published(m, ctx, tx, actor, core.OpEmployeeEdit, out)
	})
	if err != nil {
		concluded = false // the transaction did not commit
	}
	if errors.Is(err, core.ErrNone) {
		return core.NotFound("employee")
	}
	if err != nil {
		return err
	}
	out, _, err := withPersonal(m, ctx, id)
	if err != nil {
		return err
	}
	return answer(c, out, extra)
}

// employee reads an employee in a transaction of its own (the state an intent is written from).
func employee(m *core.Base, ctx context.Context, id string) (core.Employee, error) {
	var e core.Employee
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		e, err = core.GetEmployee(ctx, tx, id, false)
		return err
	})
	return e, err
}

func same(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// write writes an employee's changeable fields with a new version (the job and the replay write through it too).
func write(ctx context.Context, tx db.Tx, e core.Employee, p core.Personal) error {
	nb, _ := json.Marshal(e.Names)
	_, err := tx.Exec(ctx, `UPDATE employee SET name = $2, names = $3, state = $4, termination_date = $5, end_reason = $6,
		birth_date = $7, national_id = $8, search = $9, search_version = $10, version = version + 1, updated_at = now()
		WHERE id = $1`, e.ID, e.Name, nb, e.State, e.TerminationDate, e.EndReason, p.BirthDate, p.NationalID,
		core.SearchOf(e.EmployeeNo, e.Name, e.Names), search.Version)
	return err
}

func UpdateEmployee(m *core.Base, c *httpx.Context) error {
	var in employeeIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return core.Invalid("name", "name cannot be empty")
	}
	if in.Names != nil {
		if err := core.CheckNames(*in.Names); err != nil {
			return err
		}
	}
	if err := checkPersonal(in.Personal); err != nil {
		return err
	}
	return change(m, c, in.Version, nil, func(_ context.Context, _ db.Tx, e *core.Employee, p *core.Personal, _ string) error {
		if in.Name != nil {
			e.Name = *in.Name
		}
		if in.Names != nil {
			e.Names = *in.Names
		}
		if in.Personal != nil {
			*p = *in.Personal
		}
		return nil
	}, nil)
}

func optionalVersion(c *httpx.Context) (*int64, error) {
	var in struct {
		Version *int64 `json:"version"`
	}
	if c.Request().ContentLength > 0 { // the body is optional
		if err := c.BindJSON(&in); err != nil {
			return nil, err
		}
	}
	return in.Version, nil
}

// Terminate terminates on a date: today or before at once (ending what the termination ends beyond the employee),
// later recorded and carried out by the job. The answer warns when a linked account holds the authorization admin
// role (D62); it never refuses for that.
func Terminate(m *core.Base, c *httpx.Context) error {
	var in struct {
		Version         *int64 `json:"version"`
		TerminationDate string `json:"termination_date"`
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if !core.DateForm.MatchString(in.TerminationDate) {
		return core.Invalid("termination_date", "a date, YYYY-MM-DD")
	}
	warn := false
	if m.AdminWarning != nil && core.UUIDForm.MatchString(c.Param("id")) {
		var err error
		if warn, err = m.AdminWarning(c.Ctx(), c.Param("id")); err != nil {
			return err
		}
	}
	tight := func(today string) *tightening.Tightening[core.Employee] {
		if in.TerminationDate > today { // registered for later: recorded now (a restore must not forget it)
			return tightening.ScheduledTermination(in.TerminationDate, today)
		}
		return tightening.Termination(in.TerminationDate, today)
	}
	return change(m, c, in.Version, tight, func(ctx context.Context, tx db.Tx, e *core.Employee, _ *core.Personal, today string) error {
		if e.State != "active" {
			return core.ErrState.New("the employee is " + e.State + ": only an active employee is terminated")
		}
		if in.TerminationDate < e.HireDate {
			return core.Invalid("termination_date", "before the hire date "+e.HireDate)
		}
		d := in.TerminationDate
		e.TerminationDate = &d
		if d <= today {
			return terminated(m, ctx, tx, core.Principal(ctx), e, d)
		}
		return nil
	}, map[string]any{"last_admin_warning": warn})
}

// terminated makes an employee terminated as of a date and ends what the termination ends beyond it.
func terminated(m *core.Base, ctx context.Context, tx db.Tx, actor string, e *core.Employee, date string) error {
	reason := "terminated"
	e.State, e.EndReason = "terminated", &reason
	if m.Terminated != nil {
		return m.Terminated(ctx, tx, actor, e.ID, date)
	}
	return nil
}

// WithdrawTermination withdraws a termination whose date has not come.
func WithdrawTermination(m *core.Base, c *httpx.Context) error {
	v, err := optionalVersion(c)
	if err != nil {
		return err
	}
	return change(m, c, v, nil, func(_ context.Context, _ db.Tx, e *core.Employee, _ *core.Personal, today string) error {
		if e.State != "active" || e.TerminationDate == nil {
			return core.ErrState.New("no termination to withdraw")
		}
		if *e.TerminationDate <= today { // due: the job carries it out within a minute; it is a fact now
			return core.ErrState.New("the termination's date has come: it is carried out, not withdrawn")
		}
		e.TerminationDate = nil
		return nil
	}, nil)
}

// CancelHire cancels a hire that has not begun.
func CancelHire(m *core.Base, c *httpx.Context) error {
	v, err := optionalVersion(c)
	if err != nil {
		return err
	}
	return change(m, c, v, nil, func(_ context.Context, _ db.Tx, e *core.Employee, _ *core.Personal, _ string) error {
		if e.State != "pre_hire" {
			return core.ErrState.New("the employee is " + e.State + ": only a hire that has not begun is cancelled")
		}
		reason := "cancelled"
		e.State, e.EndReason = "terminated", &reason
		return nil
	}, nil)
}

var employeeList = list.Spec{Operation: core.OpEmployeeRead, Alias: "e", IDColumn: "id", IDKind: list.UUID,
	Columns: []list.Column{
		{Field: "employee_no", Column: "employee_no", Kind: list.Text, Sortable: true},
		{Field: "legal_entity_id", Column: "legal_entity_id", Kind: list.UUID, Filterable: true},
		{Field: "state", Column: "state", Kind: list.Text, Filterable: true}},
	Search: []list.SearchColumn{{Name: "names", Column: "search", Fields: []string{"employee_no", "name", "names"}}}}

// ListEmployees lists employees by legal entity, state or keyword, by number; never the personal data.
func ListEmployees(m *core.Base, c *httpx.Context) error {
	if v := c.Query("legal_entity_id"); v != "" && !core.UUIDForm.MatchString(v) {
		return core.Invalid("legal_entity_id", "legal_entity_id is an ID")
	}
	ctx, q, err := core.Build(c, employeeList, list.Request{Filters: core.Eq(c, "legal_entity_id", "state"),
		Q: c.Query("q"), Sort: []list.Order{{Field: "employee_no"}}})
	if err != nil {
		return err
	}
	out, next, err := list.Page(ctx, m.RT.DB, q, "employee", core.EmployeeCols, core.ScanEmployee,
		func(e core.Employee) ([]string, string) { return []string{e.EmployeeNo}, e.ID })
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"employees": out, "next_cursor": next})
}

// Effective carries out the hires and the terminations whose date came, legal entity by legal entity (each its own
// today). A termination ends what it ends beyond the employee, as one made today does.
func Effective(m *core.Base, ctx context.Context, _ *work.Run) error {
	var les []string
	err := m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT legal_entity_id::text FROM employee WHERE state = 'pre_hire'
			OR (state = 'active' AND termination_date IS NOT NULL)`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var le string
			if err := rows.Scan(&le); err != nil {
				return err
			}
			les = append(les, le)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	var all []error
	for _, le := range les {
		today, err := Today(m, ctx, le)
		if err != nil {
			all = append(all, err)
			continue
		}
		all = append(all, effectiveIn(m, ctx, le, today))
	}
	return errors.Join(all...)
}

const jobActor = "job:" + JobEffective

func effectiveIn(m *core.Base, ctx context.Context, le, today string) error {
	var due []string
	err := m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id::text FROM employee WHERE legal_entity_id = $1 AND ((state = 'pre_hire'
			AND hire_date <= $2) OR (state = 'active' AND termination_date <= $2))`, le, today)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			due = append(due, id)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	for _, id := range due {
		// a termination that came due tightens: its intent first, from the employee read without a lock
		var t *tightening.Tightening[core.Employee]
		var pending *evidence.Pending
		if pre, err := employee(m, ctx, id); err == nil && pre.State == "active" && pre.TerminationDate != nil {
			t = tightening.Termination(*pre.TerminationDate, today)
			pending = tightening.Intent(ctx, m.Ev, t, id, pre.Version, pre)
		}
		concluded := false
		err := m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
			concluded = false
			cur, err := core.GetEmployee(ctx, tx, id, true)
			if err != nil {
				return err
			}
			next := cur
			switch {
			case cur.State == "pre_hire" && cur.HireDate <= today:
				next.State = "active"
			case cur.State == "active" && cur.TerminationDate != nil && *cur.TerminationDate <= today:
				if err := terminated(m, ctx, tx, jobActor, &next, *cur.TerminationDate); err != nil {
					return err
				}
			default:
				return nil // changed meanwhile
			}
			var p core.Personal
			if err := tx.QueryRow(ctx, `SELECT birth_date::text, national_id FROM employee WHERE id = $1`, id).Scan(
				&p.BirthDate, &p.NationalID); err != nil {
				return err
			}
			if err := write(ctx, tx, next, p); err != nil {
				return err
			}
			out, err := core.GetEmployee(ctx, tx, id, false)
			if err != nil {
				return err
			}
			if t != nil {
				if concluded, err = tightening.Conclude(ctx, tx, pending, t, cur, next); err != nil {
					return err
				}
			}
			return Published(m, ctx, tx, jobActor, core.OpEmployeeEdit, out)
		})
		if err != nil {
			concluded = false
		}
		tightening.Settle(ctx, pending, concluded)
		if err != nil {
			return err
		}
	}
	return nil
}

// ReplayTermination terminates the employee again as the evidence records it, after a full recovery (no business
// precondition: it held when the termination was made), ending its open appointments as a termination does. Already
// terminated: nothing to do. Not there: evidence.ErrDangling.
// ReplayScheduledTermination registers a termination for a later date again, as the evidence records it, after a full
// recovery: the effective-dates job carries it out when it comes. Not active any more, or an earlier date registered:
// nothing to do. Not there: evidence.ErrDangling.
func ReplayScheduledTermination(m *core.Base, ctx context.Context, tx db.Tx, actor, id, date string) error {
	cur, err := core.GetEmployee(ctx, tx, id, true)
	if errors.Is(err, core.ErrNone) {
		return fmt.Errorf("employee %s: %w", id, evidence.ErrDangling)
	}
	if err != nil || cur.State != "active" || cur.TerminationDate != nil && *cur.TerminationDate <= date {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE employee SET termination_date = $2, version = version + 1, updated_at = now()
		WHERE id = $1`, id, date); err != nil {
		return err
	}
	out, err := core.GetEmployee(ctx, tx, id, false)
	if err != nil {
		return err
	}
	return Published(m, ctx, tx, actor, core.OpEmployeeEdit, out)
}

func ReplayTermination(m *core.Base, ctx context.Context, tx db.Tx, actor, id, date string) error {
	cur, err := core.GetEmployee(ctx, tx, id, true)
	if errors.Is(err, core.ErrNone) {
		return fmt.Errorf("employee %s: %w", id, evidence.ErrDangling)
	}
	if err != nil || cur.State == "terminated" {
		return err
	}
	next := cur
	next.TerminationDate = &date
	if err := terminated(m, ctx, tx, actor, &next, date); err != nil {
		return err
	}
	var p core.Personal
	if err := tx.QueryRow(ctx, `SELECT birth_date::text, national_id FROM employee WHERE id = $1`, id).Scan(&p.BirthDate,
		&p.NationalID); err != nil {
		return err
	}
	if err := write(ctx, tx, next, p); err != nil {
		return err
	}
	out, err := core.GetEmployee(ctx, tx, id, false)
	if err != nil {
		return err
	}
	return Published(m, ctx, tx, actor, core.OpEmployeeEdit, out)
}
