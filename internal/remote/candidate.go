package remote

import (
	"ael/internal/store"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// PortableCandidate identifies repository content, not a machine, checkout,
// workspace, branch name or runner. Only hashes and paths are transmitted.
type PortableCandidate struct {
	Schema     int            `json:"schema"`
	Repository string         `json:"repository"`
	Commit     string         `json:"commit"`
	Files      []PortableFile `json:"files"`
	ID         string         `json:"id"`
	// Local-only completeness signal. It is attached to an observation, not
	// the content ID, so ignored logs do not change the code candidate.
	InputsKnown bool `json:"-"`
	// Ordinary filesystem reads cannot prove that an external writer remained
	// stopped after the last read. A content observation is not a lease.
	StabilityProven bool `json:"-"`
}

type PortableFile struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256,omitempty"`
	Index      string `json:"index,omitempty"`
	Executable bool   `json:"executable,omitempty"`
	Deleted    bool   `json:"deleted,omitempty"`
}

type PortableDiff struct {
	RepositoryChanged bool     `json:"repository_changed"`
	CommitChanged     bool     `json:"commit_changed"`
	Paths             []string `json:"paths"`
	ExactlyEqual      bool     `json:"exactly_equal"`
}

var portableDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var portableCommit = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

func portableRuntimePath(path string) bool {
	if path == ".ael/remote.json" || path == ".ael/knowledge-sources.json" || path == ".ael/tool-update.lock" {
		return true
	}
	for _, prefix := range []string{".ael/work-items/", ".ael/outcomes/", ".ael/records/", ".ael/deleted/", ".ael/imports/", ".ael/bin/", ".ael/tool-update.lock/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func portableSensitivePath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}
	for _, name := range []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "credentials", "credentials.json", "service-account.json", "token", "token.json", ".npmrc", ".pypirc"} {
		if base == name {
			return true
		}
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".keystore"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

func portablePathValid(path string) bool {
	if path == "" || strings.ContainsAny(path, "\\\x00\r\n") || strings.HasPrefix(path, "/") || path == "." || strings.HasPrefix(path, "../") {
		return false
	}
	if filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) != path {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".git" || part == ".." || part == "" {
			return false
		}
	}
	return true
}

func portableGit(root string, args ...string) ([]byte, error) {
	options := append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull}, args...)
	cmd := exec.Command("git", options...)
	cmd.Dir = root
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	return cmd.Output()
}

func portableCandidateID(candidate PortableCandidate) string {
	body, _ := json.Marshal(struct {
		Schema     int            `json:"schema"`
		Repository string         `json:"repository"`
		Commit     string         `json:"commit"`
		Files      []PortableFile `json:"files"`
	}{candidate.Schema, candidate.Repository, candidate.Commit, candidate.Files})
	return store.Hash(body)
}

// ValidatePortableCandidate lets the server reject forged identifiers,
// ambiguous paths, excluded runtime content and unsorted manifests.
func ValidatePortableCandidate(candidate PortableCandidate) error {
	if candidate.Schema != 1 || !identifier.MatchString(candidate.Repository) || !portableCommit.MatchString(candidate.Commit) || len(candidate.Files) > 200000 || candidate.Files == nil {
		return errors.New("CANDIDATE_INVALID")
	}
	previous := ""
	for _, file := range candidate.Files {
		if !portablePathValid(file.Path) || portableRuntimePath(file.Path) || portableSensitivePath(file.Path) || file.Path <= previous {
			return errors.New("CANDIDATE_INVALID")
		}
		if file.Deleted {
			if file.SHA256 != "" || file.Executable {
				return errors.New("CANDIDATE_INVALID")
			}
		} else if !portableDigest.MatchString(file.SHA256) {
			return errors.New("CANDIDATE_INVALID")
		}
		if file.Index != "" && file.Index != "removed" && !portableCommit.MatchString(file.Index) {
			return errors.New("CANDIDATE_INVALID")
		}
		previous = file.Path
	}
	if candidate.ID != portableCandidateID(candidate) {
		return errors.New("CANDIDATE_INVALID")
	}
	return nil
}

func portableInputPaths(root string) ([]string, bool, error) {
	tracked, err := portableGit(root, "ls-tree", "-r", "-z", "--name-only", "HEAD")
	if err != nil {
		return nil, false, errors.New("CANDIDATE_GIT_UNAVAILABLE: cannot list committed files")
	}
	workspace, err := portableGit(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--")
	if err != nil {
		return nil, false, errors.New("CANDIDATE_GIT_UNAVAILABLE: cannot list workspace inputs")
	}
	names := map[string]bool{}
	add := func(path string) error {
		if !portablePathValid(path) {
			return errors.New("CANDIDATE_UNSAFE_PATH")
		}
		if portableRuntimePath(path) {
			return nil
		}
		if portableSensitivePath(path) {
			return fmt.Errorf("CANDIDATE_SENSITIVE_INPUT: exclude or move %s before capture", path)
		}
		names[path] = true
		return nil
	}
	for _, group := range [][]byte{tracked, workspace} {
		for _, raw := range bytes.Split(group, []byte{0}) {
			if len(raw) > 0 {
				if err = add(string(raw)); err != nil {
					return nil, false, err
				}
			}
		}
	}
	// Policy and local knowledge can be Git-ignored yet still drive checks.
	policy := filepath.Join(root, ".ael", "policy.yaml")
	if _, err = os.Lstat(policy); err == nil {
		if err = add(".ael/policy.yaml"); err != nil {
			return nil, false, err
		}
	} else if !os.IsNotExist(err) {
		return nil, false, err
	}
	knowledgeRoot := filepath.Join(root, ".ael", "knowledge")
	err = filepath.WalkDir(knowledgeRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) && path == knowledgeRoot {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("CANDIDATE_UNSAFE_INPUT: linked knowledge source")
		}
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			return add(filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	ignored, err := portableGit(root, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory", "--")
	if err != nil {
		return nil, false, errors.New("CANDIDATE_GIT_UNAVAILABLE: cannot inspect ignored inputs")
	}
	unknown := false
	for _, raw := range bytes.Split(ignored, []byte{0}) {
		path := string(raw)
		if path == "" || path == ".ael/" || portableRuntimePath(path) {
			continue
		}
		unknown = true
	}
	// If Git collapses an ignored .ael directory, distinguish known runtime
	// files from other unaccounted local inputs without uploading their names.
	aelRoot := filepath.Join(root, ".ael")
	err = filepath.WalkDir(aelRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) && path == aelRoot {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".ael" {
			return nil
		}
		if portableRuntimePath(rel) || portableRuntimePath(rel+"/") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == ".ael/knowledge" || rel == ".ael/policy.yaml" {
			return nil
		}
		if strings.HasPrefix(rel, ".ael/knowledge/") {
			if !entry.IsDir() && !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
				unknown = true
			}
			return nil
		}
		if entry.IsDir() {
			unknown = true
			return filepath.SkipDir
		}
		unknown = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	ordered := make([]string, 0, len(names))
	for path := range names {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	if len(ordered) > 200000 {
		return nil, false, errors.New("CANDIDATE_TOO_LARGE")
	}
	return ordered, unknown, nil
}

func portableTrackedModes(root string) (map[string]bool, map[string]string, bool, error) {
	config, err := portableGit(root, "config", "--bool", "--get", "core.filemode")
	filemode := true
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return nil, nil, false, errors.New("CANDIDATE_GIT_UNAVAILABLE: cannot inspect file mode")
		}
	} else {
		switch strings.TrimSpace(string(config)) {
		case "true":
		case "false":
			filemode = false
		default:
			return nil, nil, false, errors.New("CANDIDATE_GIT_UNAVAILABLE: invalid file mode")
		}
	}
	modes := map[string]bool{}
	index := map[string]string{}
	for stage, command := range [][]string{{"ls-tree", "-r", "-z", "HEAD"}, {"ls-files", "--stage", "-z", "--"}} {
		data, err := portableGit(root, command...)
		if err != nil {
			return nil, nil, false, errors.New("CANDIDATE_GIT_UNAVAILABLE: cannot inspect tracked modes")
		}
		for _, raw := range bytes.Split(data, []byte{0}) {
			if len(raw) == 0 {
				continue
			}
			parts := bytes.SplitN(raw, []byte{'\t'}, 2)
			if len(parts) != 2 {
				return nil, nil, false, errors.New("CANDIDATE_GIT_UNAVAILABLE: malformed tracked mode")
			}
			fields := strings.Fields(string(parts[0]))
			if len(fields) < 2 {
				return nil, nil, false, errors.New("CANDIDATE_GIT_UNAVAILABLE: malformed tracked mode")
			}
			path := string(parts[1])
			if stage == 0 {
				index[path] = "removed"
			} else {
				if !portableCommit.MatchString(fields[1]) {
					return nil, nil, false, errors.New("CANDIDATE_GIT_UNAVAILABLE: malformed index object")
				}
				index[path] = fields[1]
			}
			if fields[0] == "100644" || fields[0] == "100755" {
				modes[path] = fields[0] == "100755"
			}
		}
	}
	return modes, index, filemode, nil
}

func CapturePortableCandidate(root, repository string) (PortableCandidate, error) {
	first, err := capturePortableCandidateOnce(root, repository)
	if err != nil {
		return PortableCandidate{}, err
	}
	second, err := capturePortableCandidateOnce(root, repository)
	if err != nil {
		return PortableCandidate{}, err
	}
	if first.ID != second.ID {
		return PortableCandidate{}, errors.New("CANDIDATE_CHANGED_DURING_CAPTURE")
	}
	second.InputsKnown = first.InputsKnown && second.InputsKnown
	return second, nil
}

func capturePortableCandidateOnce(root, repository string) (PortableCandidate, error) {
	var result PortableCandidate
	if !identifier.MatchString(repository) {
		return result, errors.New("REPOSITORY_ID_REQUIRED")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return result, errors.New("CANDIDATE_WORKSPACE_UNAVAILABLE")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return result, errors.New("CANDIDATE_WORKSPACE_UNAVAILABLE")
	}
	top, err := portableGit(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return result, errors.New("CANDIDATE_GIT_UNAVAILABLE: a verified Git checkout is required")
	}
	topPath, err := filepath.EvalSymlinks(strings.TrimSpace(string(top)))
	if err != nil || topPath != root {
		return result, errors.New("CANDIDATE_WORKSPACE_MISMATCH: select the Git checkout root")
	}
	head, err := portableGit(root, "rev-parse", "--verify", "HEAD^{commit}")
	commit := strings.TrimSpace(string(head))
	if err != nil || !portableCommit.MatchString(commit) {
		return result, errors.New("CANDIDATE_GIT_UNAVAILABLE: cannot verify HEAD")
	}
	conflicts, err := portableGit(root, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil || len(conflicts) != 0 {
		return result, errors.New("CANDIDATE_GIT_CONFLICT: resolve unmerged files first")
	}
	ordered, unknown, err := portableInputPaths(root)
	if err != nil {
		return result, err
	}
	modes, index, filemode, err := portableTrackedModes(root)
	if err != nil {
		return result, err
	}
	files := make([]PortableFile, 0, len(ordered))
	for _, path := range ordered {
		full, err := store.SafePath(root, filepath.FromSlash(path))
		if err != nil {
			return result, fmt.Errorf("CANDIDATE_UNSAFE_INPUT: %s", path)
		}
		before, err := os.Lstat(full)
		if os.IsNotExist(err) {
			files = append(files, PortableFile{Path: path, Index: index[path], Deleted: true})
			continue
		}
		if err != nil || !before.Mode().IsRegular() {
			return result, fmt.Errorf("CANDIDATE_UNSAFE_INPUT: %s", path)
		}
		hash, err := store.HashFile(full)
		if err != nil {
			return result, fmt.Errorf("CANDIDATE_INPUT_UNAVAILABLE: %s", path)
		}
		after, err := os.Lstat(full)
		if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
			return result, fmt.Errorf("CANDIDATE_INPUT_CHANGED: %s", path)
		}
		executable := before.Mode().Perm()&0111 != 0
		if trackedMode, ok := modes[path]; ok && !filemode {
			executable = trackedMode
		}
		files = append(files, PortableFile{Path: path, SHA256: hash, Index: index[path], Executable: executable})
	}
	lastHead, headErr := portableGit(root, "rev-parse", "--verify", "HEAD^{commit}")
	lastPaths, lastUnknown, pathsErr := portableInputPaths(root)
	if headErr != nil || pathsErr != nil || strings.TrimSpace(string(lastHead)) != commit || !slices.Equal(ordered, lastPaths) {
		return result, errors.New("CANDIDATE_CHANGED_DURING_CAPTURE")
	}
	result = PortableCandidate{Schema: 1, Repository: repository, Commit: commit, Files: files, InputsKnown: !unknown && !lastUnknown && filemode}
	result.ID = portableCandidateID(result)
	return result, ValidatePortableCandidate(result)
}

func ComparePortableCandidates(before, after PortableCandidate) PortableDiff {
	valid := ValidatePortableCandidate(before) == nil && ValidatePortableCandidate(after) == nil
	result := PortableDiff{RepositoryChanged: before.Repository != after.Repository, CommitChanged: before.Commit != after.Commit, Paths: []string{}, ExactlyEqual: valid && before.InputsKnown && after.InputsKnown && before.StabilityProven && after.StabilityProven && before.ID == after.ID}
	a, b := map[string]PortableFile{}, map[string]PortableFile{}
	for _, f := range before.Files {
		a[f.Path] = f
	}
	for _, f := range after.Files {
		b[f.Path] = f
	}
	seen := map[string]bool{}
	for path := range a {
		seen[path] = true
	}
	for path := range b {
		seen[path] = true
	}
	for path := range seen {
		if a[path] != b[path] {
			result.Paths = append(result.Paths, path)
		}
	}
	sort.Strings(result.Paths)
	return result
}
