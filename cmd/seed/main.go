// Command seed fills a running mdm/employee with the seed data for people (be-assembly-standard's seed and test data
// conventions):
//
//   - access: the roles of mdm/employee's operations granted by the authorization admin: maintain (everything, the
//     restricted personal data too) to the seed user and test.mdm.admin; read (no personal data) to test.mdm.viewer;
//   - employees: CN01 230 (more than one page), ES01 40, US01 30; three in CN01 hired later (pre_hire), one in CN01
//     with a termination planned (2027-01-31), one in ES01 terminated today;
//   - appointments on mdm/org's positions (its seed: a head -MGR in every active unit, a sales representative -REP in
//     every sales team): the first employees head the units in code order, the others are sales representatives in
//     turn; the CN01 head of finance also heads accounts receivable (two appointments at once); one CN01 representative
//     is transferred today to SALES-SOUTH-KA (the old appointment ended yesterday, a new one);
//   - account links (protected: the seed user asks, the authorization admin confirms, security 08 row 21):
//     test.sales.cn to the first SALES-EAST-SMB representative, test.sales.multi to one with appointments in
//     SALES-EAST-KA and SALES-SOUTH-KA too, test.sales.es to the first ES01 SALES-NORTH-KA representative,
//     test.unit.admin.cn to the CN01 head of SALES,
//     test.mdm.admin and test.mdm.viewer both to one CN01 employee (one person, two accounts); test.revoked's link
//     ended. test.nobody has none.
//
// Every name starts with 「本地测试」; employee numbers are the idempotency keys (a hire of the same number answers
// the first one), an employee with appointments gets no more, an account already linked is left alone: a second run
// creates nothing. The authorization member must have this component as its employee source (EMPLOYEE_COMPONENT):
// the links' confirmations bind the employee.
//
//	SEED_IAM_URL (http://localhost:18200)    the identity member's development exchange
//	SEED_AUTHZ_URL (http://localhost:18223)  the authorization member
//	SEED_ADMIN (test.alice)                  the authorization admin (the install claim): grants and confirms
//	SEED_USER (test.bob), SEED_PASSWORD      the user that writes the data; the test accounts' password
//	MDM_ORG_URL (http://localhost:18083)     the legal entities and positions (mdm/org's seed first)
//	MDM_EMPLOYEE_URL (http://localhost:18227)
//
// Run: go run ./cmd/seed (the component, mdm/org with its seed, and the security members up).
package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/seed"
)

const prefix = "「本地测试」"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "seed mdm/employee: "+format+"\n", a...)
	os.Exit(1)
}

func check(err error) {
	if err != nil {
		fail("%v", err)
	}
}

type client struct{ seed.Client }

func (c client) do(method, path string, body any) (int, map[string]any) {
	code, out, err := c.Do(method, path, body)
	check(err)
	return code, out
}

func (c client) must(method, path string, body any) map[string]any {
	out, err := c.Must(method, path, body)
	check(err)
	return out
}

// confirm makes a confirmation at the authorization member (it answers 201).
func (c client) confirm(body map[string]any) map[string]any {
	code, out := c.do("POST", "/api/authz/confirmations", body)
	if code != 201 {
		fail("confirmation %v: %d %v", body, code, out)
	}
	return out
}

// list reads every page of a list route.
func (c client) list(path, key string) []map[string]any {
	var all []map[string]any
	cursor := ""
	for {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		p := path + sep + "limit=200"
		if cursor != "" {
			p += "&cursor=" + url.QueryEscape(cursor)
		}
		out := c.must("GET", p, nil)
		items, _ := out[key].([]any)
		for _, i := range items {
			all = append(all, i.(map[string]any))
		}
		if cursor, _ = out["next_cursor"].(string); cursor == "" {
			return all
		}
	}
}

func fieldsSeg(resource, op string, fields []string, dims ...string) sdkauthz.Segment {
	s := seed.Segment(resource, op, dims...)
	s.ReadFields = fields
	return s
}

var (
	employeeFields = []string{"id", "legal_entity_id", "employee_no", "name", "names", "state", "hire_date",
		"termination_date", "end_reason", "version", "updated_at"}
	appointmentFields = []string{"id", "employee_id", "legal_entity_id", "unit_id", "position_id", "start_date", "end_date",
		"state", "end_reason", "note", "version", "updated_at"}
	linkFields = []string{"id", "employee_id", "account", "state", "confirmation", "end_confirmation", "linked_at", "ended_at",
		"version", "updated_at"}
)

// access grants mdm/employee's roles; it answers the seed user's client, the admin's (to confirm links) and the
// accounts.
func access(base, authzBase string) (client, client, map[string]seed.Account) {
	pw := os.Getenv("SEED_PASSWORD")
	if pw == "" {
		fail("SEED_PASSWORD is not set (the test accounts' password in Casdoor)")
	}
	iam, admin, user := env("SEED_IAM_URL", "http://localhost:18200"), env("SEED_ADMIN", "test.alice"), env("SEED_USER", "test.bob")
	accounts, err := seed.Accounts(iam, pw, admin, user, "test.mdm.admin", "test.mdm.viewer", "test.sales.cn", "test.sales.multi",
		"test.sales.es", "test.unit.admin.cn", "test.revoked")
	check(err)
	a, err := seed.NewAuthz(authzBase, accounts[admin].Token, "mdm/employee")
	check(err)
	le, dept := "legal_entity", "department"
	read := []sdkauthz.Segment{fieldsSeg("mdm.employee", "mdm.employee.read", employeeFields, le),
		fieldsSeg("mdm.employee.appointment", "mdm.employee.appointment.read", appointmentFields, le, dept),
		fieldsSeg("mdm.employee.account_link", "mdm.employee.account_link.read", linkFields, le)}
	maintain := []sdkauthz.Segment{fieldsSeg("mdm.employee", "mdm.employee.read", append(employeeFields, "personal"), le),
		read[1], read[2],
		seed.Segment("mdm.employee", "mdm.employee.manage", le),
		seed.Segment("mdm.employee.appointment", "mdm.employee.appointment.manage", le, dept),
		seed.Segment("mdm.employee.account_link", "mdm.employee.account_link.manage", le)}
	_, err = a.GrantRoles([]seed.RoleSpec{
		{Key: "maintain", Name: prefix + "员工维护", To: []string{user, "test.mdm.admin"}, Segments: maintain},
		{Key: "read", Name: prefix + "员工只读", To: []string{"test.mdm.viewer"}, Segments: read},
	}, accounts)
	check(err)
	c := client{seed.NewClient(base, accounts[user].Token)}
	check(c.WaitGranted("POST", "/api/employee/employees", 30*time.Second))
	return c, client{seed.NewClient(authzBase, accounts[admin].Token)}, accounts
}

var (
	cnSurnames  = []string{"王", "李", "张", "刘", "陈", "杨", "赵", "黄", "周", "吴", "徐", "孙", "胡", "朱", "高", "林", "何", "郭", "马", "罗"}
	cnGiven     = []string{"伟", "芳", "娜", "敏", "静", "磊", "洋", "艳", "勇", "军", "杰", "娟", "涛", "明", "超", "秀英", "霞", "平", "刚", "桂英"}
	esFirst     = []string{"Lucía", "Hugo", "Martina", "Mateo", "Sofía", "Leo", "Julia", "Daniel"}
	esLast      = []string{"García", "Fernández", "López", "Martínez", "Sánchez"}
	usFirst     = []string{"Olivia", "Liam", "Emma", "Noah", "Ava", "James"}
	usLast      = []string{"Smith", "Johnson", "Williams", "Brown", "Jones"}
	hireYears   = []string{"2019", "2020", "2021", "2022", "2023", "2024", "2025"}
	laterHire   = "2027-03-01" // pre_hire until then
	plannedTerm = "2027-01-31"
)

func name(code string, i int) string {
	switch code {
	case "CN01":
		return cnSurnames[i%len(cnSurnames)] + cnGiven[(i/len(cnSurnames)+i)%len(cnGiven)]
	case "ES01":
		return esFirst[i%len(esFirst)] + " " + esLast[(i/len(esFirst))%len(esLast)]
	}
	return usFirst[i%len(usFirst)] + " " + usLast[(i/len(usFirst))%len(usLast)]
}

// today is the business date of a legal entity's timezone.
func today(tz string) string {
	loc, err := time.LoadLocation(tz)
	check(err)
	return time.Now().In(loc).Format(time.DateOnly)
}

func main() {
	base, orgBase := env("MDM_EMPLOYEE_URL", "http://localhost:18227"), env("MDM_ORG_URL", "http://localhost:18083")
	authzBase := env("SEED_AUTHZ_URL", "http://localhost:18223")
	if !seed.Ready(base) {
		fail("mdm/employee at %s is not ready", base)
	}
	c, admin, accounts := access(base, authzBase)
	org := client{seed.NewClient(orgBase, accounts[env("SEED_USER", "test.bob")].Token)}
	entities := map[string]map[string]any{}
	for _, e := range org.list("/api/org/legal-entities", "legal_entities") {
		entities[e["code"].(string)] = e
	}
	counts := map[string]int{"CN01": 230, "ES01": 40, "US01": 30}
	employees, appointments := 0, 0
	byNo := map[string]map[string]any{} // employee number (with the legal entity) -> the employee
	posByCode := map[string]string{}    // legal entity/position code -> position
	holderOf := map[string]string{}     // legal entity/position code -> the first employee appointed to it
	for _, code := range []string{"CN01", "ES01", "US01"} {
		le := entities[code]
		if le == nil {
			fail("no legal entity %s: run mdm/org's seed first", code)
		}
		leID, tz := le["id"].(string), le["timezone"].(string)
		var heads, reps []map[string]any
		for _, p := range org.list("/api/org/positions?state=active&legal_entity_id="+leID, "positions") {
			posByCode[code+"/"+p["code"].(string)] = p["id"].(string)
			switch {
			case strings.HasSuffix(p["code"].(string), "-MGR"):
				heads = append(heads, p)
			case strings.HasSuffix(p["code"].(string), "-REP"):
				reps = append(reps, p)
			}
		}
		if len(heads) == 0 || len(reps) == 0 {
			fail("%s has no positions: run mdm/org's seed (3.0.34 or later) first", code)
		}
		for i := range counts[code] {
			no := fmt.Sprintf("%s-E%04d", code, i+1)
			hire := fmt.Sprintf("%s-%02d-%02d", hireYears[i%len(hireYears)], i%12+1, i%27+1)
			if code == "CN01" && i >= counts[code]-3 {
				hire = laterHire
			}
			body := map[string]any{"legal_entity_id": leID, "employee_no": no, "name": prefix + name(code, i), "hire_date": hire}
			if i%3 == 0 { // some have their restricted personal data
				body["personal"] = map[string]any{"birth_date": fmt.Sprintf("19%02d-%02d-%02d", 70+i%30, i%12+1, i%27+1)}
			}
			e := c.must("POST", "/api/employee/employees", body)
			if _, ok := e["id"].(string); !ok { // a 200 without the employee would panic below: say what came back
				fail("hire %s answered %v", no, e)
			}
			byNo[no] = e
			employees++
			pos := reps[i%len(reps)]
			if i < len(heads) {
				pos = heads[i]
			}
			if k := code + "/" + pos["code"].(string); holderOf[k] == "" {
				holderOf[k] = no
			}
			if len(c.must("GET", "/api/employee/employees/"+e["id"].(string)+"/appointments", nil)["appointments"].([]any)) > 0 {
				continue // appointed on an earlier run
			}
			c.must("POST", "/api/employee/employees/"+e["id"].(string)+"/appointments", map[string]any{
				"position_id": pos["id"], "start_date": hire})
			appointments++
		}
		// ES01's last employee is terminated today (its appointments end with it)
		if code == "ES01" {
			last := byNo[fmt.Sprintf("ES01-E%04d", counts[code])]
			if last["state"] == "active" {
				byNo[fmt.Sprintf("ES01-E%04d", counts[code])] = c.must("POST", "/api/employee/employees/"+last["id"].(string)+"/terminate",
					map[string]any{"termination_date": today(tz)})
			}
		}
	}
	// CN01: one termination planned
	planned := byNo["CN01-E0200"]
	if planned["termination_date"] == nil {
		c.must("POST", "/api/employee/employees/"+planned["id"].(string)+"/terminate", map[string]any{"termination_date": plannedTerm})
	}
	// CN01: the head of finance also heads accounts receivable for now (two open appointments)
	appointAlso := func(no, posCode string) {
		e := byNo[no]
		for _, a := range c.must("GET", "/api/employee/employees/"+e["id"].(string)+"/appointments", nil)["appointments"].([]any) {
			if a.(map[string]any)["position_id"] == posByCode["CN01/"+posCode] {
				return
			}
		}
		c.must("POST", "/api/employee/employees/"+e["id"].(string)+"/appointments", map[string]any{
			"position_id": posByCode["CN01/"+posCode], "start_date": "2026-01-05", "note": prefix + "兼任"})
		appointments++
	}
	appointAlso(holderOf["CN01/FIN-MGR"], "FIN-AR-MGR")
	// the multi-department salesperson: a representative in SALES-EAST and in SALES-SOUTH
	multi := "CN01-E0101"
	appointAlso(multi, "SALES-EAST-KA-REP")
	appointAlso(multi, "SALES-SOUTH-KA-REP")
	// CN01: a transfer today to SALES-SOUTH-KA (the old appointment ends yesterday, a new one begins)
	moved := byNo["CN01-E0102"]
	open := c.must("GET", "/api/employee/employees/"+moved["id"].(string)+"/appointments?state=active", nil)["appointments"].([]any)
	if len(open) == 1 && open[0].(map[string]any)["position_id"] != posByCode["CN01/SALES-SOUTH-KA-REP"] {
		c.must("POST", "/api/employee/employees/"+moved["id"].(string)+"/transfer", map[string]any{
			"from_appointment_id": open[0].(map[string]any)["id"], "position_id": posByCode["CN01/SALES-SOUTH-KA-REP"],
			"date": today("Asia/Shanghai")})
	}

	// account links: the seed user asks, the authorization admin confirms
	links := 0
	// link answers the employee's link of the account: made now, or found (an ended one included: the account is not
	// linked again)
	link := func(user, no string) map[string]any {
		account, emp := accounts[user].Principal, byNo[no]["id"].(string)
		for _, l := range c.must("GET", "/api/employee/employees/"+emp+"/accounts", nil)["links"].([]any) {
			if l.(map[string]any)["account"] == account {
				return l.(map[string]any)
			}
		}
		if code, out := c.do("GET", "/api/employee/accounts/"+account+"/employee", nil); code == 200 {
			fail("%s is linked to another employee (%v), not %s", user, out["employee_no"], no)
		}
		e := c.must("GET", "/api/employee/employees/"+emp, nil)
		conf := admin.confirm(map[string]any{"employee": e["id"], "account": account,
			"action": "link", "employee_version": e["version"]})
		links++
		return c.must("POST", "/api/employee/employees/"+emp+"/accounts", map[string]any{"account": account,
			"version": e["version"], "confirmation": conf["id"]})
	}
	link("test.sales.cn", holderOf["CN01/SALES-EAST-SMB-REP"])
	link("test.sales.multi", multi)
	link("test.sales.es", holderOf["ES01/SALES-NORTH-KA-REP"])
	link("test.unit.admin.cn", holderOf["CN01/SALES-MGR"]) // the head of sales
	link("test.mdm.admin", "CN01-E0150")
	link("test.mdm.viewer", "CN01-E0150")                                // one person, two accounts
	if l := link("test.revoked", "CN01-E0151"); l["state"] == "active" { // a link that ended
		e := c.must("GET", "/api/employee/employees/"+byNo["CN01-E0151"]["id"].(string), nil)
		conf := admin.confirm(map[string]any{"employee": e["id"], "account": l["account"],
			"action": "unlink", "employee_version": e["version"]})
		c.must("POST", "/api/employee/account-links/"+l["id"].(string)+"/end", map[string]any{"version": e["version"],
			"confirmation": conf["id"]})
	}
	fmt.Printf("seed mdm/employee: %d employees, %d new appointments, %d new links; roles maintain and read (%s)\n",
		employees, appointments, links, time.Now().Format(time.RFC3339))
}
