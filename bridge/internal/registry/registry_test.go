package registry

import (
	"testing"

	"specwire/bridge/internal/domain"
)

func TestLoadBuiltins(t *testing.T) {
	bundle, err := LoadBuiltins()
	if err != nil {
		t.Fatalf("LoadBuiltins: %v", err)
	}
	if len(bundle.ConnectorTypes) != 2 {
		t.Fatalf("connector types = %d, want 2", len(bundle.ConnectorTypes))
	}
	if len(bundle.Behaviors) != 5 {
		t.Fatalf("behaviors = %d, want 5", len(bundle.Behaviors))
	}
	if len(bundle.DataModels) != 5 {
		t.Fatalf("data models = %d, want 5", len(bundle.DataModels))
	}
	if err := bundle.Validate(); err != nil {
		t.Fatalf("builtins invalid: %v", err)
	}
}

func TestValidateBehaviorRequiresDeployedDeclarativeContract(t *testing.T) {
	item := domain.ConnectorBehavior{ConnectorTypeKey: "custom", ConnectorTypeVersion: "v1", Key: "custom.input", Version: "v1", DisplayName: "Custom Input", Direction: domain.DirectionInput, OutputModelRef: "CustomEvent.v1", AdapterOperation: "custom.input", RequiredCapabilities: []string{"custom.read"}, IdempotencyStrategy: "delivery", Reconciliation: "delivery_lookup"}
	if err := ValidateBehavior(item, AdapterAllowlist{"custom.input": true}); err != nil {
		t.Fatalf("valid behavior rejected: %v", err)
	}
	item.AdapterOperation = "https.request"
	if err := ValidateBehavior(item, AdapterAllowlist{"custom.input": true}); err == nil {
		t.Fatal("unallowlisted adapter was accepted")
	}
	item.AdapterOperation = "custom.input"
	item.RequiredCapabilities = nil
	if err := ValidateBehavior(item, AdapterAllowlist{"custom.input": true}); err == nil {
		t.Fatal("behavior without capabilities was accepted")
	}
}
