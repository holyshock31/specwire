package registry

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"specwire/bridge/internal/domain"
)

//go:embed definitions
var definitionFS embed.FS

type Bundle struct {
	ConnectorTypes []domain.ConnectorType       `json:"connector_types"`
	Behaviors      []domain.ConnectorBehavior   `json:"behaviors"`
	DataModels     []domain.DataModelDefinition `json:"data_models"`
}

type AdapterAllowlist map[string]bool

func (a AdapterAllowlist) IsAllowlisted(operation string) bool { return a[operation] }

func (a AdapterAllowlist) Operations() []string {
	operations := make([]string, 0, len(a))
	for operation, enabled := range a {
		if enabled {
			operations = append(operations, operation)
		}
	}
	sort.Strings(operations)
	return operations
}

type AdapterCatalog interface{ IsAllowlisted(string) bool }

func ValidateBehavior(item domain.ConnectorBehavior, adapters AdapterCatalog) error {
	if strings.TrimSpace(item.ConnectorTypeKey) == "" || strings.TrimSpace(item.ConnectorTypeVersion) == "" || strings.TrimSpace(item.Key) == "" || strings.TrimSpace(item.Version) == "" || strings.TrimSpace(item.DisplayName) == "" || strings.TrimSpace(item.AdapterOperation) == "" {
		return fmt.Errorf("%w: behavior type, key, version, display name and adapter operation are required", domain.ErrInvalid)
	}
	if item.Direction != domain.DirectionInput && item.Direction != domain.DirectionOutput {
		return fmt.Errorf("%w: behavior direction is invalid", domain.ErrInvalid)
	}
	if item.ParameterSchema == nil {
		item.ParameterSchema = map[string]any{}
	}
	if len(item.RequiredCapabilities) == 0 {
		return fmt.Errorf("%w: behavior %s must declare required capabilities", domain.ErrInvalid, item.Key)
	}
	if strings.TrimSpace(item.IdempotencyStrategy) == "" {
		return fmt.Errorf("%w: behavior %s must declare an idempotency strategy", domain.ErrInvalid, item.Key)
	}
	if strings.TrimSpace(item.Reconciliation) == "" {
		return fmt.Errorf("%w: behavior %s must declare reconciliation capability", domain.ErrInvalid, item.Key)
	}
	if item.Direction == domain.DirectionInput && strings.TrimSpace(item.OutputModelRef) == "" {
		return fmt.Errorf("%w: input behavior %s must declare an output model", domain.ErrInvalid, item.Key)
	}
	if item.Direction == domain.DirectionOutput && strings.TrimSpace(item.InputModelRef) == "" {
		return fmt.Errorf("%w: output behavior %s must declare an input model", domain.ErrInvalid, item.Key)
	}
	if adapters == nil || !adapters.IsAllowlisted(item.AdapterOperation) {
		return fmt.Errorf("%w: adapter operation %s is not deployed and allowlisted", domain.ErrForbidden, item.AdapterOperation)
	}
	return nil
}

func ValidateDataModel(item domain.DataModelDefinition) error {
	if strings.TrimSpace(item.Key) == "" || strings.TrimSpace(item.Version) == "" || strings.TrimSpace(item.DisplayName) == "" || item.Schema == nil {
		return fmt.Errorf("%w: data model key, version, display name and schema are required", domain.ErrInvalid)
	}
	if schemaType, ok := item.Schema["type"].(string); ok && schemaType != "object" {
		return fmt.Errorf("%w: data model %s schema must be an object", domain.ErrInvalid, item.Key)
	}
	if properties, ok := item.Schema["properties"]; ok {
		if _, ok := properties.(map[string]any); !ok {
			return fmt.Errorf("%w: data model %s properties must be an object", domain.ErrInvalid, item.Key)
		}
	}
	for _, field := range item.RequiredFields {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("%w: data model %s has an empty required field", domain.ErrInvalid, item.Key)
		}
	}
	for field, role := range item.SemanticRoles {
		if strings.TrimSpace(field) == "" || strings.TrimSpace(role) == "" {
			return fmt.Errorf("%w: data model %s has an incomplete semantic role", domain.ErrInvalid, item.Key)
		}
	}
	if containsExecutableDefinition(item.Schema) {
		return fmt.Errorf("%w: data model %s contains executable definition content", domain.ErrForbidden, item.Key)
	}
	return nil
}

func containsExecutableDefinition(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(strings.TrimSpace(key))
			if lower == "script" || lower == "code" || lower == "program" || lower == "expression" {
				return true
			}
			if containsExecutableDefinition(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsExecutableDefinition(child) {
				return true
			}
		}
	}
	return false
}

func (b Bundle) Validate() error {
	seenTypes := map[string]struct{}{}
	for _, item := range b.ConnectorTypes {
		if strings.TrimSpace(item.Key) == "" || strings.TrimSpace(item.Version) == "" {
			return fmt.Errorf("%w: connector type key and version are required", domain.ErrInvalid)
		}
		if strings.TrimSpace(item.DisplayName) == "" || item.Provider == "" {
			return fmt.Errorf("%w: connector type %s has incomplete metadata", domain.ErrInvalid, item.Key)
		}
		key := StableKey(item.Key, item.Version)
		if _, ok := seenTypes[key]; ok {
			return fmt.Errorf("%w: duplicate connector type %s", domain.ErrConflict, key)
		}
		seenTypes[key] = struct{}{}
	}
	seenBehaviors := map[string]struct{}{}
	for _, item := range b.Behaviors {
		if strings.TrimSpace(item.Key) == "" || strings.TrimSpace(item.Version) == "" || strings.TrimSpace(item.AdapterOperation) == "" {
			return fmt.Errorf("%w: behavior key, version and adapter operation are required", domain.ErrInvalid)
		}
		if item.Direction != domain.DirectionInput && item.Direction != domain.DirectionOutput {
			return fmt.Errorf("%w: behavior %s has invalid direction %q", domain.ErrInvalid, item.Key, item.Direction)
		}
		if _, ok := seenTypes[StableKey(item.ConnectorTypeKey, item.ConnectorTypeVersion)]; !ok {
			return fmt.Errorf("%w: behavior %s references unknown connector type %s", domain.ErrInvalid, item.Key, item.ConnectorTypeKey)
		}
		key := StableKey(item.Key, item.Version)
		if _, ok := seenBehaviors[key]; ok {
			return fmt.Errorf("%w: duplicate behavior %s", domain.ErrConflict, key)
		}
		seenBehaviors[key] = struct{}{}
	}
	seenModels := map[string]struct{}{}
	for _, item := range b.DataModels {
		if strings.TrimSpace(item.Key) == "" || strings.TrimSpace(item.Version) == "" || item.Schema == nil {
			return fmt.Errorf("%w: data model %s has incomplete definition", domain.ErrInvalid, item.Key)
		}
		key := StableKey(item.Key, item.Version)
		if _, ok := seenModels[key]; ok {
			return fmt.Errorf("%w: duplicate data model %s", domain.ErrConflict, key)
		}
		seenModels[key] = struct{}{}
	}
	return nil
}

func StableKey(key, version string) string {
	return strings.TrimSpace(key) + "@" + strings.TrimSpace(version)
}

func LoadBuiltins() (Bundle, error) {
	var bundle Bundle
	err := fs.WalkDir(definitionFS, "definitions", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		body, err := fs.ReadFile(definitionFS, path)
		if err != nil {
			return fmt.Errorf("read builtin definition %s: %w", path, err)
		}
		category := filepath.Base(filepath.Dir(path))
		switch category {
		case "connector-types":
			var item domain.ConnectorType
			if err := json.Unmarshal(body, &item); err != nil {
				return fmt.Errorf("parse connector type %s: %w", path, err)
			}
			bundle.ConnectorTypes = append(bundle.ConnectorTypes, item)
		case "connector-behaviors":
			var item domain.ConnectorBehavior
			if err := json.Unmarshal(body, &item); err != nil {
				return fmt.Errorf("parse connector behavior %s: %w", path, err)
			}
			bundle.Behaviors = append(bundle.Behaviors, item)
		case "data-models":
			var item domain.DataModelDefinition
			if err := json.Unmarshal(body, &item); err != nil {
				return fmt.Errorf("parse data model %s: %w", path, err)
			}
			bundle.DataModels = append(bundle.DataModels, item)
		default:
			return fmt.Errorf("%w: unsupported registry definition directory %s", domain.ErrInvalid, category)
		}
		return nil
	})
	if err != nil {
		return Bundle{}, err
	}
	sort.Slice(bundle.ConnectorTypes, func(i, j int) bool {
		return StableKey(bundle.ConnectorTypes[i].Key, bundle.ConnectorTypes[i].Version) < StableKey(bundle.ConnectorTypes[j].Key, bundle.ConnectorTypes[j].Version)
	})
	sort.Slice(bundle.Behaviors, func(i, j int) bool {
		return StableKey(bundle.Behaviors[i].Key, bundle.Behaviors[i].Version) < StableKey(bundle.Behaviors[j].Key, bundle.Behaviors[j].Version)
	})
	sort.Slice(bundle.DataModels, func(i, j int) bool {
		return StableKey(bundle.DataModels[i].Key, bundle.DataModels[i].Version) < StableKey(bundle.DataModels[j].Key, bundle.DataModels[j].Version)
	})
	if err := bundle.Validate(); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}
