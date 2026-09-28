package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

const repairSchema = `{"type":"object","properties":{
	"count":  {"type":"integer"},
	"ratio":  {"type":"number"},
	"force":  {"type":"boolean"},
	"args":   {"type":"array"},
	"opts":   {"type":"object"},
	"query":  {"type":"string"}
}}`

func repairOne(t *testing.T, args string) (map[string]any, []string) {
	t.Helper()
	fixed, repairs, err := RepairArgs(json.RawMessage(repairSchema), json.RawMessage(args))
	if err != nil {
		t.Fatalf("RepairArgs: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(fixed, &obj); err != nil {
		t.Fatalf("repaired args no longer decode: %v", err)
	}
	return obj, repairs
}

func TestRepairStringInteger(t *testing.T) {
	obj, repairs := repairOne(t, `{"count":"42"}`)
	if obj["count"] != float64(42) { // json.Unmarshal reads numbers as float64
		t.Errorf("count not coerced: %#v", obj["count"])
	}
	if len(repairs) != 1 || !strings.Contains(repairs[0], "count") {
		t.Errorf("repair not reported: %v", repairs)
	}
}

func TestRepairStringNumber(t *testing.T) {
	obj, _ := repairOne(t, `{"ratio":"0.5"}`)
	if obj["ratio"] != 0.5 {
		t.Errorf("ratio not coerced: %#v", obj["ratio"])
	}
}

func TestRepairStringBoolean(t *testing.T) {
	obj, repairs := repairOne(t, `{"force":"true"}`)
	if obj["force"] != true {
		t.Errorf("force not coerced: %#v", obj["force"])
	}
	if len(repairs) != 1 {
		t.Errorf("repair not reported: %v", repairs)
	}
}

func TestRepairScalarIntoArray(t *testing.T) {
	obj, repairs := repairOne(t, `{"args":"ls"}`)
	arr, ok := obj["args"].([]any)
	if !ok || len(arr) != 1 || arr[0] != "ls" {
		t.Errorf("args not wrapped: %#v", obj["args"])
	}
	if len(repairs) != 1 {
		t.Errorf("repair not reported: %v", repairs)
	}
}

func TestRepairJSONStringIntoObject(t *testing.T) {
	obj, _ := repairOne(t, `{"opts":"{\"a\":1}"}`)
	inner, ok := obj["opts"].(map[string]any)
	if !ok || inner["a"] != float64(1) {
		t.Errorf("opts not unpacked: %#v", obj["opts"])
	}
}

func TestRepairLeavesAmbiguousUntouched(t *testing.T) {
	// "cuarenta y dos" is not an unambiguous integer; "yes" is not a
	// boolean literal; an array-typed property already holding an array
	// needs nothing; a string property holding a string is fine.
	in := `{"count":"cuarenta y dos","force":"yes","args":["a","b"],"query":"hola"}`
	fixed, repairs, err := RepairArgs(json.RawMessage(repairSchema), json.RawMessage(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(repairs) != 0 {
		t.Errorf("ambiguous values must stay untouched, repairs: %v", repairs)
	}
	if string(fixed) != in {
		t.Errorf("arguments should pass through byte-identical: %s", fixed)
	}
}

func TestRepairNonObjectPassesThrough(t *testing.T) {
	for _, in := range []string{`"not json at all"`, `[1,2,3]`, `{broken`} {
		fixed, repairs, err := RepairArgs(json.RawMessage(repairSchema), json.RawMessage(in))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if len(repairs) != 0 || string(fixed) != in {
			t.Errorf("%s should pass through untouched, got %s %v", in, fixed, repairs)
		}
	}
}
