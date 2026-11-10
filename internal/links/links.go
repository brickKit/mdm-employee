// Package links is the links of accounts to employees (security 02, 08): an account to at most one employee at a time,
// an employee to several accounts; linking and ending are protected operations. The confirmation is checked with the
// authorization member before the transaction (AccountService.CheckLinkChange, contract-infra-authz alpha.6), the
// employee's version it binds is checked again under the employee's lock, and the link carries the confirmation: the
// authorization member marks it used when it applies the link from this member's watermark stream, and here a
// confirmation is used once (unique), since until then the authorization member still finds it unused. A termination
// leaves the links (the employee's state says the person left); before it, and before the end of a link, the
// authorization member says whether an account is an authorization admin, for the warning of D62.
package links

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	core "github.com/brickKit/mdm-employee/v3/internal/core"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/calls"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/ids"
	authzv3 "github.com/brickKit/contract-infra-authz/v3/gen/go/be/authz/v3"
	"github.com/jackc/pgx/v5/pgconn"
)

func facts(legalEntity string) sdkauthz.Facts {
	return sdkauthz.Facts{core.DimLegalEntity: sdkauthz.Value(legalEntity)}
}

func admit(c *httpx.Context, op, legalEntity string) (context.Context, string, error) {
	s, _ := sdkauthz.From(c.Ctx())
	ctx, _, err := s.Check(c.Ctx(), op, facts(legalEntity))
	if err != nil {
		return ctx, "", err
	}
	return ctx, core.Principal(ctx), nil
}

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

// check asks the authorization member whether the confirmation holds for the change. A nil error and handled=true:
// the answer is written (202 or a refusal); the caller stops.
func check(m *core.Base, c *httpx.Context, ctx context.Context, employee string, version int64, account, action,
	requester, confirmation string) (bool, error) {
	if m.Authz == nil {
		return false, errs.New(errs.ReasonDependencyUnavailable, core.Authz+" is not reachable: a link is checked there")
	}
	out, err := m.Authz.CheckLinkChange(ctx, &authzv3.CheckLinkChangeRequest{Employee: employee, Account: account,
		Action: action, EmployeeVersion: version, Requester: requester, Confirmation: confirmation}, calls.Idempotent())
	if err != nil {
		return false, err
	}
	switch out.GetVerdict() {
	case authzv3.AccountChangeVerdict_ACCOUNT_CHANGE_VERDICT_ALLOWED:
		return false, nil
	case authzv3.AccountChangeVerdict_ACCOUNT_CHANGE_VERDICT_CONFIRMATION_REQUIRED:
		return true, c.JSON(http.StatusAccepted, map[string]any{"outcome": "awaiting_confirmation",
			"reason": "CONFIRMATION_REQUIRED", "detail": out.GetReason(), "employee": employee, "account": account,
			"action": action, "version": version})
	case authzv3.AccountChangeVerdict_ACCOUNT_CHANGE_VERDICT_UNPROVABLE: // security 03: nothing decided, ask again later
		return true, c.JSON(http.StatusAccepted, map[string]any{"outcome": "processing",
			"reason": "ADMIN_AVAILABILITY_UNPROVABLE", "detail": out.GetReason()})
	default:
		return true, errs.New(errs.ReasonPermissionDenied, "the authorization member refused the change: "+out.GetReason())
	}
}

// used reports whether a confirmation was already used here, for a link or for an end.
func used(ctx context.Context, tx db.Tx, confirmation string) (bool, error) {
	var u bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM account_link WHERE confirmation = $1 OR end_confirmation = $1)`,
		confirmation).Scan(&u)
	return u, err
}

// unique maps a concurrent duplicate (the unique indexes) to the reasons the checks give.
func unique(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		if pg.ConstraintName == "account_link_active" {
			return core.ErrLinked.New("the account is linked to an employee already")
		}
		return core.ErrConfirmationUsed.New("the confirmation was used for another change")
	}
	return err
}

// Published writes the link's event (the employee and the account as subjects) and its audit record.
func Published(m *core.Base, ctx context.Context, tx db.Tx, actor string, l core.Link) error {
	if err := core.Emit(m, ctx, tx, core.LinkChanged, l.ID, l.Version, l, core.EmployeeSubject(l.EmployeeID),
		core.AccountSubject(l.Account)); err != nil {
		return err
	}
	return core.Change(m, ctx, tx, actor, core.OpLinkEdit, core.Links, l.ID, l)
}

func activeLink(ctx context.Context, tx db.Tx, account string) (bool, error) {
	var linked bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM account_link WHERE account = $1 AND state = 'active')`,
		account).Scan(&linked)
	return linked, err
}

// LinkAccount links an account to the employee, with a confirmation of exactly this link.
func LinkAccount(m *core.Base, c *httpx.Context) error {
	var in struct {
		Account      string `json:"account"`
		Version      *int64 `json:"version"`
		Confirmation string `json:"confirmation"`
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.Account == "" || len(in.Account) > 200 {
		return core.Invalid("account", "the account's platform principal")
	}
	if in.Version == nil {
		return core.Invalid("version", "the employee's version the confirmation binds")
	}
	emp, err := employeeOf(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	ctx, actor, err := admit(c, core.OpLinkEdit, emp.LegalEntityID)
	if err != nil {
		return err
	}
	// what this member decides alone first: the authorization member is not asked for a change that cannot be made
	if *in.Version != emp.Version {
		return core.ErrVersion.New(fmt.Sprintf("the employee is at version %d", emp.Version))
	}
	if emp.State == "terminated" {
		return core.ErrState.New("the employee is terminated: no new links")
	}
	var linked bool
	if err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		linked, err = activeLink(ctx, tx, in.Account)
		return err
	}); err != nil {
		return err
	}
	if linked {
		return core.ErrLinked.New("the account is linked to an employee already")
	}
	if handled, err := check(m, c, ctx, emp.ID, emp.Version, in.Account, "link", actor, in.Confirmation); handled || err != nil {
		return err
	}
	var out core.Link
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		cur, err := core.GetEmployee(ctx, tx, emp.ID, true)
		if err != nil {
			return err
		}
		if cur.Version != *in.Version {
			return core.ErrVersion.New(fmt.Sprintf("the employee is at version %d", cur.Version))
		}
		if cur.State == "terminated" {
			return core.ErrState.New("the employee is terminated: no new links")
		}
		if u, err := used(ctx, tx, in.Confirmation); err != nil || u {
			if err == nil {
				err = core.ErrConfirmationUsed.New("the confirmation was used for another change")
			}
			return err
		}
		if l, err := activeLink(ctx, tx, in.Account); err != nil || l {
			if err == nil {
				err = core.ErrLinked.New("the account is linked to an employee already")
			}
			return err
		}
		id := ids.New().String()
		if _, err := tx.Exec(ctx, `INSERT INTO account_link (id, employee_id, legal_entity_id, account, state, confirmation)
			VALUES ($1, $2, $3, $4, 'active', $5)`, id, cur.ID, cur.LegalEntityID, in.Account, in.Confirmation); err != nil {
			return unique(err)
		}
		if out, err = core.GetLink(ctx, tx, id, false); err != nil {
			return err
		}
		return Published(m, ctx, tx, actor, out)
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

// administrators answers whether any of the accounts holds the authorization admin role now (D62's warning).
func administrators(m *core.Base, ctx context.Context, accounts []string) (bool, error) {
	if len(accounts) == 0 {
		return false, nil
	}
	if m.Authz == nil {
		return false, errs.New(errs.ReasonDependencyUnavailable, core.Authz+" is not reachable")
	}
	out, err := m.Authz.Administrators(ctx, &authzv3.AdministratorsRequest{Accounts: accounts}, calls.Idempotent())
	if err != nil {
		return false, err
	}
	return len(out.GetAdministrators()) > 0, nil
}

// AdminWarning answers whether an account linked to the employee holds the authorization admin role (wired as
// core.Base.AdminWarning, asked before a termination, outside any transaction).
func AdminWarning(m *core.Base) func(ctx context.Context, employee string) (bool, error) {
	return func(ctx context.Context, employee string) (bool, error) {
		var accounts []string
		err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
			rows, err := tx.Query(ctx, `SELECT account FROM account_link WHERE employee_id = $1 AND state = 'active'`, employee)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var a string
				if err := rows.Scan(&a); err != nil {
					return err
				}
				accounts = append(accounts, a)
			}
			return rows.Err()
		})
		if err != nil {
			return false, err
		}
		return administrators(m, ctx, accounts)
	}
}

// EndLink ends an account link, with a confirmation of exactly this end; the answer warns when the account is an
// authorization admin (D62).
func EndLink(m *core.Base, c *httpx.Context) error {
	var in struct {
		Version      *int64 `json:"version"`
		Confirmation string `json:"confirmation"`
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.Version == nil {
		return core.Invalid("version", "the employee's version the confirmation binds")
	}
	id := c.Param("id")
	if !core.UUIDForm.MatchString(id) {
		return core.NotFound("account link")
	}
	var l core.Link
	if err := m.RT.DB.Tx(c.Ctx(), func(ctx context.Context, tx db.Tx) error {
		var err error
		l, err = core.GetLink(ctx, tx, id, false)
		return err
	}); err != nil {
		if errors.Is(err, core.ErrNone) {
			return core.NotFound("account link")
		}
		return err
	}
	emp, err := employeeOf(m, c.Ctx(), l.EmployeeID)
	if err != nil {
		return err
	}
	ctx, actor, err := admit(c, core.OpLinkEdit, emp.LegalEntityID)
	if err != nil {
		return err
	}
	if l.State != "active" {
		return core.ErrState.New("the link has ended")
	}
	if *in.Version != emp.Version {
		return core.ErrVersion.New(fmt.Sprintf("the employee is at version %d", emp.Version))
	}
	warn, err := administrators(m, ctx, []string{l.Account})
	if err != nil {
		return err
	}
	if handled, err := check(m, c, ctx, emp.ID, emp.Version, l.Account, "unlink", actor, in.Confirmation); handled || err != nil {
		return err
	}
	var out core.Link
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		cur, err := core.GetEmployee(ctx, tx, emp.ID, true)
		if err != nil {
			return err
		}
		if cur.Version != *in.Version {
			return core.ErrVersion.New(fmt.Sprintf("the employee is at version %d", cur.Version))
		}
		link, err := core.GetLink(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if link.State != "active" {
			return core.ErrState.New("the link has ended")
		}
		if u, err := used(ctx, tx, in.Confirmation); err != nil || u {
			if err == nil {
				err = core.ErrConfirmationUsed.New("the confirmation was used for another change")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE account_link SET state = 'ended', end_confirmation = $2, ended_at = now(),
			version = version + 1, updated_at = now() WHERE id = $1`, id, in.Confirmation); err != nil {
			return unique(err)
		}
		if out, err = core.GetLink(ctx, tx, id, false); err != nil {
			return err
		}
		return Published(m, ctx, tx, actor, out)
	})
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"id": out.ID, "employee_id": out.EmployeeID, "account": out.Account,
		"state": out.State, "confirmation": out.Confirmation, "end_confirmation": out.EndConfirmation,
		"linked_at": out.LinkedAt, "ended_at": out.EndedAt, "version": out.Version, "updated_at": out.UpdatedAt,
		"last_admin_warning": warn})
}

// ListLinks lists an employee's links, the active ones first.
func ListLinks(m *core.Base, c *httpx.Context) error {
	emp, err := employeeOf(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	ctx, _, err := admit(c, core.OpLinkRead, emp.LegalEntityID)
	if err != nil {
		return err
	}
	out := []core.Link{}
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+core.LinkCols+` FROM account_link WHERE employee_id = $1
			ORDER BY state = 'active' DESC, linked_at`, emp.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			l, err := core.ScanLink(rows)
			if err != nil {
				return err
			}
			out = append(out, l)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"links": out})
}

// EmployeeOfAccount answers the employee an account is linked to now.
func EmployeeOfAccount(m *core.Base, c *httpx.Context) error {
	var emp string
	err := m.RT.DB.Tx(c.Ctx(), func(ctx context.Context, tx db.Tx) error {
		return tx.QueryRow(ctx, `SELECT employee_id::text FROM account_link WHERE account = $1 AND state = 'active'`,
			c.Param("account")).Scan(&emp)
	})
	if err != nil {
		return core.NotFound("employee of the account")
	}
	e, err := employeeOf(m, c.Ctx(), emp)
	if err != nil {
		return err
	}
	if _, _, err := admit(c, core.OpLinkRead, e.LegalEntityID); err != nil {
		return err
	}
	return c.JSON(200, e)
}
