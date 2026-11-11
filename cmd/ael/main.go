package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/execution"
	"ael/internal/knowledge"
	"ael/internal/migration"
	"ael/internal/process"
	"ael/internal/server"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var build = "dev"

func main() {
	options := parse(os.Args[1:])
	v, e := run(os.Args[1:])
	if e != nil {
		_ = writeError(os.Stderr, e, options.has("json"))
		os.Exit(1)
	}
	if v != nil {
		if e = writeOutput(os.Stdout, v, options.has("json"), options.has("verbose")); e != nil {
			_ = writeError(os.Stderr, e, options.has("json"))
			os.Exit(1)
		}
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
			if booleanFlag(key) {
				continue
			}
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
func booleanFlag(key string) bool {
	switch key {
	case "dry-run", "ack-stopped", "ack-drained", "apply", "codex", "json", "verbose", "no-color", "required":
		return true
	}
	return false
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
func run(raw []string) (value any, runErr error) {
	bootstrap.Build = build
	a := parse(raw)
	for _, flag := range []string{"json", "verbose", "no-color"} {
		if value, ok := a.Flags[flag]; ok && value != "true" && value != "false" {
			return nil, core.Error("INVALID_FLAG_VALUE", flag)
		}
	}
	cmd := strings.Join(a.Words, " ")
	root := a.get("project", ".")
	id := a.get("task", "")
	executor := a.get("executor", "attached")
	switch cmd {
	case "", "help":
		return map[string]any{"commands": []string{"version", "doctor", "init [--dry-run] [--bundle-dir DIR]", "upgrade [--from DIR] [--prefix DIR] [--dry-run]", "update [--project PATH] [--from DIR] [--prefix DIR] [--dry-run] [--ack-stopped]", "task create --file definition.json", "task show|plan --task ID", "task list [--parent EPIC]", "task tree --task EPIC", "task link --parent EPIC --task LEAF [--required=false] [--milestone NAME]", "task edit --task ID --file definition.json --reason TEXT", "task cancel --task ID --reason TEXT", "task delete --task ID --reason TEXT", "task budget --task ID --max-seconds 0 --reason TEXT", "claim --task ID --executor NAME [--unit CHECK] [--owner-pid PERSISTENT_AGENT_PID]", "heartbeat --task ID --attempt ID --token TOKEN", "context --task ID", "progress --task ID --attempt ID --token TOKEN --file progress.json", "assess --file assessment.json", "knowledge status", "knowledge index", "submit --file submission.json", "next --task ID", "resume --task ID [--ack-stopped] [--return-to execute]", "verify --task ID --check ID", "confirm --task ID --kind plan|revision|delivery --decision approve|adjust|reject --revision N --plan-revision N --candidate HASH --reason TEXT --executor PERSON", "confirm --kind review --task ID --check ID --artifacts report.md --reason TEXT --executor PERSON", "dashboard [--addr 0.0.0.0:7331]", "loop run --task ID --config executor.json | --codex [--session EXACT_ID]", "executor probe", "workspace bind --path WORKTREE", "knowledge search --query TEXT", "knowledge read --id ID", "migrate scan --out REPORT", "migrate plan --inventory REPORT/inventory.json --out REPORT", "migrate validate --plan REPORT/plan.json [--proposal proposal.json]", "migrate apply --plan REPORT/plan.json --approved-digest HASH [--proposal proposal.json]", "migrate verify|repair|rollback --migration ID", "activate --migration ID --approved-digest HASH --ack-drained", "storage stats", "storage backup --out NEW_DIR", "storage restore --from EXPORT_DIR", "storage prune [--before RFC3339] [--apply]", "repair"}, "global": "--project PATH; default text; --json for output protocol 1; --verbose; --no-color; errors stderr exit 1", "definition": core.Definition{}, "submission": core.Submission{}, "step_result": core.StepResult{}}, nil
	case "version":
		return map[string]any{"version": bootstrap.Version, "build": build, "cli_output_protocol": 1}, nil
	case "doctor":
		return bootstrap.Doctor(root)
	case "init":
		return bootstrap.InitProject(root, a.has("dry-run"), false, a.get("bundle-dir", ""))
	case "upgrade", "update":
		for flag, value := range a.Flags {
			if flag != "project" && flag != "prefix" && flag != "from" && flag != "bundle-dir" && flag != "dry-run" && flag != "ack-stopped" && flag != "json" && flag != "verbose" && flag != "no-color" {
				return nil, core.Error("UNKNOWN_FLAG", flag)
			}
			if !booleanFlag(flag) && value == "true" {
				return nil, core.Error("FLAG_VALUE_REQUIRED", flag)
			}
			if booleanFlag(flag) && value != "true" && value != "false" {
				return nil, core.Error("INVALID_FLAG_VALUE", flag)
			}
		}
		if cmd == "upgrade" && (a.Flags["project"] != "" || a.Flags["ack-stopped"] != "") {
			return nil, core.Error("INVALID_UPDATE_SCOPE", "upgrade updates the local installation; use update --project PATH for a project")
		}
		from := a.get("from", a.get("bundle-dir", ""))
		if a.Flags["from"] != "" && a.Flags["bundle-dir"] != "" && a.Flags["from"] != a.Flags["bundle-dir"] {
			return nil, core.Error("CONFLICTING_SOURCE", "use a single distribution source")
		}
		options := bootstrap.UpdateOptions{From: from, Prefix: a.get("prefix", ""), DryRun: a.has("dry-run"), AckStopped: a.has("ack-stopped")}
		if cmd == "upgrade" {
			return bootstrap.Upgrade(options)
		}
		return bootstrap.UpdateProject(root, options)
	case "refresh":
		return nil, core.Error("UNKNOWN_COMMAND", "refresh was removed; use update for the current project, or upgrade for the local AEL installation")
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
	if !a.has("json") {
		// Summaries need the open store. A display failure must not turn an
		// already successful mutation into an apparent command failure.
		defer func() { value = withTextSummaries(svc, value, cmd) }()
	}
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
		if e = svc.Store.Flush(); e == nil {
			e = svc.RecoverDefinitions()
		}
		return map[string]string{"outbox": "repaired or conflict retained", "records": "project checkpoints reconciled"}, e
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
	case "task budget":
		seconds, e := strconv.Atoi(a.get("max-seconds", ""))
		if e != nil {
			return nil, e
		}
		return svc.SetTimeLimit(id, seconds, a.get("reason", ""))
	case "task list":
		var limit, offset, days int
		fmt.Sscan(a.get("limit", "20"), &limit)
		fmt.Sscan(a.get("offset", "0"), &offset)
		fmt.Sscan(a.get("days", "0"), &days)
		return svc.QueryPage(core.Query{Limit: limit, Offset: offset, Days: days, Text: a.get("query", ""), Board: a.get("board", ""), Parent: a.get("parent", "")})
	case "task tree":
		return svc.Tree(id)
	case "task link":
		required := a.get("required", "true")
		if required != "true" && required != "false" {
			return nil, core.Error("INVALID_FLAG_VALUE", "required")
		}
		return svc.LinkEpic(a.get("parent", ""), id, required == "true", a.get("milestone", ""))
	case "task show", "task plan", "next":
		return svc.Next(id)
	case "claim":
		owner := 0
		identity := ""
		if value := a.get("owner-pid", ""); value != "" {
			owner, e = strconv.Atoi(value)
			if e != nil {
				return nil, e
			}
			identity, e = process.Identity(owner)
			if e != nil {
				return nil, e
			}
		}
		n, e := svc.Claim(id, a.get("unit", ""), executor, a.get("workspace", ""), a.get("session", ""))
		if e != nil || owner == 0 {
			return n, e
		}
		return startKeeper(svc, id, n, owner, identity)
	case "lease watch":
		owner, e := strconv.Atoi(a.get("owner-pid", ""))
		if e != nil {
			return nil, e
		}
		identity := os.Getenv("AEL_LEASE_OWNER_ID")
		if identity == "" {
			return nil, core.Error("OWNER_IDENTITY_REQUIRED", "use claim --owner-pid with the Agent's persistent PID")
		}
		attempt, token := a.get("attempt", ""), os.Getenv("AEL_LEASE_TOKEN")
		current, e := process.Identity(owner)
		if e != nil {
			return nil, e
		}
		if current != identity {
			return nil, core.Error("OWNER_CHANGED", "")
		}
		if _, e = svc.Heartbeat(id, attempt, token); e != nil {
			return nil, e
		}
		if os.Getenv("AEL_LEASE_READY") == "1" {
			ready := os.NewFile(3, "lease-ready")
			if ready == nil {
				return nil, core.Error("KEEPER_HANDSHAKE_FAILED", "")
			}
			_, e = ready.Write([]byte{1})
			ready.Close()
			if e != nil {
				return nil, e
			}
		}
		return nil, svc.WatchLease(context.Background(), id, attempt, token, owner, identity, 20*time.Second)
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
	case "task delete":
		return svc.Delete(id, a.get("reason", ""))
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
	case "progress":
		var p core.Progress
		if e = input(a.get("file", ""), &p); e != nil {
			return nil, e
		}
		return svc.Progress(id, a.get("attempt", ""), a.get("token", ""), p)
	case "assess":
		var r core.Assessment
		if e = input(a.get("file", ""), &r); e != nil {
			return nil, e
		}
		return svc.Assess(r)
	case "knowledge index":
		c, e := knowledge.Index(svc.Root)
		if e == nil {
			e = knowledge.Sync(svc)
		}
		return c, e
	case "knowledge status":
		return knowledge.Status(svc.Root), nil
	case "context":
		return knowledge.Context(svc, id, a.get("query", ""), 12000)
	case "knowledge search":
		return knowledge.Search(svc.Root, a.get("query", ""))
	case "knowledge read":
		if version := a.get("version", ""); version != "" {
			return knowledge.ReadVersion(svc, a.get("id", ""), version)
		}
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
		return nil, server.Listen(svc, a.get("addr", "0.0.0.0:7331"), nil, func(url string) {
			_ = writeOutput(os.Stdout, map[string]string{"url": url}, a.has("json"), a.has("verbose"))
		})
	default:
		return nil, core.Error("UNKNOWN_COMMAND", cmd)
	}
}

// The owner is persisted before spawning. Readiness proves the detached keeper
// opened the same runtime and renewed the claim before success is reported.
func startKeeper(s *core.Service, id string, n core.Next, owner int, identity string) (core.Next, error) {
	original := n.Attempt
	var e error
	n, e = s.BindLeaseOwner(id, original.ID, original.Token, owner, identity)
	if e != nil {
		return n, e
	}
	failed := func(e error) (core.Next, error) {
		_, _ = s.Interrupt(id, original.ID, original.Token, "attached keeper failed to start: "+e.Error())
		return n, e
	}
	exe, e := os.Executable()
	if e != nil {
		return failed(e)
	}
	reader, writer, e := os.Pipe()
	if e != nil {
		return failed(e)
	}
	defer reader.Close()
	defer writer.Close()
	dir := filepath.Join(s.Store.Dir, "artifacts", original.ID)
	if e = os.MkdirAll(dir, 0700); e != nil {
		return failed(e)
	}
	log, e := os.OpenFile(filepath.Join(dir, "keeper.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return failed(e)
	}
	defer log.Close()
	watcher := process.Command(context.Background(), exe, "lease", "watch", "--project", s.Root, "--task", id, "--attempt", original.ID, "--owner-pid", strconv.Itoa(owner))
	watcher.Env = append(os.Environ(), "AEL_LEASE_TOKEN="+original.Token, "AEL_LEASE_OWNER_ID="+identity, "AEL_LEASE_READY=1")
	watcher.ExtraFiles = []*os.File{writer}
	watcher.Stderr = log
	if e = watcher.Start(); e != nil {
		return failed(e)
	}
	writer.Close()
	ready := make(chan error, 1)
	go func() {
		var marker [1]byte
		_, e := io.ReadFull(reader, marker[:])
		if e == nil && marker[0] != 1 {
			e = core.Error("KEEPER_HANDSHAKE_FAILED", "")
		}
		ready <- e
	}()
	select {
	case e = <-ready:
	case <-time.After(5 * time.Second):
		e = core.Error("KEEPER_START_TIMEOUT", "")
	}
	if e != nil {
		_ = process.StopGroup(watcher.Process.Pid)
		_ = watcher.Wait()
		return failed(e)
	}
	n.KeeperPID = watcher.Process.Pid
	_ = watcher.Process.Release()
	return n, nil
}
