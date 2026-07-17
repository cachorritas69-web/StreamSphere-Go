package common

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

const claimsContextKey contextKey = "claims"

func GenerateToken(secret, userID, username, role string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Issuer:    "streamsphere-auth",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func ParseToken(secret, raw string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(raw, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("algoritmo de firma inválido")
		}
		return []byte(secret), nil
	}, jwt.WithIssuer("streamsphere-auth"))
	if err != nil || !parsed.Valid {
		return nil, errors.New("token inválido o expirado")
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok {
		return nil, errors.New("claims inválidos")
	}
	return claims, nil
}

func BearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return ""
	}
	return strings.TrimSpace(header[7:])
}

func Authenticate(secret string, required bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := BearerToken(r)
			if raw == "" {
				if required {
					Fail(w, http.StatusUnauthorized, "AUTH_REQUIRED", "Debes iniciar sesión")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			claims, err := ParseToken(secret, raw)
			if err != nil {
				if required {
					Fail(w, http.StatusUnauthorized, "INVALID_TOKEN", err.Error())
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			ctx := context.WithValue(r.Context(), claimsContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func ClaimsFromRequest(r *http.Request) (*Claims, bool) {
	claims, ok := r.Context().Value(claimsContextKey).(*Claims)
	return claims, ok
}

func HasRole(claims *Claims, roles ...string) bool {
	if claims == nil {
		return false
	}
	for _, role := range roles {
		if claims.Role == role {
			return true
		}
	}
	return false
}
