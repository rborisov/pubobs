package auth

import (
	"context"
	"fmt"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// AuthProvider is implemented by both OIDCClient and YandexClient.
type AuthProvider interface {
	AuthCodeURL(state string) string
	ExchangeCode(ctx context.Context, code string) (*UserClaims, error)
}

// NamedProvider wraps an AuthProvider with a display ID and human-readable name.
type NamedProvider struct {
	ID     string
	Name   string
	Client AuthProvider
}

// UserClaims holds the identity fields extracted from an OIDC ID token.
type UserClaims struct {
	Subject string
	Email   string
	Name    string
}

// OIDCClient wraps go-oidc for the PKCE flow.
type OIDCClient struct {
	provider *gooidc.Provider
	oauth2   oauth2.Config
}

// NewOIDCClient discovers the OIDC provider at issuer and returns a configured client.
func NewOIDCClient(ctx context.Context, issuer, clientID, clientSecret, baseURL string) (*OIDCClient, error) {
	provider, err := gooidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider %q: %w", issuer, err)
	}
	return &OIDCClient{
		provider: provider,
		oauth2: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  baseURL + "/auth/callback",
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{gooidc.ScopeOpenID, "email", "profile"},
		},
	}, nil
}

// AuthCodeURL returns the OIDC authorization URL with the given state.
func (c *OIDCClient) AuthCodeURL(state string) string {
	return c.oauth2.AuthCodeURL(state)
}

// ExchangeCode exchanges an OIDC authorization code for validated user claims.
func (c *OIDCClient) ExchangeCode(ctx context.Context, code string) (*UserClaims, error) {
	token, err := c.oauth2.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("missing id_token in response")
	}
	verifier := c.provider.Verifier(&gooidc.Config{ClientID: c.oauth2.ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("verify id_token: %w", err)
	}
	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}
	return &UserClaims{
		Subject: idToken.Subject,
		Email:   claims.Email,
		Name:    claims.Name,
	}, nil
}
