package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/securityfs"
	"ael/internal/store"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
)

type runnerDeliveryMode struct {
	Path string
	Mode uint32
}

type runnerDeliveryPlan struct {
	Schema                                        int
	Journal, AttemptID, OperationID, Source       string
	BaselineCandidate, BaselineFingerprint        string
	TargetCandidate, TargetFingerprint, PatchHash string
	Modes                                         []runnerDeliveryMode
}

func runnerDeliveryKnown(candidate remote.PortableCandidate) map[string]bool {
	known := make(map[string]bool, len(candidate.Files))
	for _, file := range candidate.Files {
		known[file.Path] = true
	}
	return known
}

func runnerDeliveryCopyTree(source, destination string, candidate remote.PortableCandidate) error {
	if err := os.Mkdir(destination, 0700); err != nil {
		return err
	}
	for _, file := range candidate.Files {
		if !file.Deleted {
			if err := runnerCopyWorkspaceFile(source, destination, file.Path, file.SHA256, nil); err != nil {
				return err
			}
		}
	}
	return runnerCopyWorkspaceFile(source, destination, ".ael/knowledge-sources.json", "", nil)
}

func runnerDeliveryModes(source, snapshot string, before, after remote.PortableCandidate) ([]runnerDeliveryMode, error) {
	prior := map[string]bool{}
	for _, file := range before.Files {
		prior[file.Path] = !file.Deleted
	}
	var modes []runnerDeliveryMode
	for _, file := range after.Files {
		if file.Deleted {
			continue
		}
		target, err := store.SafePath(snapshot, filepath.FromSlash(file.Path))
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(target)
		if err != nil || !info.Mode().IsRegular() {
			return nil, core.Error("RUNNER_WORKSPACE_CHANGED", "delivery snapshot file is unavailable")
		}
		changed := !prior[file.Path]
		if !changed {
			original, err := store.SafePath(source, filepath.FromSlash(file.Path))
			if err != nil {
				return nil, err
			}
			beforeInfo, err := os.Lstat(original)
			if err != nil || !beforeInfo.Mode().IsRegular() {
				return nil, core.Error("RUNNER_WORKSPACE_CHANGED", "original delivery file is unavailable")
			}
			changed = beforeInfo.Mode().Perm() != info.Mode().Perm()
		}
		if changed {
			modes = append(modes, runnerDeliveryMode{Path: file.Path, Mode: uint32(info.Mode().Perm())})
		}
	}
	return modes, nil
}

func runnerDeliveryReadPlan(path string) (runnerDeliveryPlan, error) {
	var plan runnerDeliveryPlan
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(path) != nil || securityfs.SingleLinkRegular(path) != nil || info.Size() > 64<<20 {
		return plan, core.Error("RUNNER_RECOVERY_INVALID", "delivery plan is missing or unsafe")
	}
	f, err := os.Open(path)
	if err != nil {
		return plan, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, (64<<20)+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || decoder.Decode(new(any)) != io.EOF {
		return plan, core.Error("RUNNER_RECOVERY_INVALID", "delivery plan is invalid")
	}
	return plan, nil
}

func runnerDeliveryMarkComplete(chain *runnerWorkspaceChain) error {
	next := *chain
	next.BaselineCandidate, next.BaselineFingerprint = chain.CurrentCandidate, chain.CurrentFingerprint
	next.CurrentRelative = ""
	next.LastTaskID, next.LastAttemptID, next.LastOperation = "", "", ""
	if err := next.save(); err != nil {
		return err
	}
	*chain = next
	return nil
}

// runnerDeliverWorkspace applies a confirmed snapshot only while the original
// checkout still matches its recorded baseline. Before/after copies and the
// patch remain in the private runtime if an application is interrupted.
func runnerDeliverWorkspace(chain *runnerWorkspaceChain) error {
	if chain.Pending != nil {
		return core.Error("RUNNER_RECOVERY_REQUIRED", "a workspace result is still pending")
	}
	if chain.CurrentRelative == "" {
		return nil
	}
	snapshot, err := chain.currentWorkspace()
	if err != nil {
		return err
	}
	receiptPath, err := store.SafePath(chain.runtime, filepath.Join("control", "attempts", chain.LastAttemptID, "receipt.json"))
	if err != nil {
		return core.Error("RUNNER_RECOVERY_INVALID", "workspace receipt path is unsafe")
	}
	receiptBytes, err := readRunnerPrivateFile(receiptPath)
	if err != nil {
		return core.Error("RUNNER_RECOVERY_REQUIRED", "confirmed workspace receipt is unavailable")
	}
	var receipt map[string]any
	if json.Unmarshal(receiptBytes, &receipt) != nil || receipt == nil {
		return core.Error("RUNNER_RECOVERY_INVALID", "confirmed workspace receipt is invalid")
	}
	cfg := remoteRunnerConfig{RunnerID: chain.RunnerID, Repository: chain.Repository, CheckoutID: chain.CheckoutID, WorkspaceID: chain.WorkspaceID, RuntimeDir: chain.runtime, Workspace: chain.Source}
	binding := remote.Binding{Schema: 1, Profile: chain.Profile, ProjectID: chain.ProjectID}
	if err = runnerAcceptWorkspaceReceipt(cfg, chain.Source, binding, chain.LastAttemptID, receipt); err != nil {
		return err
	}
	current, currentFingerprint, err := runnerWorkspaceFingerprint(chain.Source, chain.Repository)
	if err != nil {
		return core.Error("RUNNER_RECOVERY_REQUIRED", "original checkout could not be verified; retain the accepted snapshot")
	}
	if current.ID == chain.CurrentCandidate && currentFingerprint == chain.CurrentFingerprint {
		return runnerDeliveryMarkComplete(chain)
	}
	if current.ID != chain.BaselineCandidate || currentFingerprint != chain.BaselineFingerprint {
		return core.Error("RUNNER_WORKSPACE_CHANGED", "original checkout changed; preserve it and inspect the accepted snapshot at "+snapshot)
	}
	target, targetFingerprint, err := runnerWorkspaceFingerprint(snapshot, chain.Repository)
	if err != nil || target.ID != chain.CurrentCandidate || targetFingerprint != chain.CurrentFingerprint {
		return core.Error("RUNNER_WORKSPACE_CHANGED", "accepted snapshot changed before delivery")
	}
	if current.Commit != target.Commit {
		return core.Error("RUNNER_RECOVERY_REQUIRED", "accepted snapshot changed Git HEAD; inspect it before delivery")
	}
	beforeStage, beforeFlags, _, err := runnerWorkspaceIndex(chain.Source, runnerDeliveryKnown(current))
	if err != nil {
		return err
	}
	afterStage, afterFlags, _, err := runnerWorkspaceIndex(snapshot, runnerDeliveryKnown(target))
	if err != nil {
		return err
	}
	if !bytes.Equal(beforeStage, afterStage) || !bytes.Equal(beforeFlags, afterFlags) {
		return core.Error("RUNNER_RECOVERY_REQUIRED", "accepted snapshot changed the Git index; preserve both workspaces for inspection")
	}
	beforeOptions, err := runnerWorkspaceOptions(chain.Source)
	if err != nil {
		return err
	}
	afterOptions, err := runnerWorkspaceOptions(snapshot)
	if err != nil {
		return err
	}
	if !slices.Equal(beforeOptions, afterOptions) {
		return core.Error("RUNNER_RECOVERY_REQUIRED", "accepted snapshot changed Git conversion settings")
	}
	root, err := runnerPrivateDir(chain.runtime, "control", "deliveries")
	if err != nil {
		return err
	}
	dir := filepath.Join(root, chain.LastAttemptID)
	planPath := filepath.Join(dir, "delivery.json")
	patchPath := filepath.Join(dir, "changes.patch")
	var plan runnerDeliveryPlan
	if _, err = os.Lstat(dir); os.IsNotExist(err) {
		if err = os.Mkdir(dir, 0700); err != nil {
			return err
		}
		if err = runnerDeliveryCopyTree(chain.Source, filepath.Join(dir, "before"), current); err != nil {
			return core.Error("RUNNER_RECOVERY_REQUIRED", "cannot preserve checkout before delivery; inspect "+dir)
		}
		if err = runnerDeliveryCopyTree(snapshot, filepath.Join(dir, "after"), target); err != nil {
			return core.Error("RUNNER_RECOVERY_REQUIRED", "cannot preserve accepted output before delivery; inspect "+dir)
		}
		modes, err := runnerDeliveryModes(chain.Source, snapshot, current, target)
		if err != nil {
			return err
		}
		patch, diffErr := runnerWorkspaceGit(dir, nil, "diff", "--no-index", "--binary", "--no-renames", "--", "before", "after")
		if diffErr != nil {
			var exit *exec.ExitError
			if !errors.As(diffErr, &exit) || exit.ExitCode() != 1 {
				return core.Error("RUNNER_RECOVERY_REQUIRED", "cannot prepare delivery patch; inspect "+dir)
			}
		}
		if err = store.Atomic(patchPath, patch, 0600); err != nil {
			return err
		}
		plan = runnerDeliveryPlan{Schema: 1, Journal: filepath.Base(chain.path), AttemptID: chain.LastAttemptID, OperationID: chain.LastOperation, Source: chain.Source, BaselineCandidate: chain.BaselineCandidate, BaselineFingerprint: chain.BaselineFingerprint, TargetCandidate: chain.CurrentCandidate, TargetFingerprint: chain.CurrentFingerprint, PatchHash: store.Hash(patch), Modes: modes}
		raw, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		if err = store.Atomic(planPath, raw, 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		plan, err = runnerDeliveryReadPlan(planPath)
		if err != nil {
			return core.Error("RUNNER_RECOVERY_REQUIRED", "delivery preparation is incomplete; inspect "+dir)
		}
	}
	if plan.Schema != 1 || plan.Journal != filepath.Base(chain.path) || plan.AttemptID != chain.LastAttemptID || plan.OperationID != chain.LastOperation || plan.Source != chain.Source || plan.BaselineCandidate != chain.BaselineCandidate || plan.BaselineFingerprint != chain.BaselineFingerprint || plan.TargetCandidate != chain.CurrentCandidate || plan.TargetFingerprint != chain.CurrentFingerprint || !runnerWorkspaceHash(plan.PatchHash) {
		return core.Error("RUNNER_RECOVERY_INVALID", "delivery plan and workspace journal disagree")
	}
	patchHash, err := store.HashFile(patchPath)
	if err != nil || patchHash != plan.PatchHash {
		return core.Error("RUNNER_RECOVERY_INVALID", "delivery patch changed")
	}
	current, currentFingerprint, err = runnerWorkspaceFingerprint(chain.Source, chain.Repository)
	if err != nil || current.ID != chain.BaselineCandidate || currentFingerprint != chain.BaselineFingerprint {
		return core.Error("RUNNER_WORKSPACE_CHANGED", "original checkout changed before delivery; inspect "+dir)
	}
	patchInfo, err := os.Stat(patchPath)
	if err != nil {
		return err
	}
	if patchInfo.Size() > 0 {
		if _, err = runnerWorkspaceGit(chain.Source, nil, "apply", "--check", "-p2", "--", patchPath); err != nil {
			return core.Error("RUNNER_WORKSPACE_CHANGED", "delivery patch no longer applies; inspect "+dir)
		}
		if _, err = runnerWorkspaceGit(chain.Source, nil, "apply", "--binary", "-p2", "--", patchPath); err != nil {
			return core.Error("RUNNER_RECOVERY_REQUIRED", "delivery may be incomplete; inspect retained before/after copies at "+dir)
		}
	}
	for _, mode := range plan.Modes {
		path, err := store.SafePath(chain.Source, filepath.FromSlash(mode.Path))
		if err != nil {
			return core.Error("RUNNER_RECOVERY_REQUIRED", "delivery path changed; inspect "+dir)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || mode.Mode&^0777 != 0 {
			return core.Error("RUNNER_RECOVERY_REQUIRED", "delivery permissions need inspection at "+dir)
		}
		if err = os.Chmod(path, os.FileMode(mode.Mode)); err != nil {
			return core.Error("RUNNER_RECOVERY_REQUIRED", "delivery permissions could not be applied; inspect "+dir)
		}
	}
	current, currentFingerprint, err = runnerWorkspaceFingerprint(chain.Source, chain.Repository)
	if err != nil || current.ID != chain.CurrentCandidate || currentFingerprint != chain.CurrentFingerprint {
		return core.Error("RUNNER_RECOVERY_REQUIRED", "delivery is incomplete or the checkout changed; inspect "+dir)
	}
	return runnerDeliveryMarkComplete(chain)
}
