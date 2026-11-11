package runcfg

import (
	"reflect"
	"testing"
)

func TestEnvRoundTripAndSemicolons(t *testing.T) {
	vars := ParseEnv("A=1\nURL=jdbc:pg://h:5432/db?x=y\n\n")
	want := []EnvVar{{"A", "1"}, {"URL", "jdbc:pg://h:5432/db?x=y"}}
	if !reflect.DeepEqual(vars, want) {
		t.Fatalf("parsed %v", vars)
	}
	if got := EnvSemicolons(vars); got != "A=1;URL=jdbc:pg://h:5432/db?x=y" {
		t.Errorf("semicolons %q", got)
	}
	if got := ParseEnvText("A=1;B=2\r\nC=3"); len(got) != 3 || got[1].Name != "B" {
		t.Errorf("pasted %v", got)
	}
}

func TestMergeEnvOverridesByName(t *testing.T) {
	got := MergeEnv([]EnvVar{{"A", "1"}, {"B", "2"}}, []EnvVar{{"B", "x"}, {"C", "3"}})
	want := []EnvVar{{"A", "1"}, {"B", "x"}, {"C", "3"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merged %v", got)
	}
}
