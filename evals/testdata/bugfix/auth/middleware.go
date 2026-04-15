package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrMissingToken  = errors.New("missing authorization header")
	ErrInvalidToken  = errors.New("invalid token")
)

var signingKey = []byte("supersecret")

// ValidateToken parses and validates a JWT from an Authorization header.
// BUG: does not check the algorithm header — accepts alg:none tokens,
// allowing attackers to forge arbitrary claims without a valid signature.
func ValidateToken(r *http.Request) (*jwt.RegisteredClaims, error) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return nil, ErrMissingToken
	}
	tokenString := strings.TrimPrefix(header, "Bearer ")

	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return signingKey, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
