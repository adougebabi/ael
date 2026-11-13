package main

import (
	"reflect"
	"testing"
)

func TestMaintenanceCheckFlagPreservesCheckIdentifiers(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		words     []string
		value     string
	}{
		{[]string{"context", "record-check", "--check", "unit"}, []string{"context", "record-check"}, "unit"},
		{[]string{"verify", "--check", "unit"}, []string{"verify"}, "unit"},
		{[]string{"context", "record-check", "--check=unit"}, []string{"context", "record-check"}, "unit"},
		{[]string{"version", "--check", "--json"}, []string{"version"}, "true"},
		{[]string{"upgrade", "--check", "--prefix", "/tmp/fixture"}, []string{"upgrade"}, "true"},
	} {
		result := parse(test.arguments)
		if !reflect.DeepEqual(result.Words, test.words) || result.Flags["check"] != test.value {
			t.Fatalf("check flag changed command semantics: %v -> %+v", test.arguments, result)
		}
	}
}
