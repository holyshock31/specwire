package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"specwire/bridge/internal/auth"
	"specwire/bridge/internal/controlplane"
	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/provider"
)

const (
	sessionCookie = "specwire_session"
	csrfCookie    = "specwire_csrf"
	csrfHeader    = "X-CSRF-Token"
)

type AccountReader interface {
	GetAccount(context.Context, domain.ID) (domain.Account, error)
}

type Server struct {
	auth          *auth.LocalProvider
	authorization auth.Store
	accounts      AccountReader
	endpoints     *controlplane.EndpointService
	integration   *IntegrationServices
	secureCookie  bool
}

func NewServer(local *auth.LocalProvider, authorization auth.Store, accounts AccountReader, endpoints *controlplane.EndpointService) (*Server, error) {
	if local == nil || authorization == nil || accounts == nil || endpoints == nil {
		return nil, fmt.Errorf("%w: HTTP API dependencies are required", domain.ErrInvalid)
	}
	return &Server{auth: local, authorization: authorization, accounts: accounts, endpoints: endpoints}, nil
}

func (s *Server) SetSecureCookie(secure bool) { s.secureCookie = secure }

// SetIntegrationServices attaches the persistent Integration Flow control
// plane.  It is deliberately optional so the legacy endpoint API and its
// callers remain compatible during the cutover.
func (s *Server) SetIntegrationServices(services IntegrationServices) {
	s.integration = &services
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	switch {
	case path == "auth/bootstrap" && r.Method == http.MethodPost:
		s.handleBootstrap(w, r)
	case path == "auth/login" && r.Method == http.MethodPost:
		s.handleLogin(w, r)
	case path == "auth/logout" && r.Method == http.MethodPost:
		s.handleLogout(w, r)
	case path == "auth/me" && r.Method == http.MethodGet:
		s.handleMe(w, r)
	case path == "auth/workspaces" && r.Method == http.MethodGet:
		s.handleWorkspaces(w, r)
	case strings.HasPrefix(path, "workspaces/"):
		if s.integration != nil && s.handleIntegration(w, r, path) {
			return
		}
		s.handleWorkspace(w, r, strings.TrimPrefix(path, "workspaces/"))
	default:
		http.NotFound(w, r)
	}
}

type bootstrapRequest struct{ Email, Password, DisplayName string }

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	var request bootstrapRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	account, workspace, err := s.auth.BootstrapFirstAdmin(r.Context(), request.Email, request.Password, request.DisplayName)
	if err != nil {
		writeError(w, err)
		return
	}
	if s.integration != nil && s.integration.BootstrapWorkspace != nil {
		if err := s.integration.BootstrapWorkspace(r.Context(), workspace); err != nil {
			writeError(w, fmt.Errorf("bootstrap Workspace catalog: %w", err))
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"account": account, "workspace": workspace})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var request struct{ Email, Password string }
	if !decodeJSON(w, r, &request) {
		return
	}
	account, credentials, err := s.auth.Login(r.Context(), request.Email, request.Password)
	if err != nil {
		// Deliberately collapse not-found, disabled-account and bad-password into
		// the same response.
		writeError(w, ErrUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: credentials.Token, Path: "/", HttpOnly: true, Secure: s.secureCookie, SameSite: http.SameSiteLaxMode, Expires: credentials.ExpiresAt})
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: credentials.CSRFToken, Path: "/", Secure: s.secureCookie, SameSite: http.SameSiteLaxMode, Expires: credentials.ExpiresAt})
	writeJSON(w, http.StatusOK, map[string]any{"account": account, "csrf_token": credentials.CSRFToken, "expires_at": credentials.ExpiresAt})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(w, r)
	if !ok {
		return
	}
	if !s.checkCSRF(w, r, session) {
		return
	}
	if err := s.auth.Logout(r.Context(), session.ID); err != nil {
		writeError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: "", Path: "/", Secure: s.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(w, r)
	if !ok {
		return
	}
	account, err := s.accounts.GetAccount(r.Context(), session.AccountID)
	if err != nil {
		writeError(w, err)
		return
	}
	csrfToken := ""
	if cookie, err := r.Cookie(csrfCookie); err == nil && s.auth.ValidateCSRF(session, cookie.Value) == nil {
		csrfToken = cookie.Value
	} else {
		csrfToken, err = s.auth.RefreshCSRF(r.Context(), session)
		if err != nil {
			writeError(w, err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: csrfToken, Path: "/", Secure: s.secureCookie, SameSite: http.SameSiteLaxMode, Expires: session.ExpiresAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": account, "csrf_token": csrfToken, "session": map[string]any{"id": session.ID, "expires_at": session.ExpiresAt}})
}

func (s *Server) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(w, r)
	if !ok {
		return
	}
	reader, ok := s.accounts.(interface {
		ListWorkspacesForAccount(context.Context, domain.ID) ([]domain.Workspace, error)
	})
	if !ok {
		writeError(w, fmt.Errorf("%w: Workspace listing is not configured", domain.ErrInvalid))
		return
	}
	items, err := reader.ListWorkspacesForAccount(r.Context(), session.AccountID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request, rest string) {
	parts := splitPath(rest)
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	workspaceID := domain.ID(parts[0])
	session, ok := s.session(w, r)
	if !ok {
		return
	}
	if len(parts) == 2 && parts[1] == "gitlab-instances" {
		switch r.Method {
		case http.MethodGet:
			if !s.authorize(w, r, session, workspaceID, domain.RoleViewer, auth.Scope{Type: "workspace", ID: workspaceID}) {
				return
			}
			instances, err := s.endpoints.ListGitLab(r.Context(), workspaceID)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, instances)
		case http.MethodPost:
			if !s.authorizeMutation(w, r, session, workspaceID, domain.RoleAdmin, auth.Scope{Type: "workspace", ID: workspaceID}) {
				return
			}
			var instance domain.GitLabInstance
			if !decodeJSON(w, r, &instance) {
				return
			}
			instance.WorkspaceID = workspaceID
			if instance.ID.Empty() {
				instance.ID = domain.NewID()
			}
			if err := s.endpoints.AddGitLab(r.Context(), instance); err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, instance)
		default:
			http.NotFound(w, r)
		}
		return
	}
	if len(parts) == 2 && parts[1] == "multica-instances" {
		switch r.Method {
		case http.MethodGet:
			if !s.authorize(w, r, session, workspaceID, domain.RoleViewer, auth.Scope{Type: "workspace", ID: workspaceID}) {
				return
			}
			instances, err := s.endpoints.ListMultica(r.Context(), workspaceID)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, instances)
		case http.MethodPost:
			if !s.authorizeMutation(w, r, session, workspaceID, domain.RoleAdmin, auth.Scope{Type: "workspace", ID: workspaceID}) {
				return
			}
			var instance domain.MulticaInstance
			if !decodeJSON(w, r, &instance) {
				return
			}
			instance.WorkspaceID = workspaceID
			if instance.ID.Empty() {
				instance.ID = domain.NewID()
			}
			if err := s.endpoints.AddMultica(r.Context(), instance); err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, instance)
		default:
			http.NotFound(w, r)
		}
		return
	}
	if len(parts) == 4 && parts[3] == "test" && (parts[1] == "gitlab-instances" || parts[1] == "multica-instances") {
		if r.Method != http.MethodPost || !s.authorizeMutation(w, r, session, workspaceID, domain.RoleAdmin, auth.Scope{Type: "workspace", ID: workspaceID}) {
			return
		}
		if parts[1] == "gitlab-instances" {
			instance, err := s.endpoints.GetGitLab(r.Context(), workspaceID, domain.ID(parts[2]))
			if err != nil {
				writeError(w, err)
				return
			}
			results, err := s.endpoints.TestGitLab(r.Context(), instance)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, results)
		} else {
			instance, err := s.endpoints.GetMultica(r.Context(), workspaceID, domain.ID(parts[2]))
			if err != nil {
				writeError(w, err)
				return
			}
			results, err := s.endpoints.TestMultica(r.Context(), instance)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, results)
		}
		return
	}
	if len(parts) == 3 && (parts[1] == "gitlab-instances" || parts[1] == "multica-instances") {
		if r.Method != http.MethodPost || !s.authorizeMutation(w, r, session, workspaceID, domain.RoleAdmin, auth.Scope{Type: "workspace", ID: workspaceID}) {
			return
		}
		var err error
		if parts[1] == "gitlab-instances" {
			err = s.endpoints.DisableGitLab(r.Context(), workspaceID, domain.ID(parts[2]))
		} else {
			err = s.endpoints.DisableMultica(r.Context(), workspaceID, domain.ID(parts[2]))
		}
		if err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) (domain.Session, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		writeError(w, ErrUnauthorized)
		return domain.Session{}, false
	}
	session, err := s.auth.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		writeError(w, ErrUnauthorized)
		return domain.Session{}, false
	}
	return session, true
}

func (s *Server) checkCSRF(w http.ResponseWriter, r *http.Request, session domain.Session) bool {
	if err := s.auth.ValidateCSRF(session, r.Header.Get(csrfHeader)); err != nil {
		writeError(w, err)
		return false
	}
	return true
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request, session domain.Session, workspaceID domain.ID, role domain.Role, scope auth.Scope) bool {
	if err := auth.Authorize(r.Context(), s.authStore(), session.AccountID, workspaceID, role, scope); err != nil {
		writeError(w, err)
		return false
	}
	return true
}

func (s *Server) authorizeMutation(w http.ResponseWriter, r *http.Request, session domain.Session, workspaceID domain.ID, role domain.Role, scope auth.Scope) bool {
	if !s.checkCSRF(w, r, session) {
		return false
	}
	return s.authorize(w, r, session, workspaceID, role, scope)
}

// authStore is supplied by LocalProvider only through its Store interface. We
// retain it as a narrow interface in Server so authorization cannot bypass
// the same repository used by login.
func (s *Server) authStore() auth.Store { return s.authorization }

var ErrUnauthorized = errors.New("unauthorized")

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, fmt.Errorf("%w: invalid JSON request", domain.ErrInvalid))
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, fmt.Errorf("%w: request must contain one JSON value", domain.ErrInvalid))
		return false
	}
	return true
}

func splitPath(path string) []string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		return nil
	}
	return parts
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	code := "internal_error"
	requestID := ""
	detail := ""
	switch {
	case errors.Is(err, ErrUnauthorized), errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrInvalidSession):
		status, message, code = http.StatusUnauthorized, "unauthorized", "session_unauthorized"
	case errors.Is(err, auth.ErrCSRF):
		status, message, code = http.StatusForbidden, "csrf validation failed", "csrf_failed"
	case errors.Is(err, domain.ErrForbidden):
		status, message, code = http.StatusForbidden, "forbidden", "forbidden"
	case errors.Is(err, domain.ErrNotFound):
		status, message, code = http.StatusNotFound, "not found", "not_found"
	case errors.Is(err, domain.ErrConflict):
		status, message, code = http.StatusConflict, "conflict", "conflict"
	case errors.Is(err, controlplane.ErrCapabilityUnavailable):
		status, message, code = http.StatusUnprocessableEntity, "required provider capability is unavailable; update the configured provider credential", "provider_capability_unavailable"
	case errors.Is(err, domain.ErrInvalid):
		status, message, code = http.StatusBadRequest, "invalid request", "invalid_request"
		detail = publicInvalidDetail(err)
	}
	var providerErr *provider.ProviderError
	if errors.As(err, &providerErr) {
		requestID = providerErr.RequestID
		switch {
		case errors.Is(err, provider.ErrNotConfigured):
			status, message, code = http.StatusUnprocessableEntity, "provider credential is required; configure a persistent GitLab or Group credential", "provider_credential_required"
		case providerErr.Category == provider.ErrorUnauthorized:
			status, message, code = http.StatusUnauthorized, "provider credential was rejected; update the configured credential", "provider_credential_rejected"
		case providerErr.Category == provider.ErrorForbidden:
			status, message, code = http.StatusForbidden, "provider credential lacks the required permission", "provider_credential_forbidden"
		case providerErr.Category == provider.ErrorNotFound:
			status, message, code = http.StatusNotFound, "provider resource was not found; verify the selected instance, Group, or project", "provider_not_found"
		case providerErr.Category == provider.ErrorConflict:
			status, message, code = http.StatusConflict, "provider reports a conflicting resource", "provider_conflict"
		case providerErr.Category == provider.ErrorRateLimited:
			status, message, code = http.StatusTooManyRequests, "provider rate limit reached; retry later", "provider_rate_limited"
		case providerErr.Category == provider.ErrorTimeout:
			status, message, code = http.StatusGatewayTimeout, "provider request timed out; retry later", "provider_timeout"
		case providerErr.Category == provider.ErrorTransient:
			status, message, code = http.StatusBadGateway, "provider is temporarily unavailable; retry later", "provider_transient"
		case providerErr.Category == provider.ErrorInvalidResponse:
			status, message, code = http.StatusBadGateway, "provider returned an invalid response", "provider_invalid_response"
		case providerErr.Category == provider.ErrorIndeterminate:
			status, message, code = http.StatusBadGateway, "provider outcome is indeterminate; inspect the operation before retrying", "provider_indeterminate"
		}
	}
	response := map[string]any{"error": message, "code": code}
	if detail != "" && providerErr == nil {
		response["detail"] = detail
	}
	if requestID != "" {
		response["request_id"] = requestID
	}
	writeJSON(w, status, response)
}

func publicInvalidDetail(err error) string {
	const prefix = "invalid domain value:"
	message := strings.TrimSpace(err.Error())
	if !strings.HasPrefix(message, prefix) {
		return ""
	}
	detail := strings.TrimSpace(strings.TrimPrefix(message, prefix))
	if len(detail) > 300 {
		return detail[:300]
	}
	return detail
}
