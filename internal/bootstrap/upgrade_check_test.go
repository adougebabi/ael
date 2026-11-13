package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeCheckReadsSelectedPrefixAndRefusesDowngrade(t *testing.T) {
	prefix := t.TempDir()
	if _, err := Upgrade(UpdateOptions{From: clientPackageFixture(t, Version), Prefix: prefix}); err != nil {
		t.Fatal(err)
	}
	current := BinaryVersion{Version: Version, Build: "client-fixture", OutputProtocol: 1, APIProtocol: 1, SkillProtocol: 1}
	result, err := CheckUpgradeTarget(prefix, current)
	if err != nil || result.Status != "up_to_date" || result.Previous.Version != Version {
		t.Fatalf("current %+v %v", result, err)
	}
	available := current
	available.Version = "99.0.0"
	result, err = CheckUpgradeTarget(prefix, available)
	if err != nil || result.Status != "update_available" {
		t.Fatalf("new %+v %v", result, err)
	}
	other := t.TempDir()
	if _, err := Upgrade(UpdateOptions{From: clientPackageFixture(t, "99.0.0"), Prefix: other}); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckUpgradeTarget(other, current); err == nil || !strings.Contains(err.Error(), "DOWNGRADE_REFUSED") {
		t.Fatalf("check ignored selected prefix %v", err)
	}
	absent := filepath.Join(t.TempDir(), "absent")
	result, err = CheckUpgradeTarget(absent, current)
	if err != nil || result.Status != "install_available" {
		t.Fatalf("fresh %+v %v", result, err)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("version check created target prefix")
	}
}
