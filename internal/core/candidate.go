package core

import (
	"ael/internal/store"
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Fingerprint is conservative. All regular files, including untracked files, are inputs.
func Fingerprint(root string) (string, error) {
	type input struct {
		Path string
		Mode uint32
		Hash string
	}
	var inputs []input
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
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" || rel == ".ael/work-items" || rel == ".ael/outcomes" || rel == ".ael/imports" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == ".DS_Store" {
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
		st, e := d.Info()
		if e != nil {
			return e
		}
		inputs = append(inputs, input{filepath.ToSlash(rel), uint32(st.Mode().Perm()), store.Hash(b)})
		return nil
	})
	if e != nil {
		return "", e
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Path < inputs[j].Path })
	b, _ := json.Marshal(inputs)
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	head, _ := cmd.Output()
	return store.Hash(bytes.Join([][]byte{bytes.TrimSpace(head), b}, []byte("\n"))), nil
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
