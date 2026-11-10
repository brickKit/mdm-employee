package core

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/brickKit/be-sdk-go/v3/canon"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/jackc/pgx/v5"
)

// Employee is an employee's state as events and reads carry it (mdm.employee.changed.v1); Personal is the restricted
// personal data, only on a read by a grant that may read it, never in an event.
type Employee struct {
	ID              string            `json:"id"`
	LegalEntityID   string            `json:"legal_entity_id"`
	EmployeeNo      string            `json:"employee_no"`
	Name            string            `json:"name"`
	Names           map[string]string `json:"names"`
	State           string            `json:"state"`
	HireDate        string            `json:"hire_date"`
	TerminationDate *string           `json:"termination_date"`
	EndReason       *string           `json:"end_reason"`
	Version         int64             `json:"version"`
	UpdatedAt       string            `json:"updated_at"`
	Personal        *Personal         `json:"personal,omitempty"`
}

// Personal is the restricted personal data (security 09a's restricted fields).
type Personal struct {
	BirthDate  *string `json:"birth_date"`
	NationalID *string `json:"national_id"`
}

// Appointment is an appointment's state (mdm.employee.appointment.changed.v1).
type Appointment struct {
	ID            string  `json:"id"`
	EmployeeID    string  `json:"employee_id"`
	LegalEntityID string  `json:"legal_entity_id"`
	UnitID        string  `json:"unit_id"`
	PositionID    string  `json:"position_id"`
	StartDate     string  `json:"start_date"`
	EndDate       *string `json:"end_date"`
	State         string  `json:"state"`
	EndReason     *string `json:"end_reason"`
	Note          string  `json:"note"`
	Version       int64   `json:"version"`
	UpdatedAt     string  `json:"updated_at"`
}

// Link is an account link's state (mdm.employee.account_link.changed.v1).
type Link struct {
	ID              string  `json:"id"`
	EmployeeID      string  `json:"employee_id"`
	Account         string  `json:"account"`
	State           string  `json:"state"`
	Confirmation    string  `json:"confirmation"`
	EndConfirmation *string `json:"end_confirmation"`
	LinkedAt        string  `json:"linked_at"`
	EndedAt         *string `json:"ended_at"`
	Version         int64   `json:"version"`
	UpdatedAt       string  `json:"updated_at"`
}

const (
	EmployeeCols = `id::text, legal_entity_id::text, employee_no, name, names, state, hire_date::text,
		termination_date::text, end_reason, version, updated_at`
	AppointmentCols = `id::text, employee_id::text, legal_entity_id::text, unit_id::text, position_id::text,
		start_date::text, end_date::text, state, end_reason, note, version, updated_at`
	LinkCols = `id::text, employee_id::text, account, state, confirmation, end_confirmation, linked_at, ended_at, version,
		updated_at`
)

func ScanEmployee(row pgx.Row) (Employee, error) {
	var e Employee
	var names []byte
	var at time.Time
	err := row.Scan(&e.ID, &e.LegalEntityID, &e.EmployeeNo, &e.Name, &names, &e.State, &e.HireDate, &e.TerminationDate,
		&e.EndReason, &e.Version, &at)
	if err == nil {
		e.UpdatedAt, err = canon.Time(at), json.Unmarshal(names, &e.Names)
	}
	return e, err
}

func ScanAppointment(row pgx.Row) (Appointment, error) {
	var a Appointment
	var at time.Time
	err := row.Scan(&a.ID, &a.EmployeeID, &a.LegalEntityID, &a.UnitID, &a.PositionID, &a.StartDate, &a.EndDate,
		&a.State, &a.EndReason, &a.Note, &a.Version, &at)
	if err == nil {
		a.UpdatedAt = canon.Time(at)
	}
	return a, err
}

func ScanLink(row pgx.Row) (Link, error) {
	var l Link
	var linked, at time.Time
	var ended *time.Time
	err := row.Scan(&l.ID, &l.EmployeeID, &l.Account, &l.State, &l.Confirmation, &l.EndConfirmation, &linked, &ended,
		&l.Version, &at)
	if err == nil {
		l.LinkedAt, l.UpdatedAt = canon.Time(linked), canon.Time(at)
		if ended != nil {
			s := canon.Time(*ended)
			l.EndedAt = &s
		}
	}
	return l, err
}

// ErrNone is a lookup that found no row.
var ErrNone = errors.New("no such record")

func one[T any](row pgx.Row, scan func(pgx.Row) (T, error)) (T, error) {
	v, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNone
	}
	return v, err
}

func get[T any](ctx context.Context, tx db.Tx, cols, table, id string, lock bool, scan func(pgx.Row) (T, error)) (T, error) {
	q := `SELECT ` + cols + ` FROM ` + table + ` WHERE id = $1::uuid`
	if lock {
		q += ` FOR UPDATE`
	}
	return one(tx.QueryRow(ctx, q, id), scan)
}

func GetEmployee(ctx context.Context, tx db.Tx, id string, lock bool) (Employee, error) {
	return get(ctx, tx, EmployeeCols, "employee", id, lock, ScanEmployee)
}

func GetAppointment(ctx context.Context, tx db.Tx, id string, lock bool) (Appointment, error) {
	return get(ctx, tx, AppointmentCols, "appointment", id, lock, ScanAppointment)
}

func GetLink(ctx context.Context, tx db.Tx, id string, lock bool) (Link, error) {
	return get(ctx, tx, LinkCols, "account_link", id, lock, ScanLink)
}

// Emit writes the record's full-state event into the outbox, in the transaction that changed it (protocol 02, S2).
// The stream is a watermark source: every event lists the subjects it is about (P9.13's besubjects, by which a
// consumer quarantines an event it cannot apply, P9.25): the employee, and for a link also the account.
func Emit(m *Base, ctx context.Context, tx db.Tx, typ, id string, version int64, data any, subjects ...string) error {
	_, err := m.RT.Events.Emit(ctx, tx, Stream, events.Event{Type: typ, RecordID: id, Version: version,
		Projection: events.Full, Data: data, Subjects: subjects})
	return err
}

// EmployeeSubject and AccountSubject are the subjects of the stream's events.
func EmployeeSubject(id string) string { return "employee:" + id }
func AccountSubject(p string) string   { return "account:" + p }
