package auth

import (
	"context"
	"fmt"

	"specwire/bridge/internal/domain"
)

type Scope struct {
	Type string
	ID   domain.ID
}

// Authorize evaluates Workspace membership and the fixed role hierarchy.  A
// workspace-scoped binding covers all resources in that Workspace; a scoped
// binding only covers the exact declared scope.  Provider membership is not
// consulted here.
func Authorize(ctx context.Context, store Store, accountID, workspaceID domain.ID, required domain.Role, scope Scope) error {
	if accountID.Empty() || workspaceID.Empty() {
		return fmt.Errorf("%w: authorization context is incomplete", domain.ErrForbidden)
	}
	if required != domain.RoleAdmin && required != domain.RoleOperator && required != domain.RoleViewer {
		return fmt.Errorf("%w: custom roles are not supported", domain.ErrForbidden)
	}
	bindings, err := store.ListRoleBindings(ctx, accountID, workspaceID)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if !roleSatisfies(binding.Role, required) || !scopeMatches(binding, scope) {
			continue
		}
		return nil
	}
	return fmt.Errorf("%w: account is not authorized for %s", domain.ErrForbidden, scope.Type)
}

func roleSatisfies(granted, required domain.Role) bool {
	switch granted {
	case domain.RoleAdmin:
		return true
	case domain.RoleOperator:
		return required == domain.RoleOperator || required == domain.RoleViewer
	case domain.RoleViewer:
		return required == domain.RoleViewer
	default:
		return false
	}
}

func scopeMatches(binding domain.ScopedRoleBinding, requested Scope) bool {
	if binding.ScopeType == "workspace" {
		return true
	}
	return binding.ScopeType == requested.Type && binding.ScopeID == requested.ID
}
