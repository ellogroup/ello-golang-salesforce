package salesforce

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/cenkalti/backoff/v4"
	"github.com/ellogroup/ello-golang-cache/cache"
	"github.com/ellogroup/ello-golang-cache/driver"
	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"io"
	"net/http"
	"net/url"
	"time"
)

const tokenTtl = 1 * time.Hour
const tokenCacheTtl = 58 * time.Minute

type TokenParams struct {
	HttpClient HttpClient             `validate:"required"`
	SMClient   *secretsmanager.Client `validate:"required"`
	SMKey      string                 `validate:"required"`
	Backoff    backoff.BackOff
}

type TokenFetcher struct {
	httpClient HttpClient
	cfg        tokenFetcherCfg
	backoff    backoff.BackOff
}

type tokenFetcherCfg struct {
	BaseUrl          string `json:"baseUrl"`
	Hostname         string `json:"hostname"`
	Username         string `json:"username"`
	ClientId         string `json:"clientId"`
	ClientSecret     string `json:"clientSecret"`
	PrivateKeyBase64 string `json:"privateKeyBase64"`
	privateKey       []byte
}

func NewTokenFetcher(p TokenParams) (*TokenFetcher, error) {
	if err := validateTokenParams(p); err != nil {
		return nil, err
	}

	cfgRaw, err := p.SMClient.GetSecretValue(context.Background(), &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(p.SMKey),
	})
	if err != nil {
		return nil, fmt.Errorf("unable to fetch credentials from secrets manager: %w", err)
	}

	cfg := tokenFetcherCfg{}
	if err := json.Unmarshal([]byte(*cfgRaw.SecretString), &cfg); err != nil {
		return nil, fmt.Errorf("unable to parse credentials from secrets manager: %w", err)
	}

	// Decode the PK
	cfg.privateKey, err = base64.StdEncoding.DecodeString(cfg.PrivateKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("unable to decode private key: %w", err)
	}

	// Retry Backoff
	b := p.Backoff
	if b == nil {
		// Default exponential backoff
		b = backoff.NewExponentialBackOff()
	}

	tf := &TokenFetcher{
		httpClient: p.HttpClient,
		cfg:        cfg,
		backoff:    b,
	}
	return tf, nil
}

func validateTokenParams(p TokenParams) error {
	validate := validator.New()
	if err := validate.Struct(p); err != nil {
		return err
	}
	return nil
}

type tokenResponse struct {
	Token string `json:"access_token"`
}

func (tf TokenFetcher) Fetch(_ context.Context) (string, error) {
	return backoff.RetryWithData[string](func() (string, error) {
		tok, err := tf.generateJwt()
		if err != nil {
			return "", err
		}
		return tf.obtainToken(tok)
	}, tf.backoff)
}

func (tf TokenFetcher) generateJwt() (string, error) {
	j := jwt.New(jwt.GetSigningMethod("RS256"))
	key, err := jwt.ParseRSAPrivateKeyFromPEM(tf.cfg.privateKey)
	if err != nil {
		return "", fmt.Errorf("error parsing private key %w", err)
	}
	j.Claims = struct {
		jwt.RegisteredClaims
		Aud string `json:"aud,omitempty"`
	}{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tf.cfg.ClientId,
			Subject:   tf.cfg.Username,
			ExpiresAt: jwt.NewNumericDate(time.Now().Local().Add(tokenTtl)),
			ID:        uuid.New().String(),
		},
		Aud: tf.cfg.Hostname,
	}
	tok, err := j.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("error generating salesforce token %w", err)
	}
	return tok, nil
}

func (tf TokenFetcher) obtainToken(tok string) (string, error) {
	data := url.Values{}
	data.Add("assertion", tok)
	data.Add("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	uri, _ := url.ParseRequestURI(fmt.Sprintf("%s/services/oauth2/token", tf.cfg.BaseUrl))
	uri.RawQuery = data.Encode()
	req, _ := http.NewRequest("POST", uri.String(), nil)
	req.Header = http.Header{
		"Content-Type": {"application/x-www-form-urlencoded"},
	}
	resp, err := tf.httpClient.Do(req)
	if err != nil {
		return "", err
	}

	resBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var sfRes *tokenResponse
	if err = json.Unmarshal(resBody, &sfRes); err != nil {
		return "", err
	}
	return tf.introspect(sfRes.Token)
}

func (tf TokenFetcher) introspect(token string) (string, error) {
	data := url.Values{}
	data.Add("token", token)
	data.Add("token_type_hint", "access_token")
	data.Add("client_id", tf.cfg.ClientId)
	data.Add("client_secret", tf.cfg.ClientSecret)
	uri, _ := url.ParseRequestURI(fmt.Sprintf("%s/services/oauth2/introspect", tf.cfg.BaseUrl))
	uri.RawQuery = data.Encode()
	req, _ := http.NewRequest("POST", uri.String(), nil)
	resp, err := tf.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("failed Call to introspect token: %v", resp)
	}
	defer resp.Body.Close()
	return token, nil
}

type TokenCache struct {
	c *cache.KeylessRecordCache[string]
}

// NewTokenCache creates a default implementation of a salesforce token cache
// using async type of cache.KeylessRecordCache and storing in memory with driver.NewMemoryCache
// with a ~1 hour TTL/refresh rate (slightly less to unsure token doesn't expire before cache becomes stale)
// for more info see: https://ellogroup.atlassian.net/wiki/spaces/EP/pages/13402137/Salesforce+Package#TokenFetcher-and-TokenCache
func NewTokenCache(p TokenParams) (*TokenCache, error) {
	tf, err := NewTokenFetcher(p)
	if err != nil {
		return nil, err
	}
	return &TokenCache{
		cache.NewKeylessRecordCacheAsync[string](
			driver.NewMemoryCache[int, cache.RecordCacheItem[string]](),
			tf,
			tokenCacheTtl,
		),
	}, nil
}
func NewTokenCacheWithLogger(p TokenParams, log *zap.Logger) (*TokenCache, error) {
	tf, err := NewTokenFetcher(p)
	if err != nil {
		return nil, err
	}
	return &TokenCache{
		cache.NewKeylessRecordCacheAsyncWithLogger[string](
			driver.NewMemoryCache[int, cache.RecordCacheItem[string]](),
			tf,
			tokenCacheTtl,
			log.Named("SalesforceTokenCache"),
		),
	}, nil
}

func (tc TokenCache) Get(ctx context.Context) (string, error) {
	return tc.c.Get(ctx)
}
