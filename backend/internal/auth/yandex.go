package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	yandexAuthURL  = "https://oauth.yandex.ru/authorize"
	yandexTokenURL = "https://oauth.yandex.ru/token"
	yandexInfoURL  = "https://login.yandex.ru/info?format=json"
)

// YandexClient implements AuthProvider using Yandex OAuth2 + user-info API.
type YandexClient struct {
	clientID     string
	clientSecret string
	redirectURL  string
}

func NewYandexClient(clientID, clientSecret, baseURL string) *YandexClient {
	return &YandexClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  baseURL + "/auth/callback",
	}
}

func (c *YandexClient) AuthCodeURL(state string) string {
	params := url.Values{
		"response_type": {"code"},
		"client_id":     {c.clientID},
		"redirect_uri":  {c.redirectURL},
		"state":         {state},
	}
	return yandexAuthURL + "?" + params.Encode()
}

func (c *YandexClient) ExchangeCode(ctx context.Context, code string) (*UserClaims, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"redirect_uri":  {c.redirectURL},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, yandexTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("yandex token request: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("yandex token decode: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("yandex token error: %s: %s", tokenResp.Error, tokenResp.ErrorDesc)
	}

	infoReq, err := http.NewRequestWithContext(ctx, http.MethodGet, yandexInfoURL, nil)
	if err != nil {
		return nil, err
	}
	infoReq.Header.Set("Authorization", "OAuth "+tokenResp.AccessToken)

	infoResp, err := http.DefaultClient.Do(infoReq)
	if err != nil {
		return nil, fmt.Errorf("yandex info request: %w", err)
	}
	defer infoResp.Body.Close()

	var info struct {
		ID           string `json:"id"`
		Login        string `json:"login"`
		RealName     string `json:"real_name"`
		DisplayName  string `json:"display_name"`
		DefaultEmail string `json:"default_email"`
	}
	if err := json.NewDecoder(infoResp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("yandex info decode: %w", err)
	}

	name := info.RealName
	if name == "" {
		name = info.DisplayName
	}
	if name == "" {
		name = info.Login
	}

	return &UserClaims{
		Subject: "yandex:" + info.ID,
		Email:   info.DefaultEmail,
		Name:    name,
	}, nil
}
