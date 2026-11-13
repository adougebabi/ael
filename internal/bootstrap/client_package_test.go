package bootstrap

import (
	"ael/internal/release"
	"ael/internal/store"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func clientPackageFixture(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	body := []byte(fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '{\"version\":\"%s\",\"build\":\"client-fixture\",\"cli_output_protocol\":1,\"api_protocol\":1,\"skill_protocol\":1}'\n", version))
	metadata := release.ClientPackage{Version: version, Build: "client-fixture", Commit: strings.Repeat("a", 40), Kind: "client", Platform: runtime.GOOS + "-" + runtime.GOARCH, Protocols: release.Protocols{API: 1, Skill: 1, CLI: 1}}
	for name, contents := range map[string][]byte{"ael": body, "THIRD_PARTY_NOTICES.txt": []byte("fixture notice"), "README.md": []byte("fixture client")} {
		os.WriteFile(filepath.Join(root, name), contents, 0755)
		metadata.Files = append(metadata.Files, release.PackageFile{Path: name, Size: int64(len(contents)), SHA256: store.Hash(contents)})
	}
	raw, _ := json.Marshal(metadata)
	os.WriteFile(filepath.Join(root, "package.json"), raw, 0644)
	return root
}

func TestClientUpgradeReusesTransactionAndPersistentCache(t *testing.T) {
	source := clientPackageFixture(t, Version)
	prefix := filepath.Join(t.TempDir(), "installation")
	result, err := Upgrade(UpdateOptions{From: source, Prefix: prefix, DryRun: true, TargetVersion: Version})
	if err != nil || result.Status != "preview" {
		t.Fatalf("preview %+v %v", result, err)
	}
	if _, err := os.Stat(prefix); !os.IsNotExist(err) {
		t.Fatal("preview changed prefix")
	}
	if _, err := Upgrade(UpdateOptions{From: source, Prefix: prefix, TargetVersion: "99.0.0"}); err == nil {
		t.Fatal("accepted wrong requested version")
	}
	result, err = Upgrade(UpdateOptions{From: source, Prefix: prefix})
	if err != nil || result.Status != "updated" {
		t.Fatalf("install %+v %v", result, err)
	}
	installed, err := readInstallation(prefix)
	if err != nil || !strings.Contains(installed.Source, "/share/ael/clients/") {
		t.Fatalf("transient source registered %+v %v", installed, err)
	}
	os.RemoveAll(source)
	result, err = Upgrade(UpdateOptions{Prefix: prefix})
	if err != nil || result.Status != "up_to_date" {
		t.Fatalf("cached reuse %+v %v", result, err)
	}
	if _, err := sourceBundle(UpdateOptions{Prefix: prefix}, false); err == nil {
		t.Fatal("single platform package became Local project source")
	}
	binary := filepath.Join(prefix, "bin/ael")
	before, _ := store.HashFile(binary)
	corrupt := clientPackageFixture(t, "99.0.0")
	os.WriteFile(filepath.Join(corrupt, "ael"), []byte("corrupt"), 0755)
	if _, err := Upgrade(UpdateOptions{From: corrupt, Prefix: prefix}); err == nil {
		t.Fatal("accepted corrupt source")
	}
	after, _ := store.HashFile(binary)
	if before != after {
		t.Fatal("bad package changed installed client")
	}
}

func TestClientPackageRejectsExecutableProtocolMismatch(t *testing.T) {
	for _, field := range []string{"api_protocol", "skill_protocol", "cli_output_protocol"} {
		t.Run(field, func(t *testing.T) {
			source := clientPackageFixture(t, Version)
			path := filepath.Join(source, "ael")
			body, _ := os.ReadFile(path)
			body = []byte(strings.ReplaceAll(string(body), `"`+field+`":1`, `"`+field+`":2`))
			os.WriteFile(path, body, 0755)
			metadata, _, err := release.ReadClientPackage(source)
			if err == nil {
				t.Fatal("changed executable unexpectedly matched old hashes")
			}
			raw, _ := os.ReadFile(filepath.Join(source, "package.json"))
			json.Unmarshal(raw, &metadata)
			for index := range metadata.Files {
				if metadata.Files[index].Path == "ael" {
					metadata.Files[index].Size = int64(len(body))
					metadata.Files[index].SHA256 = store.Hash(body)
				}
			}
			raw, _ = json.Marshal(metadata)
			os.WriteFile(filepath.Join(source, "package.json"), raw, 0644)
			prefix := t.TempDir()
			if _, err := Upgrade(UpdateOptions{From: source, Prefix: prefix}); err == nil {
				t.Fatal("installed executable contradicting the declared client protocol")
			}
			if _, err := os.Stat(filepath.Join(prefix, "bin/ael")); !os.IsNotExist(err) {
				t.Fatal("protocol failure changed target installation")
			}
		})
	}
}

func TestClientUpgradeRetainsDefaultLocalDistribution(t *testing.T) {
	prefix := t.TempDir()
	old := updateFixture(t, "0.1.18", "old-full")
	if _, err := Upgrade(UpdateOptions{From: old, Prefix: prefix}); err != nil {
		t.Fatal(err)
	}
	before, err := sourceBundle(UpdateOptions{Prefix: prefix}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Upgrade(UpdateOptions{From: clientPackageFixture(t, Version), Prefix: prefix}); err != nil {
		t.Fatal(err)
	}
	retained, err := sourceBundle(UpdateOptions{Prefix: prefix}, false)
	if err != nil || retained.Version != before.Version || retained.Root != before.Root {
		t.Fatalf("single-platform upgrade lost default Local source: %+v %v", retained, err)
	}
	if result, err := Upgrade(UpdateOptions{Prefix: prefix}); err != nil || result.Status != "up_to_date" {
		t.Fatalf("retained Local source broke machine no-op %+v %v", result, err)
	}
}

func TestFullDistributionWithAdditionalPackageJSONRemainsSupported(t *testing.T) {
	source := updateFixture(t, Version, "full-with-extra-metadata")
	os.WriteFile(filepath.Join(source, "package.json"), []byte(`{"name":"unrelated-package-metadata"}`), 0644)
	if result, err := Upgrade(UpdateOptions{From: source, Prefix: t.TempDir()}); err != nil || result.Status != "updated" {
		t.Fatalf("valid legacy lock was misclassified %+v %v", result, err)
	}
}
