package salesforce

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/cenkalti/backoff/v4"
	"github.com/ellogroup/ello-golang-cache/v2/cachefunc"
	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"
	"golang.org/x/sync/singleflight"
	"io"
	"log/slog"
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

// Fetch generates a new Salesforce auth token, retrying with the back-off policy until it succeeds or ctx is done.
func (tf TokenFetcher) Fetch(ctx context.Context) (string, error) {
	return backoff.RetryWithData[string](func() (string, error) {
		tok, err := tf.generateJwt()
		if err != nil {
			return "", err
		}
		return tf.obtainToken(ctx, tok)
	}, backoff.WithContext(tf.backoff, ctx))
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

func (tf TokenFetcher) obtainToken(ctx context.Context, tok string) (string, error) {
	data := url.Values{}
	data.Add("assertion", tok)
	data.Add("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	uri, _ := url.ParseRequestURI(fmt.Sprintf("%s/services/oauth2/token", tf.cfg.BaseUrl))
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
	data.Add("client_id", tf.cfg.ClientId)
	data.Add("client_secret", tf.cfg.ClientSecret)
	uri, _ := url.ParseRequestURI(fmt.Sprintf("%s/services/oauth2/introspect", tf.cfg.BaseUrl))
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

// NewTokenCache creates a default implementation of a salesforce token cache, storing the token in memory using
// ello-golang-cache's cachefunc with a ~1 hour TTL (slightly less to ensure the token doesn't expire while cached).
// The first token is fetched before returning; if that fails it is fetched again on the first Get.
// for more info see: https://ellogroup.atlassian.net/wiki/spaces/EP/pages/13402137/Salesforce+Package#TokenFetcher-and-TokenCache
func NewTokenCache(p TokenParams) (*TokenCache, error) {
	return NewTokenCacheWithSlogLogger(p, slog.New(slog.DiscardHandler))
}

// NewTokenCacheWithLogger creates the same token cache as NewTokenCache, logging token fetches to log.
func NewTokenCacheWithLogger(p TokenParams, log *zap.Logger) (*TokenCache, error) {
	return newTokenCache(p, zapToSlog(log))
}

// NewTokenCacheWithSlogLogger creates the same token cache as NewTokenCache, logging token fetches to log.
func NewTokenCacheWithSlogLogger(p TokenParams, log *slog.Logger) (*TokenCache, error) {
	return newTokenCache(p, log.With(slog.String("logger", tokenCacheLoggerName)))
}

func newTokenCache(p TokenParams, log *slog.Logger) (*TokenCache, error) {
	tf, err := NewTokenFetcher(p)
	if err != nil {
		return nil, err
	}
	tc := newTokenCacheWithFetcher(tf, tokenCacheTtl, log)
	if _, err := tc.Get(context.Background()); err != nil {
		// Not returned, so services still start while Salesforce auth is unavailable; the next Get tries again.
		log.Error("Unable to fetch initial Salesforce token, it will be fetched on the next request", slog.Any("error", err))
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

// zapToSlog converts a zap logger to slog for NewTokenCacheWithLogger, naming it as the previous zap-based cache did.
func zapToSlog(log *zap.Logger) *slog.Logger {
	name := tokenCacheLoggerName
	if log.Name() != "" {
		name = log.Name() + "." + name
	}
	return slog.New(zapslog.NewHandler(log.Core(), zapslog.WithName(name)))
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
