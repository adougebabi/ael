package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitUpdatePolicyRetainsChoiceAndUserFields(t *testing.T) {
	prefix := t.TempDir()
	os.MkdirAll(filepath.Join(prefix, "share/ael"), 0700)
	path := filepath.Join(prefix, "share/ael/update-policy.json")
	os.WriteFile(path, []byte(`{"schema":1,"mode":"auto","selected_by":"explicit_choice","user_note":"preserve"}`), 0600)
	reader := bufio.NewReader(strings.NewReader(""))
	policy, err := initUpdatePolicy(args{Flags: map[string]string{}}, prefix, reader, false)
	if err != nil || policy.Mode != "auto" {
		t.Fatalf("existing policy changed %+v %v", policy, err)
	}
	policy, err = initUpdatePolicy(args{Flags: map[string]string{"update-policy": "pinned", "pin-version": "0.1.22"}}, prefix, reader, false)
	if err != nil || policy.Mode != "pinned" || policy.Version != "0.1.22" {
		t.Fatalf("pin choice lost %+v %v", policy, err)
	}
	policy, err = initUpdatePolicy(args{Flags: map[string]string{"pin-version": "0.1.23"}}, prefix, reader, false)
	if err != nil || policy.Version != "0.1.23" {
		t.Fatalf("version-only pin choice ignored %+v %v", policy, err)
	}
	var saved map[string]any
	raw, _ := os.ReadFile(path)
	if json.Unmarshal(raw, &saved) != nil || saved["user_note"] != "preserve" {
		t.Fatal("user policy fields lost")
	}
	if _, err := initUpdatePolicy(args{Flags: map[string]string{"update-policy": "pinned", "pin-version": "invalid"}}, prefix, reader, false); err == nil {
		t.Fatal("invalid pin accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(raw) {
		t.Fatal("invalid policy changed file")
	}
}

func TestInitSecretRejectsOversizedAndMissingInput(t *testing.T) {
	if _, err := initSecret(bufio.NewReader(strings.NewReader(strings.Repeat("x", 5000))), false, true); err == nil {
		t.Fatal("unbounded credential accepted")
	}
	if _, err := initSecret(bufio.NewReader(strings.NewReader("secret\n")), false, false); err == nil {
		t.Fatal("noninteractive implicit credential accepted")
	}
}
