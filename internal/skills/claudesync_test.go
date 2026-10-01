package skills

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTurningTheSyncOffHidesTheSyncedSkills(t *testing.T) {
	r := fixture(t)
	if !ClaudeSync(r) {
		t.Fatal("sync should be on with no setting")
	}
	if _, ok := byName(t, mustDiscover(t, r))["cloud-one"]; !ok {
		t.Fatal("synced skill missing while the sync is on")
	}

	if err := SetClaudeSync(r, false); err != nil {
		t.Fatal(err)
	}
	if ClaudeSync(r) {
		t.Fatal("sync still on after turning it off")
	}
	list := byName(t, mustDiscover(t, r))
	if _, ok := list["cloud-one"]; ok {
		t.Fatal("synced skill still listed with the sync off")
	}
	if _, ok := list["shared"]; !ok {
		t.Fatal("the user's own skill went with the synced ones")
	}

	if err := SetClaudeSync(r, true); err != nil {
		t.Fatal(err)
	}
	if !ClaudeSync(r) {
		t.Fatal("sync still off after turning it on")
	}
}

func TestTheSyncSwitchWritesThroughALinkedSettingsFile(t *testing.T) {
	r := machine(t)
	shared := filepath.Join(r.Home, "dotfiles", "claude-settings.json")
	write(t, shared, "{\n  \"model\": \"opus\"\n}\n")
	link(t, shared, filepath.Join(r.ClaudeConfigDir, "settings.json"))

	if err := SetClaudeSync(r, false); err != nil {
		t.Fatal(err)
	}
	if !isSymlink(filepath.Join(r.ClaudeConfigDir, "settings.json")) {
		t.Fatal("the link was replaced by a file")
	}
	if got, want := read(t, shared), "{\n  \"syncClaudeAiSkills\": false,\n  \"model\": \"opus\"\n}\n"; got != want {
		t.Fatalf("shared settings:\n%s\nwant:\n%s", got, want)
	}
}

func TestTheSyncSwitchCreatesSettingsOnlyToTurnItOff(t *testing.T) {
	r := machine(t)
	path := filepath.Join(r.ClaudeConfigDir, "settings.json")
	if err := SetClaudeSync(r, true); err != nil {
		t.Fatal(err)
	}
	if exists(path) {
		t.Fatal("turning on created a settings file")
	}
	if err := SetClaudeSync(r, false); err != nil {
		t.Fatal(err)
	}
	if ClaudeSync(r) {
		t.Fatal("sync on after writing a new settings file")
	}
}

func TestTheSyncSwitchRefusesSettingsItCannotRead(t *testing.T) {
	r := machine(t)
	path := filepath.Join(r.ClaudeConfigDir, "settings.json")
	write(t, path, "{ not json")
	if err := SetClaudeSync(r, false); err == nil {
		t.Fatal("wrote into a file that is not JSON")
	}
	if got := read(t, path); got != "{ not json" {
		t.Fatalf("file changed: %q", got)
	}
}

func TestSetTopKey(t *testing.T) {
	f := "false"
	tests := []struct {
		name, in string
		value    *string
		want     string
	}{
		{"adds to an empty object", "{}", &f, "{\n  \"k\": false\n}"},
		{"adds first, indented like the rest", "{\n    \"a\": 1\n}", &f, "{\n    \"k\": false,\n    \"a\": 1\n}"},
		{"replaces only the value", "{\"a\": {\"k\": 1}, \"k\":  true }", &f, "{\"a\": {\"k\": 1}, \"k\":  false }"},
		{"removes a middle key with its comma", "{\n  \"a\": 1,\n  \"k\": false,\n  \"b\": 2\n}", nil, "{\n  \"a\": 1,\n  \"b\": 2\n}"},
		{"removes the last key", "{\n  \"a\": 1,\n  \"k\": false\n}", nil, "{\n  \"a\": 1\n}"},
		{"removes the first key", "{\n  \"k\": false,\n  \"a\": [1, 2]\n}", nil, "{\n  \"a\": [1, 2]\n}"},
		{"removes the only key", "{\n  \"k\": false\n}", nil, "{\n}"},
		{"removes every copy", "{\"k\": 1, \"a\": 2, \"k\": 3}", nil, "{\"a\": 2}"},
		{"leaves a nested key alone", "{\"a\": {\"k\": false}}", nil, "{\"a\": {\"k\": false}}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := setTopKey(tt.in, "k", tt.value)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got  %q\nwant %q", got, tt.want)
			}
			var a, b any
			if json.Unmarshal([]byte(got), &a) != nil || json.Unmarshal([]byte(tt.want), &b) != nil || !reflect.DeepEqual(a, b) {
				t.Fatalf("result is not the JSON expected: %q", got)
			}
		})
	}
}

func TestSetNestedKey(t *testing.T) {
	off := `"off"`
	tests := []struct {
		name, in string
		value    *string
		want     string
	}{
		{"adds the outer object, indented like the file", "{\n    \"a\": 1\n}", &off, "{\n    \"o\": {\n        \"k\": \"off\"\n    },\n    \"a\": 1\n}"},
		{"adds the outer object to an empty file", "{}\n", &off, "{\n  \"o\": {\n    \"k\": \"off\"\n  }\n}\n"},
		{"adds the outer object on one line like the file", `{"a": 1}`, &off, `{"o": {"k": "off"},"a": 1}`},
		{"adds into an object that has others", "{\n  \"o\": {\n    \"x\": \"on\"\n  }\n}", &off, "{\n  \"o\": {\n    \"k\": \"off\",\n    \"x\": \"on\"\n  }\n}"},
		{"adds into an empty object, one level in", "{\n  \"a\": 1,\n  \"o\": {}\n}", &off, "{\n  \"a\": 1,\n  \"o\": {\n    \"k\": \"off\"\n  }\n}"},
		{"replaces only the value", "{\"o\": {\"k\":  \"on\" }, \"k\": 1}", &off, "{\"o\": {\"k\":  \"off\" }, \"k\": 1}"},
		{"removes the key and keeps the rest of the object", "{\n  \"o\": {\n    \"k\": \"off\",\n    \"x\": \"on\"\n  }\n}", nil, "{\n  \"o\": {\n    \"x\": \"on\"\n  }\n}"},
		{"removes the outer object once it is empty", "{\n  \"o\": {\n    \"k\": \"off\"\n  },\n  \"a\": 1\n}", nil, "{\n  \"a\": 1\n}"},
		{"removing what is not there changes nothing", "{\n  \"o\": {\"x\": 1}\n}", nil, "{\n  \"o\": {\"x\": 1}\n}"},
		{"removing with no outer object changes nothing", "{\"a\": 1}", nil, "{\"a\": 1}"},
		{"a top-level key of the same name is left alone", "{\"k\": \"off\"}", nil, "{\"k\": \"off\"}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := setNestedKey(tt.in, "o", "k", tt.value)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got  %q\nwant %q", got, tt.want)
			}
			var a, b any
			if json.Unmarshal([]byte(got), &a) != nil || json.Unmarshal([]byte(tt.want), &b) != nil || !reflect.DeepEqual(a, b) {
				t.Fatalf("result is not the JSON expected: %q", got)
			}
		})
	}
	for _, in := range []string{`{"o": "off"}`, `[1]`, `{"o": {`} {
		if got, err := setNestedKey(in, "o", "k", &off); err == nil {
			t.Errorf("%q edited to %q, want an error", in, got)
		}
	}
}
