package registry

import "testing"

func TestLoadBuiltins(t *testing.T) {
	bundle, err := LoadBuiltins()
	if err != nil {
		t.Fatalf("LoadBuiltins: %v", err)
	}
	if len(bundle.ConnectorTypes) != 2 {
		t.Fatalf("connector types = %d, want 2", len(bundle.ConnectorTypes))
	}
	if len(bundle.Behaviors) != 4 {
		t.Fatalf("behaviors = %d, want 4", len(bundle.Behaviors))
	}
	if len(bundle.DataModels) != 4 {
		t.Fatalf("data models = %d, want 4", len(bundle.DataModels))
	}
	if err := bundle.Validate(); err != nil {
		t.Fatalf("builtins invalid: %v", err)
	}
}
