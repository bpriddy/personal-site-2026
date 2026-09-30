package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	contractPath = "../../docs/content-contract.json"
	docsDir      = "../../docs"
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestContractIsAdditive is the compatibility gate: the current contract may
// only add to the released (frozen) snapshot of its major version.
func TestContractIsAdditive(t *testing.T) {
	cur := readFile(t, contractPath)
	var meta struct {
		ContractVersion int `json:"contractVersion"`
	}
	if err := json.Unmarshal(cur, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.ContractVersion != Version {
		t.Fatalf("docs/content-contract.json has contractVersion %d; internal/contract.Version is %d", meta.ContractVersion, Version)
	}
	snapPath := filepath.Join(docsDir, fmt.Sprintf("content-contract.v%d.snapshot.json", Version))
	snap, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("no frozen snapshot for contract v%d (%v): copy docs/content-contract.json to %s when releasing it", Version, err, snapPath)
	}
	changes, err := Breaking(snap, cur)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		t.Errorf("non-additive change to the content contract: %s", c)
	}
	if len(changes) > 0 {
		t.Log("Front ends depend on the released contract. Make the change additive (new optional fields only), " +
			"or bump contractVersion with a new snapshot and a migration period (docs/observer.md, Layer 1).")
	}
}

func TestBreakingFixtures(t *testing.T) {
	base := readFile(t, "testdata/compat/base.json")
	for _, tc := range []struct {
		fixture string
		want    []string // "kind: path"
	}{
		{"base", nil},
		{"additive", nil},
		{"break-removed-property", []string{"removed property: pages[].body", "no longer required: pages[].body"}},
		{"break-changed-type", []string{"changed type: experiments[].title"}},
		{"break-newly-required", []string{"newly required: pages[].subtitle"}},
		{"break-removed-collection", []string{"removed collection: experiments", "no longer required: experiments"}},
		{"break-no-longer-required", []string{"no longer required: pages[].title"}},
		{"break-closed-extras", []string{"changed type: pages[].*"}},
		{"break-changed-item-type", []string{"changed type: experiments[]._generated[]", "changed type: pages[]._generated[]"}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			changes, err := Breaking(base, readFile(t, "testdata/compat/"+tc.fixture+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, c := range changes {
				got = append(got, c.String())
			}
			slices.Sort(got)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("changes:\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestBreakingInline(t *testing.T) {
	for _, tc := range []struct {
		name, old, new string
		breaking       bool
	}{
		{"same", `{"type":"string"}`, `{"type":"string"}`, false},
		{"type list order", `{"type":["string","number"]}`, `{"type":["number","string"]}`, false},
		{"narrowed type", `{"type":["string","number"]}`, `{"type":"string"}`, true},
		{"type added", `{}`, `{"type":"string"}`, true},
		{"const changed", `{"const":1}`, `{"const":2}`, true},
		{"items removed", `{"type":"array","items":{"type":"string"}}`, `{"type":"array"}`, true},
		{"extras schema to true", `{"additionalProperties":{"type":"string"}}`, `{"additionalProperties":true}`, true},
		{"extras absent to true", `{}`, `{"additionalProperties":true}`, false},
		{"recursive ref", `{"$defs":{"n":{"type":"object","properties":{"kids":{"type":"array","items":{"$ref":"#/$defs/n"}}}}},"$ref":"#/$defs/n"}`,
			`{"$defs":{"n":{"type":"object","properties":{"kids":{"type":"array","items":{"$ref":"#/$defs/n"}},"x":{"type":"string"}}}},"$ref":"#/$defs/n"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changes, err := Breaking([]byte(tc.old), []byte(tc.new))
			if err != nil {
				t.Fatal(err)
			}
			if (len(changes) > 0) != tc.breaking {
				t.Errorf("breaking = %v (%v), want %v", len(changes) > 0, changes, tc.breaking)
			}
		})
	}
	if _, err := Breaking([]byte(`{"$ref":"http://example.com/x"}`), []byte(`{}`)); err == nil {
		t.Error("remote $ref: no error")
	}
	if _, err := Breaking([]byte(`{`), []byte(`{}`)); err == nil {
		t.Error("bad JSON: no error")
	}
}

// TestSchemaMatchesDeclared keeps the JSON Schema and the Go declaration of
// each collection's fields in step.
func TestSchemaMatchesDeclared(t *testing.T) {
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Items struct {
				Ref string `json:"$ref"`
			} `json:"items"`
		} `json:"properties"`
		Defs map[string]struct {
			Required   []string                  `json:"required"`
			Properties map[string]map[string]any `json:"properties"`
			Extra      map[string]any            `json:"additionalProperties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(readFile(t, contractPath), &schema); err != nil {
		t.Fatal(err)
	}
	for coll, fields := range Declared {
		ref := schema.Properties[coll].Items.Ref
		def, ok := schema.Defs[strings.TrimPrefix(ref, "#/$defs/")]
		if !ok {
			t.Errorf("%s: no item schema (ref %q)", coll, ref)
			continue
		}
		want := append(slices.Clone(fields), GeneratedKey)
		if got := slices.Sorted(slices.Values(def.Required)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
			t.Errorf("%s: schema requires %v; Declared + _generated = %v", coll, got, want)
		}
		for _, f := range fields {
			if def.Properties[f]["type"] != "string" {
				t.Errorf("%s.%s: schema type %v, want string", coll, f, def.Properties[f]["type"])
			}
		}
		if def.Extra == nil {
			t.Errorf("%s: items must allow extra (generated) fields", coll)
		}
	}
	for _, c := range []string{"contractVersion", Pages, Experiments} {
		if !slices.Contains(schema.Required, c) {
			t.Errorf("schema doesn't require %s", c)
		}
	}
}
