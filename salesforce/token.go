package salesforce

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/cenkalti/backoff/v7"
	"github.com/ellogroup/ello-golang-cache/v2/cachefunc"
	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const tokenTTL = 1 * time.Hour
const tokenCacheTTL = 58 * time.Minute

// TokenParams configures a TokenFetcher or TokenCache.
type TokenParams struct {
	HTTPClient HTTPClient             `validate:"required"`
	SMClient   *secretsmanager.Client `validate:"required"`
	SMKey      string                 `validate:"required"`
	// Backoff is the retry policy for fetching a token. Defaults to an exponential back-off.
	Backoff backoff.BackOff
	// Logger logs TokenCache token fetches. Optional; nothing is logged when nil.
	Logger *slog.Logger
}

type TokenFetcher struct {
	httpClient HTTPClient
	cfg        tokenFetcherCfg
	backoff    backoff.BackOff
}

type tokenFetcherCfg struct {
	BaseURL          string `json:"baseUrl"`
	Hostname         string `json:"hostname"`
	Username         string `json:"username"`
	ClientID         string `json:"clientId"`
	ClientSecret     string `json:"clientSecret"`
	PrivateKeyBase64 string `json:"privateKeyBase64"`
	privateKey       []byte
}

// NewTokenFetcher creates a TokenFetcher, reading the Salesforce credentials from Secrets Manager with ctx.
func NewTokenFetcher(ctx context.Context, p TokenParams) (*TokenFetcher, error) {
	if err := validateTokenParams(p); err != nil {
		return nil, err
	}

	cfgRaw, err := p.SMClient.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
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
		httpClient: p.HTTPClient,
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

// Fetch generates a new Salesforce auth token, retrying with the back-off policy until it succeeds or ctx is done.
func (tf TokenFetcher) Fetch(ctx context.Context) (string, error) {
	return backoff.Retry(ctx, func() (string, error) {
		tok, err := tf.generateJwt()
		if err != nil {
			return "", err
		}
		return tf.obtainToken(ctx, tok)
	}, backoff.WithBackOff(tf.backoff))
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
			Issuer:    tf.cfg.ClientID,
			Subject:   tf.cfg.Username,
			ExpiresAt: jwt.NewNumericDate(time.Now().Local().Add(tokenTTL)),
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

func (tf TokenFetcher) obtainToken(ctx context.Context, tok string) (string, error) {
	data := url.Values{}
	data.Add("assertion", tok)
	data.Add("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	uri, _ := url.ParseRequestURI(fmt.Sprintf("%s/services/oauth2/token", tf.cfg.BaseURL))
	uri.RawQuery = data.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, uri.String(), nil)
	req.Header = http.Header{
		"Content-Type": {"application/x-www-form-urlencoded"},
	}
	resp, err := tf.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer closeBody(resp)

	resBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var sfRes *tokenResponse
	if err = json.Unmarshal(resBody, &sfRes); err != nil {
		return "", err
	}
	return tf.introspect(ctx, sfRes.Token)
}

func (tf TokenFetcher) introspect(ctx context.Context, token string) (string, error) {
	data := url.Values{}
	data.Add("token", token)
	data.Add("token_type_hint", "access_token")
	data.Add("client_id", tf.cfg.ClientID)
	data.Add("client_secret", tf.cfg.ClientSecret)
	uri, _ := url.ParseRequestURI(fmt.Sprintf("%s/services/oauth2/introspect", tf.cfg.BaseURL))
	uri.RawQuery = data.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, uri.String(), nil)
	resp, err := tf.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer closeBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("failed Call to introspect token: %v", resp)
	}
	return token, nil
}

// tokenFetcher fetches a new Salesforce auth token. Implemented by TokenFetcher.
type tokenFetcher interface {
	Fetch(ctx context.Context) (string, error)
}

const (
	tokenCacheKey        = 0
	tokenCacheLoggerName = "SalesforceTokenCache"
)

// TokenCache keeps a Salesforce auth token cached for ~1 hour, fetching a new one on the first Get after it expires.
//   - Only one fetch runs at a time: concurrent Get calls while the token is being fetched share that fetch.
//   - A Get call waits for the fetch only as long as its ctx allows; the fetch carries on for other callers and
//     caches its result.
//   - Failed fetches are not cached, so the next Get fetches again.
type TokenCache struct {
	fetcher tokenFetcher
	cache   cachefunc.CacheFunc[int, string]
	ttl     time.Duration
	fetches *singleflight.Group
	log     *slog.Logger
}

// NewTokenCache creates a salesforce token cache, storing the token in memory using ello-golang-cache's cachefunc with
// a ~1 hour TTL (slightly less to ensure the token doesn't expire while cached). The credentials are read and the
// first token fetched with ctx before returning; if the first fetch fails it is logged and retried on the first Get.
// Token fetches are logged to p.Logger, if set.
// for more info see: https://ellogroup.atlassian.net/wiki/spaces/EP/pages/13402137/Salesforce+Package#TokenFetcher-and-TokenCache
func NewTokenCache(ctx context.Context, p TokenParams) (*TokenCache, error) {
	tf, err := NewTokenFetcher(ctx, p)
	if err != nil {
		return nil, err
	}
	log := slog.New(slog.DiscardHandler)
	if p.Logger != nil {
		log = p.Logger.With(slog.String("logger", tokenCacheLoggerName))
	}
	tc := newTokenCacheWithFetcher(tf, tokenCacheTTL, log)
	if _, err := tc.Get(ctx); err != nil {
		// Not returned, so services still start while Salesforce auth is unavailable; the next Get tries again.
		log.ErrorContext(ctx, "Unable to fetch initial Salesforce token, it will be fetched on the next request", slog.Any("error", err))
	}
	return tc, nil
}

func newTokenCacheWithFetcher(f tokenFetcher, ttl time.Duration, log *slog.Logger) *TokenCache {
	return &TokenCache{
		fetcher: f,
		cache:   cachefunc.NewMemory[int, string](),
		ttl:     ttl,
		fetches: &singleflight.Group{},
		log:     log,
	}
}

// Get returns the cached token, fetching a new one if it has expired.
func (tc TokenCache) Get(ctx context.Context) (string, error) {
	return tc.cache.Get(tokenCacheKey, func() (string, error) {
		return tc.fetch(ctx)
	}, tc.ttl)
}

// fetch fetches a new token, sharing a fetch already in progress, and waits for it for as long as ctx allows.
func (tc TokenCache) fetch(ctx context.Context) (string, error) {
	res := tc.fetches.DoChan("token", func() (any, error) {
		// Not cancelled with this caller's ctx, as other callers may be waiting on the same fetch.
		fetchCtx := context.WithoutCancel(ctx)
		tc.log.InfoContext(fetchCtx, "Fetching Salesforce token")
		tok, err := tc.fetcher.Fetch(fetchCtx)
		if err != nil {
			tc.log.ErrorContext(fetchCtx, "Unable to fetch Salesforce token", slog.Any("error", err))
			return "", err
		}
		// Cache the token here too, so it is kept even if every waiting caller has given up.
		_, _ = tc.cache.Get(tokenCacheKey, func() (string, error) { return tok, nil }, tc.ttl)
		tc.log.InfoContext(fetchCtx, "Fetched Salesforce token")
		return tok, nil
	})

	select {
	case <-ctx.Done():
		return "", fmt.Errorf("waiting for salesforce token: %w", ctx.Err())
	case r := <-res:
		if r.Err != nil {
			return "", r.Err
		}
		tok, _ := r.Val.(string)
		return tok, nil
	}
}
