# Ello Go Salesforce packages

## Token Cache

`salesforce.TokenCache` utilises the `cache.KeylessRecordCache` to keep an active Salesforce auth token available at all
times. It requires an implementation of `salesforce.HttpClient` to make http requests, `secretsmanager.Client` and 
secrets manager key to fetch the details required to build the Salesforce auth token, and an optional back-off policy if 
it encounters any errors. If the back-off policy is excluded it will default to an exponential back-off policy.

The token will be refreshed every hour.

```go
// Example

tc := salesforce.NewTokenCache(TokenParams{
    HttpClient: httpClient,
    SMClient: smClient,
    SMKey: "SALESFORCE_AUTH_CREDS",
})

token, err := tc.Get(ctx)
```

To log the cache's activity, use `NewTokenCacheWithLogger` with a `*zap.Logger`, or `NewTokenCacheWithSlogLogger`
with a `*slog.Logger`. Entries logged by the cache do not carry a context, so context attributes (such as trace IDs)
are not added to them.

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