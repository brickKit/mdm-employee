package employee

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	core "github.com/brickKit/mdm-employee/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/launcher"
	"github.com/brickKit/be-sdk-go/v3/mergesafety"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheManifestDeclaresWhatTheSDKReads: component.yaml's configSchema is exactly the keys the SDK reads.
func TestTheManifestDeclaresWhatTheSDKReads(t *testing.T) {
	raw, err := os.ReadFile("../component.yaml")
	require.NoError(t, err)
	if p := launcher.ManifestProblems(raw, Definition(), Own); len(p) > 0 {
		t.Errorf("component.yaml's configSchema differs from what the SDK reads:\n%v\nthe block:\n%s", p,
			launcher.ConfigSchemaYAML(Definition(), Own))
	}
}

// TestTheManifestListsThePublishedEvents: component.yaml's events.publishes is exactly the event contracts.
func TestTheManifestListsThePublishedEvents(t *testing.T) {
	raw, err := os.ReadFile("../component.yaml")
	require.NoError(t, err)
	assert.Empty(t, launcher.EventProblems(raw, os.DirFS("../contract/events")))
}

// TestTheRoutesAreTheOpenAPIDocument: every route is in contract/openapi.yaml with its operation, and nothing else is.
func TestTheRoutesAreTheOpenAPIDocument(t *testing.T) {
	raw, err := os.ReadFile("../contract/openapi.yaml")
	require.NoError(t, err)
	var r httpx.Routes
	require.NoError(t, (&Module{}).Routes(&r))
	assert.Empty(t, httpx.OpenAPIProblems(raw, r.List()))
}

// TestThePayloadsFitTheirSchemas: what events and the record read carry is what contract/events promises.
func TestThePayloadsFitTheirSchemas(t *testing.T) {
	end, reason := "2026-12-31", "transfer"
	for typ, v := range map[string]any{
		core.EmployeeChanged: core.Employee{ID: "01a15ab0-0000-7000-8000-0000000000e1", LegalEntityID: "01a15ab0-0000-7000-8000-0000000000a1",
			EmployeeNo: "E0001", Name: "Ana García", Names: map[string]string{"zh-CN": "安娜"}, State: "active",
			HireDate: "2026-01-05", Version: 1, UpdatedAt: "2026-10-20T08:00:00.000000Z"},
		core.AppointmentChanged: core.Appointment{ID: "01a15ab0-0000-7000-8000-0000000000f1", EmployeeID: "01a15ab0-0000-7000-8000-0000000000e1",
			LegalEntityID: "01a15ab0-0000-7000-8000-0000000000a1", UnitID: "01a15ab0-0000-7000-8000-0000000000b1",
			PositionID: "01a15ab0-0000-7000-8000-0000000000c1", StartDate: "2026-01-05", EndDate: &end, State: "ended",
			EndReason: &reason, Note: "", Version: 3, UpdatedAt: "2026-10-20T08:00:00.000000Z"},
		core.LinkChanged: core.Link{ID: "01a15ab0-0000-7000-8000-0000000000d1", EmployeeID: "01a15ab0-0000-7000-8000-0000000000e1",
			Account: "01a15ab0-0000-7000-8000-000000000099", State: "active", Confirmation: "01a15ab0-0000-7000-8000-0000000000c9",
			LinkedAt: "2026-10-20T08:00:00.000000Z", Version: 1, UpdatedAt: "2026-10-20T08:00:00.000000Z"},
	} {
		c := jsonschema.NewCompiler()
		s, err := c.Compile("../contract/events/" + typ + ".schema.json")
		require.NoError(t, err, typ)
		b, err := json.Marshal(v)
		require.NoError(t, err)
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		require.NoError(t, err)
		assert.NoError(t, s.Validate(doc), typ)
	}
}

// TestTheModuleIsMergeSafe: the module is compiled into the Go infra shell: the entry package and every business
// module under internal/.
func TestTheModuleIsMergeSafe(t *testing.T) {
	findings, err := mergesafety.CheckDir(".")
	require.NoError(t, err)
	inner, err := mergesafety.CheckTree("../internal")
	require.NoError(t, err)
	findings = append(findings, inner...)
	for _, f := range findings {
		t.Errorf("%v", f)
	}
}
