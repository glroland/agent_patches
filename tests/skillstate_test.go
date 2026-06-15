package tests

import (
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

func TestSkillState_SaveAndLoadAll(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	if err := skillstate.Save(mem, "check_drives", skillstate.HealthCritical, "/ is 94.7% full"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := skillstate.Save(mem, "analyze_memory_utilization", skillstate.HealthOK, "RAM used: 40.0%"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	states, err := skillstate.LoadAll(mem)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("LoadAll() returned %d states, want 2: %+v", len(states), states)
	}

	// Sorted by skill name.
	if states[0].Skill != "analyze_memory_utilization" || states[0].Health != skillstate.HealthOK {
		t.Errorf("states[0] = %+v, want analyze_memory_utilization/ok", states[0])
	}
	if states[1].Skill != "check_drives" || states[1].Health != skillstate.HealthCritical {
		t.Errorf("states[1] = %+v, want check_drives/critical", states[1])
	}
	if states[1].Summary != "/ is 94.7% full" {
		t.Errorf("states[1].Summary = %q, want %q", states[1].Summary, "/ is 94.7% full")
	}
	if states[1].Time == "" {
		t.Error("states[1].Time is empty")
	}
}

// Overwriting a skill's state replaces the previous entry rather than
// accumulating history.
func TestSkillState_SaveOverwritesPreviousState(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	if err := skillstate.Save(mem, "check_drives", skillstate.HealthCritical, "first"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := skillstate.Save(mem, "check_drives", skillstate.HealthOK, "second"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	states, err := skillstate.LoadAll(mem)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("LoadAll() returned %d states, want 1: %+v", len(states), states)
	}
	if states[0].Health != skillstate.HealthOK || states[0].Summary != "second" {
		t.Errorf("states[0] = %+v, want ok/second", states[0])
	}
}
