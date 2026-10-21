package agent

import "testing"

func TestMergeOverridesBuiltin(t *testing.T) {
	builtins := []Spec{
		{Name: "claude", Command: "claude"},
		{Name: "codex", Command: "codex"},
	}
	custom := []Spec{
		{Name: "claude", Command: "my-claude", Args: []string{"--yolo"}},
		{Name: "local", Command: "local"},
	}

	merged := Merge(builtins, custom)
	if len(merged) != 3 {
		t.Fatalf("len = %d, want 3", len(merged))
	}
	if merged[0].Name != "claude" || merged[0].Command != "my-claude" {
		t.Fatalf("override not first: %+v", merged[0])
	}
	if merged[1].Name != "local" {
		t.Fatalf("custom order: %+v", merged[1])
	}
	if merged[2].Name != "codex" {
		t.Fatalf("built-in order: %+v", merged[2])
	}
}

func TestMergeSkipsInvalidAndDuplicates(t *testing.T) {
	merged := Merge(nil, []Spec{
		{Name: "", Command: "x"},
		{Name: "ok", Command: ""},
		{Name: "ok", Command: "ok"},
		{Name: "OK", Command: "other"},
	})
	if len(merged) != 1 || merged[0].Command != "ok" {
		t.Fatalf("merged = %+v", merged)
	}
}

func TestFindCaseInsensitive(t *testing.T) {
	spec, ok := Find(Builtins(), "GEMINI")
	if !ok || spec.Command != "gemini" {
		t.Fatalf("Find = %+v, %v", spec, ok)
	}
	if _, ok := Find(Builtins(), "definitely-missing"); ok {
		t.Fatal("expected a missing agent")
	}
}

func TestAvailable(t *testing.T) {
	if !Available(Spec{Name: "sh", Command: "sh"}) {
		t.Fatal("sh should be available")
	}
	if Available(Spec{Name: "missing", Command: "agentty-definitely-missing"}) {
		t.Fatal("missing binary should be unavailable")
	}
}
