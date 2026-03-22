package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const (
	claimsKey contextKey = "claims"
)

// Middleware returns an HTTP middleware that validates JWT tokens.
// It also accepts the shared service token (JWT_SECRET) for machine-to-machine
// auth from gotailme, injecting synthetic admin claims so the request is treated
// as a fully privileged service call.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			http.Error(w, `{"error":"invalid authorization format"}`, http.StatusUnauthorized)
			return
		}

		token := parts[1]

		// Check for service token (machine-to-machine auth from gotailme)
		if s.serviceToken != "" && token == s.serviceToken {
			claims := &Claims{
				UserID: "service",
				Email:  "service@diginode.cc",
				Role:   RoleAdmin,
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Normal JWT validation
		claims, err := s.ValidateToken(token)
		if err != nil {
			http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole returns middleware that checks the user has a minimum role level.
func RequireRole(minRole Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			if !hasMinRole(claims.Role, minRole) {
				http.Error(w, `{"error":"insufficient permissions"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GetClaims extracts JWT claims from the request context.
func GetClaims(ctx context.Context) *Claims {
	claims, _ := ctx.Value(claimsKey).(*Claims)
	return claims
}

// Role hierarchy: ADMIN > OPERATOR > ANALYST > VIEWER
func hasMinRole(userRole, minRole Role) bool {
	roleLevel := map[Role]int{
		RoleViewer:   0,
		RoleAnalyst:  1,
		RoleOperator: 2,
		RoleAdmin:    3,
	}
	return roleLevel[userRole] >= roleLevel[minRole]
}
