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

## Request Helper

`salesforce.RequestHelper` is a helper for making requests to Salesforce. It holds a http client, auth token 
cache/fetcher, and details of the Salesforce base url and api version.

### Query Helper

The `salesforce.Query` function takes a `salesforce.RequestHelper` and a Salesforce query and returns a 
`salesforce.QueryResponse` which includes the success of the query and a slice of results.

### Patch Helper

The `salesforce.Patch` function takes a `salesforce.RequestHelper`, the name of the object type, the id of the object 
and the object entity, and updates the record in Salesforce.