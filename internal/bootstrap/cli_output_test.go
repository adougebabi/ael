package bootstrap

import (
	"ael/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProbeBinaryRequestsExplicitJSONAndAcceptsLegacy(t *testing.T) {
	for _, tc := range []struct {
		name, script string
	}{
		{"current", `#!/bin/sh
if [ "$1" != version ] || [ "$2" != --json ]; then echo 'expected version --json' >&2; exit 1; fi
printf '%s\n' '{"version":"0.1.18","build":"new","cli_output_protocol":1}'
`},
		{"legacy", `#!/bin/sh
if [ "$1" != version ]; then exit 1; fi
printf '%s\n' '{"version":"0.1.3","build":"old"}'
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ael")
			if err := os.WriteFile(path, []byte(tc.script), 0755); err != nil {
				t.Fatal(err)
			}
			v, err := probeBinary(path)
			if err != nil || v.Version == "" {
				t.Fatalf("probe=%+v err=%v", v, err)
			}
		})
	}
}

func TestUpdateDelegationRequestsJSONWhenAdvertised(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, err := Init(root, false, false); err != nil {
		t.Fatal(err)
	}
	source := updateFixture(t, "0.2.0", "new-protocol")
	trace := filepath.Join(t.TempDir(), "arguments")
	t.Setenv("AEL_DELEGATION_TRACE", trace)
	program := []byte(`#!/bin/sh
if [ "$1" = version ]; then
  [ "$2" = --json ] || exit 2
  printf '%s\n' '{"version":"0.2.0","build":"new-protocol","cli_output_protocol":1}'
  exit 0
fi
printf '%s\n' "$@" > "$AEL_DELEGATION_TRACE"
target=''
json=no
while [ "$#" -gt 0 ]; do
 case "$1" in --project) shift; target=$1 ;; --json) json=yes ;; esac
 shift
done
[ "$json" = yes ] || { echo 'missing --json' >&2; exit 3; }
printf '{"operation":"update","status":"preview","target":"%s","available":{"version":"0.2.0","build":"new-protocol"},"files":[]}' "$target"
`)
	bundle := mustBundle(t, source)
	manifest := "version 0.2.0\n"
	for _, asset := range bundle.Assets {
		if err := os.WriteFile(filepath.Join(source, asset.Path), program, 0755); err != nil {
			t.Fatal(err)
		}
		manifest += asset.Platform + " " + store.Hash(program) + " " + asset.Path + "\n"
	}
	notice, err := os.ReadFile(filepath.Join(source, "NOTICE"))
	if err != nil {
		t.Fatal(err)
	}
	manifest += "notice " + store.Hash(notice) + " NOTICE\n"
	if err = os.WriteFile(filepath.Join(source, "ael-toolchain.lock"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	r, err := UpdateProject(root, UpdateOptions{From: source, DryRun: true})
	if err != nil || r.Status != "preview" {
		t.Fatalf("delegated update: %+v %v", r, err)
	}
	called, err := os.ReadFile(trace)
	if err != nil || !strings.Contains(string(called), "--json\n") {
		t.Fatalf("delegated arguments %q: %v", called, err)
	}
}
