package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AccessClaims struct {
	UserID      string
	Email       string
	IsAdmin     bool // is_instance_admin
	IsUserAdmin bool // is_admin user flag
}

type accessJWTClaims struct {
	jwt.RegisteredClaims
	Email       string `json:"email"`
	IsAdmin     bool   `json:"is_admin"`
	IsUserAdmin bool   `json:"is_user_admin"`
	Type        string `json:"type"`
}

type refreshJWTClaims struct {
	jwt.RegisteredClaims
	Type string `json:"type"`
}

func IssueAccessToken(key []byte, userID, email string, isAdmin, isUserAdmin bool, ttl time.Duration) (string, error) {
	claims := accessJWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		},
		Email:       email,
		IsAdmin:     isAdmin,
		IsUserAdmin: isUserAdmin,
		Type:        "access",
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
}

func VerifyAccessToken(key []byte, tokenStr string) (*AccessClaims, error) {
	var claims accessJWTClaims
	_, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return key, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}
	if claims.Type != "access" {
		return nil, errors.New("not an access token")
	}
	return &AccessClaims{
		UserID:      claims.Subject,
		Email:       claims.Email,
		IsAdmin:     claims.IsAdmin,
		IsUserAdmin: claims.IsUserAdmin,
	}, nil
}

func IssueRefreshToken(key []byte, userID string, ttl time.Duration) (string, error) {
	claims := refreshJWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		},
		Type: "refresh",
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
}

func VerifyRefreshToken(key []byte, tokenStr string) (string, error) {
	var claims refreshJWTClaims
	_, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return key, nil
	})
	if err != nil {
		return "", fmt.Errorf("parse refresh token: %w", err)
	}
	if claims.Type != "refresh" {
		return "", errors.New("not a refresh token")
	}
	return claims.Subject, nil
}
