// Reproducible P0 experiment, separate from the product executable.
package main

import (
	"ael/internal/core"
	"ael/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) > 2 && os.Args[1] == "worker" {
		s, e := store.Open(os.Args[2])
		must(e)
		defer s.Close()
		start := time.Now()
		wins := 0
		for i := 0; i < 100; i++ {
			r, e := s.DB.Exec("UPDATE tasks SET step='claimed' WHERE id=? AND step='ready'", fmt.Sprint(i))
			must(e)
			n, _ := r.RowsAffected()
			wins += int(n)
		}
		fmt.Printf("{\"wins\":%d,\"ms\":%.3f}", wins, float64(time.Since(start).Microseconds())/1000)
		return
	}
	if len(os.Args) > 2 && os.Args[1] == "crash" {
		s, e := store.Open(os.Args[2])
		must(e)
		c, e := s.DB.Conn(context.Background())
		must(e)
		_, e = c.ExecContext(context.Background(), "BEGIN IMMEDIATE")
		must(e)
		_, e = c.ExecContext(context.Background(), "UPDATE tasks SET status='corrupt'")
		must(e)
		fmt.Println("uncommitted")
		time.Sleep(time.Hour)
		return
	}
	root := os.Args[1]
	must(os.MkdirAll(root, 0700))
	report := map[string]any{"go": runtime.Version(), "platform": runtime.GOOS + "/" + runtime.GOARCH, "cpus": runtime.NumCPU(), "at": time.Now().UTC()}
	var samples []any
	for _, count := range []int{1000, 10000} {
		dir := filepath.Join(root, strconv.Itoa(count))
		s, e := store.Open(dir)
		must(e)
		start := time.Now()
		must(s.Write(func(t *store.Tx) error {
			for i := 0; i < count; i++ {
				d := core.Definition{ID: fmt.Sprint(i), Title: "代表性中文工作项 / API bug", Type: "bug", Scope: []string{"src/api"}, Source: "issue:local", NonGoals: []string{"不发布生产"}, Acceptance: []string{"功能正确", "回归通过"}, Goal: "verified", Revision: 1, Checks: []core.Check{{ID: "functional", Kind: "functional", Command: []string{"go", "test", "./..."}, Covers: []string{"功能正确", "回归通过"}, Required: true}}}
				plan := core.MakePlan(&d)
				now := time.Now().UTC()
				task := core.Task{Definition: d, Plan: plan, Step: "verify", Status: "open", Created: now, Updated: now, Candidate: store.Hash([]byte(fmt.Sprint(i))), CandidateWorkspace: "/isolated/workspace"}
				for j := 0; j < 3; j++ {
					task.Attempts = append(task.Attempts, core.Attempt{ID: fmt.Sprintf("AT-%d-%d", i, j), Unit: "execute", Executor: "fixture-executor", Session: "scoped-session", Workspace: "/isolated/workspace", Input: task.Candidate, Candidate: task.Candidate, Started: now.Add(-time.Minute), Ended: &now, Lease: now, Token: "expired-fixture-token", Fence: j + 1, Status: "pass", Reason: "真实元数据分布；执行内容为基准夹具", Outputs: []string{"runtime:artifacts/summary.txt"}})
				}
				body, _ := json.Marshal(task)
				_, e := t.Exec("INSERT INTO tasks(id,body,status,step,updated,completed) VALUES(?,?,'active','ready',?,?)", fmt.Sprint(i), string(body), time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339))
				if e != nil {
					return e
				}
				for j := 0; j < 20; j++ {
					if e = t.Event(fmt.Sprint(i), "step", map[string]any{"attempt": j % 3, "reason": "验证失败返回修复", "candidate": "sha256:0123456789abcdef"}); e != nil {
						return e
					}
				}
			}
			return nil
		}))
		must(os.MkdirAll(filepath.Join(dir, "logs"), 0700))
		must(os.MkdirAll(filepath.Join(dir, "artifacts"), 0700))
		for i := 0; i < count; i++ {
			must(os.WriteFile(filepath.Join(dir, "logs", fmt.Sprint(i)+".log"), []byte(strings.Repeat("fixture command output\n", 48)), 0600))
			must(os.WriteFile(filepath.Join(dir, "artifacts", fmt.Sprint(i)+".txt"), []byte(strings.Repeat("fixture artifact summary\n", 24)), 0600))
		}
		sample := map[string]any{"work_items": count, "attempts": count * 3, "events": count * 20, "seed_ms": time.Since(start).Milliseconds()}
		var lat []float64
		for i := 0; i < 100; i++ {
			start = time.Now()
			must(s.Write(func(t *store.Tx) error {
				_, e := t.Exec("UPDATE tasks SET step='ready' WHERE id=?", fmt.Sprint(i))
				return e
			}))
			lat = append(lat, float64(time.Since(start).Microseconds())/1000)
		}
		sort.Float64s(lat)
		sample["single_write_p50_ms"] = lat[50]
		sample["single_write_p99_ms"] = lat[99]
		start = time.Now()
		for i := 0; i < 100; i++ {
			rows, e := s.DB.Query("SELECT id,body FROM tasks WHERE status='active' ORDER BY updated DESC LIMIT 50 OFFSET ?", i*5)
			must(e)
			for rows.Next() {
				var a, b string
				must(rows.Scan(&a, &b))
			}
			must(rows.Close())
			var n int
			must(s.DB.QueryRow("SELECT count(*) FROM tasks WHERE status='active'").Scan(&n))
			rows, e = s.DB.Query("SELECT id FROM tasks WHERE completed >= ? ORDER BY completed LIMIT 50", "2020")
			must(e)
			must(rows.Close())
		}
		sample["dashboard_100_queries_ms"] = time.Since(start).Milliseconds()
		sample["files_before_checkpoint"] = sizes(dir)
		var procs []*exec.Cmd
		for i := 0; i < 4; i++ {
			cmd := exec.Command(os.Args[0], "worker", dir)
			procs = append(procs, cmd)
		}
		var outs []string
		chs := make(chan string, 4)
		for _, cmd := range procs {
			go func(c *exec.Cmd) { b, e := c.CombinedOutput(); chs <- fmt.Sprintf("%s %v", b, e) }(cmd)
		}
		for range procs {
			outs = append(outs, <-chs)
		}
		sample["four_process_claim"] = outs
		crash := exec.Command(os.Args[0], "crash", dir)
		pipe, e := crash.StdoutPipe()
		must(e)
		must(crash.Start())
		b := make([]byte, 64)
		_, e = pipe.Read(b)
		must(e)
		must(crash.Process.Kill())
		_ = crash.Wait()
		var invalid int
		must(s.DB.QueryRow("SELECT count(*) FROM tasks WHERE status='corrupt'").Scan(&invalid))
		sample["uncommitted_after_kill"] = invalid
		backup := filepath.Join(root, fmt.Sprintf("backup-%d.db", count))
		must(s.Backup(backup))
		st, e := os.Stat(backup)
		must(e)
		sample["consistent_backup_bytes"] = st.Size()
		var indexBytes int64
		must(s.DB.QueryRow("SELECT coalesce(sum(pgsize),0) FROM dbstat WHERE name IN ('task_status','task_completed','event_task')").Scan(&indexBytes))
		sample["index_bytes"] = indexBytes
		_, e = s.DB.Exec("DELETE FROM events WHERE id%2=0; PRAGMA wal_checkpoint(TRUNCATE); VACUUM;")
		must(e)
		sample["after_explicit_prune_vacuum"] = sizes(dir)
		must(s.Close())
		samples = append(samples, sample)
	}
	report["samples"] = samples
	b, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(b))
	must(os.WriteFile(filepath.Join(root, "capacity.json"), b, 0644))
}
func sizes(dir string) map[string]int64 {
	m := map[string]int64{"logs": 0, "artifacts": 0}
	must(filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		st, e := d.Info()
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(dir, p)
		key := filepath.ToSlash(rel)
		if strings.HasPrefix(key, "logs/") {
			key = "logs"
		}
		if strings.HasPrefix(key, "artifacts/") {
			key = "artifacts"
		}
		m[key] += st.Size()
		return nil
	}))
	return m
}

func must(e error) {
	if e != nil {
		panic(e)
	}
}
