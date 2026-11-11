package core

import (
	"ael/internal/store"
	"bytes"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type CandidateFile struct {
	Mode uint32 `json:"mode"`
	Hash string `json:"hash"`
}

type CandidateChange struct {
	Path   string         `json:"path"`
	Before *CandidateFile `json:"before,omitempty"`
	After  *CandidateFile `json:"after,omitempty"`
}

type CandidateProof struct {
	Input            string                   `json:"input"`
	InputHead        string                   `json:"input_head,omitempty"`
	Candidate        string                   `json:"candidate"`
	Head             string                   `json:"head,omitempty"`
	LocalRepository  string                   `json:"local_repository,omitempty"`
	Changes          []CandidateChange        `json:"changes"`
	UnboundArtifacts []string                 `json:"unbound_artifacts,omitempty"`
	Exclusions       []string                 `json:"exclusions,omitempty"`
	Files            map[string]CandidateFile `json:"files"`
}

type CandidateBaseline struct {
	Fingerprint      string                   `json:"fingerprint"`
	Head             string                   `json:"head,omitempty"`
	LocalRepository  string                   `json:"local_repository,omitempty"`
	Files            map[string]CandidateFile `json:"files"`
	Exclusions       []string                 `json:"exclusions,omitempty"`
	PolicyExclusions []string                 `json:"policy_exclusions,omitempty"`
	Unknown          bool                     `json:"unknown,omitempty"`
	UnboundArtifacts []string                 `json:"unbound_artifacts,omitempty"`
}

type candidateSnapshot struct {
	Fingerprint      string
	Head             string
	Files            map[string]CandidateFile
	Exclusions       []string
	PolicyExclusions []string
}

// Fingerprint remains conservative. All regular files, including untracked
// files, are inputs; the snapshot is also used to prove actual leaf changes.
func Fingerprint(root string) (string, error) {
	snapshot, err := captureCandidate(root)
	return snapshot.Fingerprint, err
}

func captureCandidate(root string) (candidateSnapshot, error) {
	type input struct {
		Path string
		Mode uint32
		Hash string
	}
	var policy struct {
		Exclude []string `yaml:"candidate_exclude"`
	}
	if b, e := os.ReadFile(filepath.Join(root, ".ael/policy.yaml")); e == nil {
		if e = yaml.Unmarshal(b, &policy); e != nil {
			return candidateSnapshot{}, e
		}
	} else if !os.IsNotExist(e) {
		return candidateSnapshot{}, e
	}
	for _, path := range policy.Exclude {
		clean := filepath.ToSlash(filepath.Clean(path))
		if filepath.IsAbs(path) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean == ".ael" || strings.HasPrefix(clean, ".ael/") || clean == "AGENTS.md" {
			return candidateSnapshot{}, Error("INVALID_CANDIDATE_EXCLUDE", path)
		}
	}
	var inputs []input
	skipped := []string{}
	e := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		if rel == "." {
			return nil
		}
		for _, excluded := range policy.Exclude {
			excluded = filepath.ToSlash(filepath.Clean(excluded))
			inputPath := filepath.ToSlash(rel)
			if inputPath == excluded || strings.HasPrefix(inputPath, excluded+"/") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" || rel == ".ael/work-items" || rel == ".ael/outcomes" || rel == ".ael/records" || rel == ".ael/deleted" || rel == ".ael/imports" || rel == ".ael/bin" || rel == ".ael/tool-update.lock" {
				skipped = append(skipped, filepath.ToSlash(rel))
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == ".DS_Store" {
			skipped = append(skipped, filepath.ToSlash(rel))
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return Error("SYMLINK_INPUT", rel)
		}
		if !d.Type().IsRegular() {
			return Error("UNSUPPORTED_INPUT", rel)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if filepath.ToSlash(rel) == ".ael/policy.yaml" {
			var parsed any
			if e = yaml.Unmarshal(b, &parsed); e != nil {
				return e
			}
			b, e = json.Marshal(parsed)
			if e != nil {
				return e
			}
		}
		st, e := d.Info()
		if e != nil {
			return e
		}
		inputs = append(inputs, input{filepath.ToSlash(rel), uint32(st.Mode().Perm()), store.Hash(b)})
		return nil
	})
	if e != nil {
		return candidateSnapshot{}, e
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Path < inputs[j].Path })
	b, _ := json.Marshal(inputs)
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	head, _ := cmd.Output()
	files := make(map[string]CandidateFile, len(inputs))
	for _, item := range inputs {
		files[item.Path] = CandidateFile{Mode: item.Mode, Hash: item.Hash}
	}
	policyExclusions := append([]string{}, policy.Exclude...)
	for i := range policyExclusions {
		policyExclusions[i] = filepath.ToSlash(filepath.Clean(policyExclusions[i]))
	}
	sort.Strings(policyExclusions)
	exclusions := append(append([]string{}, policyExclusions...), skipped...)
	sort.Strings(exclusions)
	headText := string(bytes.TrimSpace(head))
	return candidateSnapshot{Fingerprint: store.Hash(bytes.Join([][]byte{[]byte(headText), b}, []byte("\n"))), Head: headText, Files: files, Exclusions: exclusions, PolicyExclusions: policyExclusions}, nil
}

func fingerprintCandidateFiles(files map[string]CandidateFile, head string) string {
	type input struct {
		Path string
		Mode uint32
		Hash string
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var inputs []input
	for _, path := range paths {
		entry := files[path]
		inputs = append(inputs, input{Path: path, Mode: entry.Mode, Hash: entry.Hash})
	}
	b, _ := json.Marshal(inputs)
	return store.Hash(bytes.Join([][]byte{[]byte(head), b}, []byte("\n")))
}

func gitCommand(root string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "GIT_") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	return cmd
}

// Local repository identity deliberately describes only this host's Git
// common directory. It is not a remote or cross-device repository identity.
func localGitRepository(root, expectedHead string) (string, error) {
	if expectedHead == "" {
		if _, err := exec.LookPath("git"); err != nil {
			return "", Error("EPIC_GIT_IDENTITY_UNAVAILABLE", "Git executable is unavailable")
		}
		if head, err := gitCommand(root, "rev-parse", "--verify", "HEAD").Output(); err == nil && len(bytes.TrimSpace(head)) > 0 {
			return "", Error("EPIC_GIT_IDENTITY_UNAVAILABLE", "Git exists but the candidate HEAD was not captured")
		}
		if inside, err := gitCommand(root, "rev-parse", "--is-inside-work-tree").Output(); err == nil && string(bytes.TrimSpace(inside)) == "true" {
			return "", Error("EPIC_GIT_IDENTITY_UNAVAILABLE", "Git workspace has no verifiable commit")
		}
		if _, err := os.Lstat(filepath.Join(root, ".git")); err == nil {
			return "", Error("EPIC_GIT_IDENTITY_UNAVAILABLE", "Git metadata exists without a verifiable commit")
		} else if !os.IsNotExist(err) {
			return "", err
		}
		return "", nil
	}
	head, err := gitCommand(root, "rev-parse", "--verify", "HEAD").Output()
	if err != nil || string(bytes.TrimSpace(head)) != expectedHead {
		return "", Error("EPIC_GIT_IDENTITY_UNAVAILABLE", "cannot confirm workspace HEAD")
	}
	raw, err := gitCommand(root, "rev-parse", "--git-common-dir").Output()
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return "", Error("EPIC_GIT_IDENTITY_UNAVAILABLE", "cannot confirm local repository")
	}
	path := string(bytes.TrimSpace(raw))
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", Error("EPIC_GIT_IDENTITY_UNAVAILABLE", "cannot resolve local repository")
	}
	return store.Hash([]byte("local-git:" + path)), nil
}

func gitContainsCommit(root, ancestor, candidate string) bool {
	if ancestor == "" || candidate == "" {
		return false
	}
	for _, value := range []string{ancestor, candidate} {
		if !validGitObjectID(value) {
			return false
		}
	}
	return gitCommand(root, "merge-base", "--is-ancestor", ancestor, candidate).Run() == nil
}

func validGitObjectID(value string) bool {
	return len(value) >= 40 && len(value) <= 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func candidateChanges(before, after map[string]CandidateFile) []CandidateChange {
	paths := map[string]bool{}
	for path := range before {
		paths[path] = true
	}
	for path := range after {
		paths[path] = true
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	changes := []CandidateChange{}
	for _, path := range ordered {
		old, had := before[path]
		current, has := after[path]
		if had && has && old == current {
			continue
		}
		change := CandidateChange{Path: path}
		if had {
			copy := old
			change.Before = &copy
		}
		if has {
			copy := current
			change.After = &copy
		}
		changes = append(changes, change)
	}
	return changes
}

func sameCandidateFile(a, b *CandidateFile) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func uniqueSorted(values ...[]string) []string {
	seen := map[string]bool{}
	for _, group := range values {
		for _, value := range group {
			seen[value] = true
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func definitionBytes(d Definition) []byte {
	b, _ := json.MarshalIndent(d, "", "  ")
	return []byte("---\n" + string(b) + "\n---\n# " + d.Title + "\n")
}
func readDefinition(path string) (Definition, []byte, error) {
	var d Definition
	b, e := os.ReadFile(path)
	if e != nil {
		return d, nil, e
	}
	parts := strings.SplitN(strings.TrimPrefix(string(b), "---\n"), "\n---\n", 2)
	if !strings.HasPrefix(string(b), "---\n") || len(parts) != 2 {
		return d, nil, Error("INVALID_TASK_FILE", path)
	}
	e = json.Unmarshal([]byte(strings.TrimSpace(parts[0])), &d)
	return d, b, e
}

func DefinitionDocument(d Definition) []byte { return definitionBytes(d) }
