package core

import "context"

// OrgLegalEntity, OrgUnit and OrgPosition are what this component needs of mdm/org's records (its events carry more).
type OrgLegalEntity struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	State    string `json:"state"`
	Timezone string `json:"timezone"`
}

type OrgUnit struct {
	ID            string `json:"id"`
	LegalEntityID string `json:"legal_entity_id"`
	Path          string `json:"path"`
	State         string `json:"state"`
}

type OrgPosition struct {
	ID            string `json:"id"`
	LegalEntityID string `json:"legal_entity_id"`
	UnitID        string `json:"unit_id"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	State         string `json:"state"`
}

// OrgView answers mdm/org's records from this member's projection, filling a missing one from mdm/org (internal/orgview
// implements it; the module builds one per member, never a process-wide one). Not found: false.
type OrgView interface {
	LegalEntity(ctx context.Context, id string) (OrgLegalEntity, bool, error)
	Unit(ctx context.Context, id string) (OrgUnit, bool, error)
	Position(ctx context.Context, id string) (OrgPosition, bool, error)
}

// Retirement is the payload of JobEndOnRetirement: a unit or a position of mdm/org retired with open appointments on it.
type Retirement struct {
	Kind string `json:"kind"` // unit or position
	ID   string `json:"id"`
}

// JobEndOnRetirement ends the open appointments on a retired unit or position (the appointments module's job; the
// projection queues it in the transaction that learned of the retirement).
const JobEndOnRetirement = "end-on-retirement"
