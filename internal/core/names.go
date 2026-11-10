package core

import (
	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
)

// ID is the component's ID.
const ID = "mdm/employee"

// The stream and its event types (contract/events): a watermark source with the record read (protocol 02).
const (
	Stream             = "mdm.employee"
	Partitions         = 4
	EmployeeChanged    = "mdm.employee.changed.v1"
	AppointmentChanged = "mdm.employee.appointment.changed.v1"
	LinkChanged        = "mdm.employee.account_link.changed.v1"
)

// mdm/org: the component, its stream and the events this one projects.
const (
	Org                = "mdm/org"
	OrgStream          = "mdm.org"
	LegalEntityChanged = "mdm.legal_entity.changed.v1"
	UnitChanged        = "mdm.org_unit.changed.v1"
	PositionChanged    = "mdm.org_position.changed.v1"
)

// Resources and operations (contract/openapi.yaml names each route's operation).
const (
	Employees      = "mdm.employee"
	OpEmployeeRead = "mdm.employee.read"
	OpEmployeeEdit = "mdm.employee.manage"
	Appointments   = "mdm.employee.appointment"
	OpApptRead     = "mdm.employee.appointment.read"
	OpApptEdit     = "mdm.employee.appointment.manage"
	Links          = "mdm.employee.account_link"
	OpLinkRead     = "mdm.employee.account_link.read"
	OpLinkEdit     = "mdm.employee.account_link.manage"
	System         = "mdm.employee.system"
	// OpSystem is the record read of the watermark stream (be-protocol P9.30): its readers are the pullers.
	OpSystem = "mdm.employee.system.read"
	// OpPull is the watermark pull (P9.13): the authorization member only.
	OpPull = "mdm.employee.system.pull"
	// OpResend is the redelivery of this component's stream (P6.38: any component of the deployment).
	OpResend = "mdm.employee.system.resend"
	// OpRecover is a full recovery's checks (chapter 14: the recovery tool only).
	OpRecover = "mdm.employee.system.recover"
	// OpReplay is the replay of this member's evidence in a full recovery (P12.19: the recovery tool only).
	OpReplay = "mdm.employee.system.replay"
)

// Authz is the authorization member: the only puller of the watermark stream and the one that checks a link's
// confirmation (contract-infra-authz's AccountService).
const Authz = "infra/authz"

// Dimensions (security 07): the legal entity everywhere, and the department (an org-tree path) on appointments, their
// unit's path. Employees and links are scoped by legal entity only: an employee with several open appointments has
// several departments, and the SDK's org-tree dimension takes one path a row (design.md's open questions).
const (
	DimLegalEntity = "legal_entity"
	DimDepartment  = "department"
)

// PersonalKey is the read key of the restricted personal data (security 09a): a grant reads it only when it lists
// the field.
const PersonalKey = "mdm.employee.personal"

// MaxBatch is the most IDs a read takes and records a snapshot page gives (decision 0304).
const MaxBatch = 500

func fields(names ...string) map[string]sdkauthz.Field {
	out := map[string]sdkauthz.Field{}
	for _, n := range names {
		out[n] = sdkauthz.Field{Class: sdkauthz.Ordinary}
	}
	return out
}

// EmployeeFields, AppointmentFields and LinkFields are the fields every reader of a record sees.
var (
	EmployeeFields = []string{"id", "legal_entity_id", "employee_no", "name", "names", "state", "hire_date",
		"termination_date", "end_reason", "version", "updated_at"}
	AppointmentFields = []string{"id", "employee_id", "legal_entity_id", "unit_id", "position_id", "start_date", "end_date",
		"state", "end_reason", "note", "version", "updated_at"}
	LinkFields = []string{"id", "employee_id", "account", "state", "confirmation", "end_confirmation", "linked_at",
		"ended_at", "version", "updated_at"}
)

// Definitions are the component's resources and operations.
func Definitions() *sdkauthz.Definitions {
	entity := map[string]sdkauthz.Dimension{DimLegalEntity: {Kind: sdkauthz.ValueSet, Column: "legal_entity_id"}}
	scoped := map[string]sdkauthz.Dimension{
		DimLegalEntity: {Kind: sdkauthz.ValueSet, Column: "legal_entity_id"},
		DimDepartment:  {Kind: sdkauthz.OrgTree, Column: "unit_path"},
	}
	employee := fields(EmployeeFields...)
	employee["personal"] = sdkauthz.Field{Class: sdkauthz.Restricted, ReadKey: PersonalKey}
	op := func(key, typ, purpose, risk string, write bool) sdkauthz.Operation {
		o := sdkauthz.Operation{Key: key, ResourceType: typ, Purpose: purpose, RiskClass: risk}
		if write {
			o.SideEffects, o.Audit = []string{"business_write"}, true
		}
		return o
	}
	return &sdkauthz.Definitions{Component: ID,
		Resources: []sdkauthz.Resource{
			{Type: Employees, ViewOperation: OpEmployeeRead, Dimensions: entity, Fields: employee},
			{Type: Appointments, ViewOperation: OpApptRead, Dimensions: scoped, Fields: fields(AppointmentFields...)},
			{Type: Links, ViewOperation: OpLinkRead, Dimensions: entity, Fields: fields(LinkFields...)},
			{Type: System, ViewOperation: OpSystem, Dimensions: map[string]sdkauthz.Dimension{}, Fields: map[string]sdkauthz.Field{}},
		},
		Operations: []sdkauthz.Operation{
			op(OpEmployeeRead, Employees, "read", "low", false),
			op(OpEmployeeEdit, Employees, "manage", "high", true),
			op(OpApptRead, Appointments, "read", "low", false),
			op(OpApptEdit, Appointments, "manage", "high", true),
			op(OpLinkRead, Links, "read", "low", false),
			op(OpLinkEdit, Links, "manage", "high", true),
			op(OpSystem, System, "read", "low", false),
			op(OpPull, System, "read", "low", false),
			op(OpResend, System, "read", "low", false),
			op(OpRecover, System, "read", "low", false),
			{Key: OpReplay, ResourceType: System, Purpose: "action", RiskClass: "high", SideEffects: []string{"business_write"}},
		}}
}
