package execution

import (
	"ael/internal/core"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func nextStopReason(n core.Next, err error) string {
	if err != nil {
		return "error"
	}
	for _, reason := range n.Reasons {
		if strings.Contains(reason, "budget exhausted") {
			return "budget_stopped"
		}
		if strings.Contains(reason, "expired attempt") {
			return "runner_offline"
		}
	}
	if n.Task != nil && strings.Contains(n.Task.Failure, "requires") && strings.Contains(n.Task.Failure, "capability") {
		return "wait_capability"
	}
	switch n.Action {
	case "completed":
		return "completed"
	case "wait_confirmation":
		return "wait_human"
	case "wait_dependency":
		return "wait_dependency"
	case "wait_executor":
		return "wait_executor"
	case "wait_environment":
		return "wait_environment"
	case "cancelled":
		return "cancelled"
	default:
		return "stopped"
	}
}

// RunEpic coordinates only already-authorized leaf work. Each child process
// still receives one attempt and returns one result; it cannot start a loop.
func RunEpic(s *core.Service, parent string, c Config) (result core.WorkResult, runErr error) {
	if err := prepareConfig(&c); err != nil {
		if strings.HasPrefix(err.Error(), "EXECUTOR_MISSING:") {
			result, runErr = s.NextWork(core.WorkRequest{Parent: parent, Runner: c.Runner, Capabilities: c.Capabilities, Resources: c.Resources, Parallel: c.Parallel})
			if runErr != nil {
				return result, runErr
			}
			if result.Action == "ready" || result.Action == "wait_runner" || result.Action == "wait_capability" {
				result.Action, result.StopReason, result.Detail = "wait_capability", "wait_capability", "no local executor is configured"
				if strings.TrimSpace(c.Runner) == "" {
					result.Action, result.StopReason, result.Detail = "wait_runner", "wait_runner", "no runner is available"
				}
			}
			if checkpointErr := s.SaveWorkCheckpoint(result); checkpointErr != nil {
				return result, checkpointErr
			}
			return result, nil
		}
		return result, err
	}
	if strings.TrimSpace(c.Runner) == "" {
		result, runErr = s.NextWork(core.WorkRequest{Parent: parent, Capabilities: c.Capabilities, Resources: c.Resources, Parallel: c.Parallel})
		if runErr != nil {
			return result, runErr
		}
		if result.Action == "ready" || result.Action == "wait_capability" {
			result.Action, result.StopReason, result.Detail = "wait_runner", "wait_runner", "no runner is available"
		}
		return result, s.SaveWorkCheckpoint(result)
	}
	req := core.WorkRequest{Parent: parent, Runner: c.Runner, Capabilities: c.Capabilities, Resources: c.Resources, Parallel: c.Parallel, Workspace: c.Workspace, Session: c.Session}
	defer func() {
		if result.Checkpoint.Parent == "" {
			result.Checkpoint = core.WorkCheckpoint{Parent: parent, Runner: c.Runner, At: time.Now().UTC()}
		}
		if result.StopReason == "" {
			if runErr != nil {
				result.StopReason = "error"
			} else {
				result.StopReason = result.Action
			}
		}
		if err := s.SaveWorkCheckpoint(result); err != nil && runErr == nil {
			runErr = err
		}
	}()
	program := ""
	if c.Codex {
		program = "codex"
	} else if len(c.Command) > 0 {
		program = c.Command[0]
	}
	var executableErr error
	if strings.ContainsRune(program, filepath.Separator) {
		filename := program
		if !filepath.IsAbs(filename) {
			workspace := c.Workspace
			if workspace == "" {
				workspace = s.Root
			}
			filename = filepath.Join(workspace, filename)
		}
		info, err := os.Stat(filename)
		if err != nil {
			executableErr = err
		} else if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			executableErr = core.Error("EXECUTOR_UNAVAILABLE", "executable is not runnable")
		}
	} else {
		_, executableErr = exec.LookPath(program)
	}
	if executableErr != nil {
		choice, err := s.NextWork(req)
		if err != nil {
			return choice, err
		}
		if choice.Action == "ready" || choice.Action == "wait_capability" {
			choice.Action, choice.StopReason, choice.Detail = "wait_capability", "wait_capability", "executor unavailable: "+executableErr.Error()
		}
		return choice, nil
	}
	for i := 0; i < c.MaxSteps; i++ {
		req.Session = c.Session
		choice, err := s.ClaimNextWork(req)
		if err != nil {
			choice.Action, choice.StopReason, choice.Detail = "stopped", "error", err.Error()
			return choice, err
		}
		if choice.Action != "claimed" {
			return choice, nil
		}
		n := *choice.Next
		if n.Attempt == nil || n.Task == nil {
			return choice, core.Error("ATTEMPT_MISSING", choice.Checkpoint.Task)
		}
		if n.Step == "verify" {
			var check core.Check
			for _, candidate := range n.Task.Definition.Checks {
				if candidate.ID == choice.Checkpoint.Unit {
					check = candidate
					break
				}
			}
			if len(check.Command) > 0 {
				n, err = s.VerifyClaim(choice.Checkpoint.Task, check.ID, c.Runner, n.Attempt.ID, n.Attempt.Token)
			} else {
				n, err = runAttempt(s, choice.Checkpoint.Task, &c, n)
			}
		} else {
			n, err = runAttempt(s, choice.Checkpoint.Task, &c, n)
		}
		choice.Next = &n
		if err != nil {
			choice.Action, choice.StopReason, choice.Detail = "stopped", "error", err.Error()
			return choice, err
		}
		if n.Action != "continue" && n.Action != "repair" && n.Action != "completed" {
			choice.Action, choice.StopReason = "stopped", nextStopReason(n, nil)
			choice.Detail = strings.Join(n.Reasons, "; ")
			choice.Checkpoint.Step = n.Step
			return choice, nil
		}
	}
	next, err := s.NextWork(req)
	if err != nil {
		return core.WorkResult{Action: "stopped", StopReason: "error", Detail: err.Error()}, err
	}
	if next.Action == "ready" {
		next.Action, next.StopReason, next.Detail = "stopped", "max_steps", "bounded loop stopped; checkpoint saved"
	}
	return next, nil
}
