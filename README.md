# Ello Go Salesforce packages

```shell
go get github.com/ellogroup/ello-golang-salesforce/v2
```

```go
import "github.com/ellogroup/ello-golang-salesforce/v2/salesforce"
```

Upgrading from v1? See [Migrating from v1](#migrating-from-v1).

## Token Cache

`salesforce.TokenCache` keeps a Salesforce auth token cached in memory (using `ello-golang-cache/v2`'s `cachefunc`). It
requires an implementation of `salesforce.HTTPClient` to make http requests, `secretsmanager.Client` and secrets manager
key to fetch the details required to build the Salesforce auth token, and an optional back-off policy if it encounters
any errors. If the back-off policy is excluded it will default to an exponential back-off policy.

- The first token is fetched when the cache is created. If that fails, the error is logged and the token is fetched on
  the first `Get` instead.
- The token is cached for 58 minutes, then fetched again on the first `Get` after it expires, so that call waits for it.
- Only one fetch runs at a time: concurrent `Get` calls share it.
- A `Get` call waits only as long as its `ctx` allows. The fetch carries on and caches the token for later calls.
- Failed fetches are not cached, so the next `Get` tries again.

```go
// Example

tc, err := salesforce.NewTokenCache(ctx, salesforce.TokenParams{
    HTTPClient: httpClient,
    SMClient:   smClient,
    SMKey:      "SALESFORCE_AUTH_CREDS",
    Logger:     logger, // optional *slog.Logger
})

token, err := tc.Get(ctx)
```

`NewTokenCache` reads the credentials from Secrets Manager and fetches the first token using `ctx`.

To log token fetches, set `TokenParams.Logger`. Fetches are logged with the context of the `Get` call that started
them, so context attributes such as trace IDs are included by slog handlers that read them.

`TokenFetcher.Fetch` retries with the back-off policy until it succeeds or its `ctx` is done, and sends its token
requests with that `ctx`.

## Request Helper

`salesforce.RequestHelper` is a helper for making requests to Salesforce. It holds a http client, auth token 
cache/fetcher, and details of the Salesforce base url and api version.

Every helper sends its request with the `context.Context` it is given, so cancelling the context (or reaching its
deadline) aborts the request, and context values such as OpenTelemetry trace context reach the http client. Requests
running after the caller's own work has finished (e.g. in a goroutine after an HTTP response is sent) should use a
context that is not cancelled with it, such as `context.WithoutCancel(ctx)`.

### Query Helper

The `salesforce.Query` function takes a `salesforce.RequestHelper` and a Salesforce query and returns a 
`salesforce.QueryResponse` which includes the success of the query and a slice of results.

### Patch Helper

The `salesforce.Patch` function takes a `salesforce.RequestHelper`, the name of the object type, the id of the object 
and the object entity, and updates the record in Salesforce.

## Migrating from v1

v2 removes zap, adds a `ctx` to the constructors, and renames identifiers to Go style. JSON field names sent to and
received from Salesforce are unchanged.

| v1 | v2 |
|---|---|
| `github.com/ellogroup/ello-golang-salesforce/salesforce` | `github.com/ellogroup/ello-golang-salesforce/v2/salesforce` |
| `NewTokenCache(p)` | `NewTokenCache(ctx, p)` |
| `NewTokenCacheWithLogger(p, zapLogger)` | `NewTokenCache(ctx, p)` with `p.Logger` set to a `*slog.Logger` |
| `NewTokenFetcher(p)` | `NewTokenFetcher(ctx, p)` |
| `TokenParams.HttpClient` / `HttpClient` | `TokenParams.HTTPClient` / `HTTPClient` |
| `TokenParams.Backoff` (`cenkalti/backoff/v4`) | `TokenParams.Backoff` (`cenkalti/backoff/v7`) |
| `PostResponse.Id` | `PostResponse.ID` |
| `Attributes.Url` | `Attributes.URL` |

Behaviour changes from v1:

- The token is fetched on the first `Get` after it expires rather than refreshed in the background, so about once an
  hour one `Get` waits for it.
- Request helpers, `TokenFetcher.Fetch` and `TokenCache.Get` respect `ctx`: a cancelled or expired `ctx` aborts the
  call, and a `nil` `ctx` is an error.
