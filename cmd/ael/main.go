package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/execution"
	"ael/internal/knowledge"
	"ael/internal/migration"
	"ael/internal/server"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

var build = "dev"

func main() {
	v, e := run(os.Args[1:])
	if e != nil {
		code := strings.SplitN(e.Error(), ":", 2)[0]
		_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"error": e.Error(), "code": code})
		os.Exit(1)
	}
	if v != nil {
		_ = json.NewEncoder(os.Stdout).Encode(v)
	}
}

type args struct {
	Words []string
	Flags map[string]string
}

func parse(raw []string) args {
	a := args{Flags: map[string]string{}}
	for i := 0; i < len(raw); i++ {
		if strings.HasPrefix(raw[i], "--") {
			key := strings.TrimPrefix(raw[i], "--")
			if x := strings.SplitN(key, "=", 2); len(x) == 2 {
				a.Flags[x[0]] = x[1]
				continue
			}
			a.Flags[key] = "true"
			if i+1 < len(raw) && !strings.HasPrefix(raw[i+1], "--") {
				i++
				a.Flags[key] = raw[i]
			}
		} else {
			a.Words = append(a.Words, raw[i])
		}
	}
	return a
}
func (a args) get(k, d string) string {
	if v := a.Flags[k]; v != "" {
		return v
	}
	return d
}
func (a args) has(k string) bool { return a.Flags[k] == "true" }
func input(path string, v any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func run(raw []string) (any, error) {
	a := parse(raw)
	cmd := strings.Join(a.Words, " ")
	root := a.get("project", ".")
	id := a.get("task", "")
	executor := a.get("executor", "attached")
	switch cmd {
	case "", "help":
		return map[string]any{"commands": []string{"version", "doctor", "init [--dry-run]", "refresh", "task create --file definition.json", "task show|plan --task ID", "task list", "task edit --task ID --file definition.json --reason TEXT", "task cancel --task ID --reason TEXT", "claim --task ID --executor NAME [--unit CHECK]", "heartbeat --task ID --attempt ID --token TOKEN", "context --task ID", "submit --file submission.json", "next --task ID", "resume --task ID [--ack-stopped] [--return-to execute]", "verify --task ID --check ID", "confirm --task ID --kind plan|revision|delivery --decision approve|adjust|reject --revision N --plan-revision N --candidate HASH --reason TEXT --executor PERSON", "confirm --kind review --task ID --check ID --artifacts report.md --reason TEXT --executor PERSON", "dashboard [--addr 127.0.0.1:7331]", "loop run --task ID --config executor.json | --codex [--session EXACT_ID]", "executor probe", "workspace bind --path WORKTREE", "knowledge search --query TEXT", "knowledge read --id ID", "migrate scan --out REPORT", "migrate plan --inventory REPORT/inventory.json --out REPORT", "migrate validate --plan REPORT/plan.json [--proposal proposal.json]", "migrate apply --plan REPORT/plan.json --approved-digest HASH [--proposal proposal.json]", "migrate verify|repair|rollback --migration ID", "activate --migration ID --approved-digest HASH --ack-drained", "storage stats", "storage backup --out NEW_DIR", "storage restore --from EXPORT_DIR", "storage prune [--before RFC3339] [--apply]", "repair"}, "global": "--project PATH; all output JSON; errors stderr exit 1", "definition": core.Definition{}, "submission": core.Submission{}, "step_result": core.StepResult{}}, nil
	case "version":
		return map[string]string{"version": bootstrap.Version, "build": build}, nil
	case "doctor":
		return bootstrap.Doctor(root)
	case "init":
		return bootstrap.Init(root, a.has("dry-run"), false)
	case "refresh":
		return bootstrap.Init(root, a.has("dry-run"), true)
	}
	if cmd == "migrate scan" {
		inv, e := migration.Scan(root)
		if e != nil {
			return nil, e
		}
		if out := a.get("out", ""); out != "" {
			e = migration.SaveInventory(inv, out)
		}
		return inv, e
	}
	if cmd == "migrate plan" {
		var inv migration.Inventory
		if e := migration.ReadJSON(a.get("inventory", ""), &inv); e != nil {
			return nil, e
		}
		p, e := migration.MakePlan(inv)
		if e == nil && a.get("out", "") != "" {
			e = migration.SavePlan(p, a.get("out", ""))
		}
		return p, e
	}
	if cmd == "migrate validate" {
		var p migration.Plan
		if e := migration.ReadJSON(a.get("plan", ""), &p); e != nil {
			return nil, e
		}
		var proposal *migration.Proposal
		if a.get("proposal", "") != "" {
			proposal = &migration.Proposal{}
			if e := migration.ReadJSON(a.get("proposal", ""), proposal); e != nil {
				return nil, e
			}
		}
		d, e := migration.Validate(p, proposal)
		return map[string]string{"approved_digest": d, "plan_id": p.ID}, e
	}
	svc, _, e := bootstrap.Open(root)
	if e != nil {
		return nil, e
	}
	defer svc.Store.Close()
	switch cmd {
	case "migrate apply":
		var p migration.Plan
		if e = migration.ReadJSON(a.get("plan", ""), &p); e != nil {
			return nil, e
		}
		var proposal *migration.Proposal
		if a.get("proposal", "") != "" {
			proposal = &migration.Proposal{}
			if e = migration.ReadJSON(a.get("proposal", ""), proposal); e != nil {
				return nil, e
			}
		}
		return migration.Apply(svc, p, proposal, a.get("approved-digest", ""))
	case "migrate verify":
		return migration.Verify(svc, a.get("migration", ""))
	case "migrate repair":
		return migration.Repair(svc, a.get("migration", ""))
	case "migrate rollback":
		return migration.Rollback(svc, a.get("migration", ""))
	case "activate":
		return migration.Activate(svc, a.get("migration", ""), a.get("approved-digest", ""), a.has("ack-drained"))
	case "workspace bind":
		return svc.BindWorkspace(a.get("path", ""))
	case "storage stats":
		return svc.StorageStats()
	case "storage backup":
		return svc.Backup(a.get("out", ""))
	case "storage restore":
		return svc.Restore(a.get("from", ""))
	case "storage prune":
		before := time.Now().UTC().Add(-30 * 24 * time.Hour)
		if value := a.get("before", ""); value != "" {
			before, e = time.Parse(time.RFC3339, value)
			if e != nil {
				return nil, e
			}
		}
		return svc.Prune(before, a.has("apply"))
	case "repair":
		return map[string]string{"outbox": "repaired or conflict retained"}, svc.Store.Flush()
	case "task create":
		var d core.Definition
		if e = input(a.get("file", ""), &d); e != nil {
			return nil, e
		}
		return svc.Create(d)
	case "task edit":
		var d core.Definition
		if e = input(a.get("file", ""), &d); e != nil {
			return nil, e
		}
		return svc.Edit(id, d, a.get("reason", ""))
	case "task list":
		var limit, offset, days int
		fmt.Sscan(a.get("limit", "20"), &limit)
		fmt.Sscan(a.get("offset", "0"), &offset)
		fmt.Sscan(a.get("days", "0"), &days)
		return svc.QueryPage(core.Query{Limit: limit, Offset: offset, Days: days, Text: a.get("query", ""), Board: a.get("board", "")})
	case "task show", "task plan", "next":
		return svc.Next(id)
	case "claim":
		return svc.Claim(id, a.get("unit", ""), executor, a.get("workspace", ""), a.get("session", ""))
	case "heartbeat":
		return svc.Heartbeat(id, a.get("attempt", ""), a.get("token", ""))
	case "submit":
		var sub core.Submission
		if e = input(a.get("file", ""), &sub); e != nil {
			return nil, e
		}
		return svc.Submit(sub)
	case "verify":
		return svc.Verify(id, a.get("check", ""), executor)
	case "resume":
		return svc.Resume(id, a.get("return-to", ""), a.has("ack-stopped"))
	case "task cancel":
		return svc.Cancel(id, a.get("reason", ""))
	case "task pause":
		return svc.Pause(id, a.get("reason", ""))
	case "confirm":
		if a.get("kind", "") == "check-status" {
			return svc.RecordOptionalStatus(id, a.get("check", ""), a.get("status", ""), executor, a.get("reason", ""))
		}
		if a.get("kind", "") == "review" {
			return svc.Review(id, a.get("check", ""), executor, a.get("reason", ""), strings.Split(a.get("artifacts", ""), ","))
		}
		var rev, plan int
		fmt.Sscan(a.get("revision", "0"), &rev)
		fmt.Sscan(a.get("plan-revision", "0"), &plan)
		return svc.Confirm(id, a.get("kind", ""), a.get("decision", ""), executor, a.get("reason", ""), a.get("candidate", ""), rev, plan)
	case "executor probe":
		return execution.ProbeCodex()
	case "loop run":
		var c execution.Config
		if path := a.get("config", ""); path != "" {
			if e = input(path, &c); e != nil {
				return nil, e
			}
		}
		c.Codex = c.Codex || a.has("codex")
		c.Session = a.get("session", c.Session)
		return execution.Run(svc, id, c)
	case "context":
		return knowledge.Context(svc, id, a.get("query", ""), 12000)
	case "knowledge search":
		return knowledge.Search(svc.Root, a.get("query", ""))
	case "knowledge read":
		all, e := knowledge.ReadAll(svc.Root)
		if e != nil {
			return nil, e
		}
		for _, r := range all {
			if r.ID == a.get("id", "") {
				return r, nil
			}
		}
		return nil, core.Error("KNOWLEDGE_MISSING", a.get("id", ""))
	case "dashboard":
		return nil, server.Listen(svc, a.get("addr", "127.0.0.1:7331"), nil)
	default:
		return nil, core.Error("UNKNOWN_COMMAND", cmd)
	}
}
