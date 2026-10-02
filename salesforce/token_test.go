package salesforce

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fetcherStub is a tokenFetcher returning the configured results in order, optionally blocking until released.
type fetcherStub struct {
	calls   atomic.Int32
	results []fetchResult
	release chan struct{}
	started chan struct{}
}

type fetchResult struct {
	tok string
	err error
}

func (f *fetcherStub) Fetch(_ context.Context) (string, error) {
	n := int(f.calls.Add(1))
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.release != nil {
		<-f.release
	}
	r := f.results[min(n, len(f.results))-1]
	return r.tok, r.err
}

func newTestTokenCache(f tokenFetcher, ttl time.Duration) *TokenCache {
	return newTokenCacheWithFetcher(f, ttl, slog.New(slog.DiscardHandler))
}

func TestTokenCache_Get_CachesToken(t *testing.T) {
	f := &fetcherStub{results: []fetchResult{{tok: "token1"}, {tok: "token2"}}}
	tc := newTestTokenCache(f, time.Hour)

	for range 3 {
		tok, err := tc.Get(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "token1", tok)
	}
	assert.Equal(t, int32(1), f.calls.Load())
}

func TestTokenCache_Get_FetchesAgainOnceExpired(t *testing.T) {
	f := &fetcherStub{results: []fetchResult{{tok: "token1"}, {tok: "token2"}}}
	tc := newTestTokenCache(f, 10*time.Millisecond)

	tok, err := tc.Get(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "token1", tok)

	time.Sleep(20 * time.Millisecond)

	tok, err = tc.Get(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "token2", tok)
	assert.Equal(t, int32(2), f.calls.Load())
}

// A failed fetch must not be cached or delay the next attempt: the next Get fetches again.
func TestTokenCache_Get_DoesNotCacheFailures(t *testing.T) {
	f := &fetcherStub{results: []fetchResult{{err: errors.New("salesforce unavailable")}, {tok: "token"}}}
	tc := newTestTokenCache(f, time.Hour)

	_, err := tc.Get(context.Background())
	require.Error(t, err)

	tok, err := tc.Get(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "token", tok)
	assert.Equal(t, int32(2), f.calls.Load())
}

func TestTokenCache_Get_ConcurrentCallsShareOneFetch(t *testing.T) {
	f := &fetcherStub{
		results: []fetchResult{{tok: "token"}},
		release: make(chan struct{}),
		started: make(chan struct{}, 10),
	}
	tc := newTestTokenCache(f, time.Hour)

	const callers = 10
	var wg sync.WaitGroup
	toks := make([]string, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Go(func() {
			toks[i], errs[i] = tc.Get(context.Background())
		})
	}

	<-f.started
	// Give the other callers time to join the in-flight fetch before releasing it.
	time.Sleep(20 * time.Millisecond)
	close(f.release)
	wg.Wait()

	assert.Equal(t, int32(1), f.calls.Load())
	for i := range callers {
		require.NoError(t, errs[i])
		assert.Equal(t, "token", toks[i])
	}
}

// A caller whose ctx is done stops waiting, but the fetch completes and caches the token for the next caller.
func TestTokenCache_Get_CallerContextCancelled(t *testing.T) {
	f := &fetcherStub{
		results: []fetchResult{{tok: "token"}},
		release: make(chan struct{}),
		started: make(chan struct{}, 1),
	}
	tc := newTestTokenCache(f, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := tc.Get(ctx)
		errCh <- err
	}()

	<-f.started
	cancel()
	assert.ErrorIs(t, <-errCh, context.Canceled)

	close(f.release)
	assert.Eventually(t, func() bool {
		tok, err := tc.Get(context.Background())
		return err == nil && tok == "token"
	}, time.Second, 5*time.Millisecond)
	assert.Equal(t, int32(1), f.calls.Load())
}

type tokenCtxKey struct{}

func newTestTokenFetcher(t *testing.T, client HTTPClient) TokenFetcher {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return TokenFetcher{
		httpClient: client,
		cfg:        tokenFetcherCfg{BaseURL: "https://salesforce.example", privateKey: pemKey},
		backoff:    &backoff.StopBackOff{},
	}
}

func TestTokenFetcher_Fetch_SendsRequestsWithContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), tokenCtxKey{}, "value")

	httpClient := new(HTTPClientMock)
	withCtx := mock.MatchedBy(func(req *http.Request) bool { return req.Context().Value(tokenCtxKey{}) == "value" })
	httpClient.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return strings.HasSuffix(req.URL.Path, "/oauth2/token")
	})).Return(&http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"token"}`))}, nil).Once()
	httpClient.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return strings.HasSuffix(req.URL.Path, "/oauth2/introspect")
	})).Return(&http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil).Once()

	tok, err := newTestTokenFetcher(t, httpClient).Fetch(ctx)

	require.NoError(t, err)
	assert.Equal(t, "token", tok)
	httpClient.AssertNumberOfCalls(t, "Do", 2)
	for _, c := range httpClient.Calls {
		req, _ := c.Arguments.Get(0).(*http.Request)
		assert.True(t, withCtx.Matches(req), "request %s sent with caller's context", req.URL.Path)
	}
}

func TestTokenFetcher_Fetch_StopsRetryingWhenContextDone(t *testing.T) {
	httpClient := new(HTTPClientMock)
	httpClient.On("Do", mock.Anything).Return(&http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(`{}`))}, nil)
	tf := newTestTokenFetcher(t, httpClient)
	tf.backoff = backoff.NewConstantBackOff(time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := tf.Fetch(ctx)
		done <- err
	}()

	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("Fetch kept retrying after ctx was done")
	}
}

// A caller can find the cache empty, then reach fetch only after another fetch has cached a token and finished. fetch
// must use that token rather than fetch again.
func TestTokenCache_fetch_UsesTokenCachedByEarlierFetch(t *testing.T) {
	f := &fetcherStub{results: []fetchResult{{tok: "new-token"}}}
	tc := newTestTokenCache(f, time.Hour)
	_, err := tc.cache.Get(tokenCacheKey, func() (string, error) { return "cached-token", nil }, time.Hour)
	require.NoError(t, err)

	tok, err := tc.fetch(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "cached-token", tok)
	assert.Equal(t, int32(0), f.calls.Load())
}
