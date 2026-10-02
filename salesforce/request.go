package salesforce

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type TokenGetter interface {
	Get(ctx context.Context) (string, error)
}

// HTTPClient sends http requests, e.g. *http.Client.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// RequestHelper a helper struct for sending requests to salesforce
// for more on this see https://ellogroup.atlassian.net/wiki/spaces/EP/pages/13402137/Salesforce+Package
type RequestHelper struct {
	tokenGetter TokenGetter
	client      HTTPClient
	baseURL     string
	apiVersion  int
}

func NewRequestHelper(client HTTPClient, tg TokenGetter, baseURL string, apiVersion int) (*RequestHelper, error) {
	if len(baseURL) == 0 {
		return nil, fmt.Errorf("baseURL needs to be provided")
	}
	if apiVersion <= 0 {
		return nil, fmt.Errorf("salesfore apiVersion needs to be provided")
	}
	if tg == nil {
		return nil, fmt.Errorf("tokenGetter needs to be provided")
	}
	return &RequestHelper{
		tokenGetter: tg,
		client:      client,
		baseURL:     baseURL,
		apiVersion:  apiVersion,
	}, nil
}

type QueryError struct {
	queryUsed  string
	statusCode int
}

func (q QueryError) Error() string {
	return fmt.Sprintf("error querying salesforce - status code: %v, query: %v", q.statusCode, q.queryUsed)
}

// Query salesforce in a generic way
// - uses the baseURL, tokenGetter and http client on RequestHelper to query salesforce
// - the request is sent with ctx, so it is cancelled with ctx and carries any values on it (e.g. trace context)
// - QueryError returned if status code != 200 with status code of response
func Query[E any](ctx context.Context, h *RequestHelper, q string) (*QueryResponse[E], error) {
	reqURL := fmt.Sprintf("%s/services/data/v%d.0/query?q=%s", h.baseURL, h.apiVersion, url.QueryEscape(q))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("unable to create salesforce request: %w", err)
	}

	token, err := h.tokenGetter.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to create salesforce auth token: %w", err)
	}
	req.Header = http.Header{
		"Content-Type":  {"application/json"},
		"Authorization": {"Bearer " + token},
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("unable to send request to salesforce: %w", err)
	}
	defer closeBody(resp)
	if resp.StatusCode != 200 {
		return nil, QueryError{statusCode: resp.StatusCode, queryUsed: q}
	}
	resBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var parsedResp *QueryResponse[E]
	if err = json.Unmarshal(resBody, &parsedResp); err != nil {
		return nil, err
	}
	return parsedResp, nil
}

// Post sends a post request to salesforce to create an object
// - uses the baseURL, tokenGetter and http client on RequestHelper
// - the request is sent with ctx, so it is cancelled with ctx and carries any values on it (e.g. trace context)
// - returns the id of the newly created object
func Post(ctx context.Context, h *RequestHelper, name string, record any) (string, error) {
	reqURL := fmt.Sprintf("%s/services/data/v%d.0/sobjects/%s", h.baseURL, h.apiVersion, name)

	reqBody, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("unable to create salesforce payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("unable to create salesforce request: %w", err)
	}
	token, err := h.tokenGetter.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to create salesforce auth token: %w", err)
	}
	req.Header = http.Header{
		"Content-Type":  {"application/json"},
		"Authorization": {"Bearer " + token},
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("unable to send request to salesforce: %w", err)
	}
	defer closeBody(resp)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("unexpected salesforce response code: %d", resp.StatusCode)
	}

	resBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("unable to parse response body: %w", err)
	}

	var parsedResp *PostResponse
	if err = json.Unmarshal(resBody, &parsedResp); err != nil {
		return "", err
	}

	if !parsedResp.Success {
		return "", fmt.Errorf("salesforce returns a failure result: %s", resBody)
	}

	return parsedResp.ID, nil
}

// Patch sends a patch request to salesforce to update an object
// - uses the baseURL, tokenGetter and http client on RequestHelper to query salesforce
// - the request is sent with ctx, so it is cancelled with ctx and carries any values on it (e.g. trace context)
// - returns the status code in the response, as patch requests could result in 200, 201 or 204
func Patch(ctx context.Context, h *RequestHelper, name, id string, record any) (int, error) {
	reqURL := fmt.Sprintf("%s/services/data/v%d.0/sobjects/%s/%s", h.baseURL, h.apiVersion, name, id)

	reqBody, err := json.Marshal(record)
	if err != nil {
		return 0, fmt.Errorf("unable to create salesforce payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return 0, fmt.Errorf("unable to create salesforce request: %w", err)
	}

	token, err := h.tokenGetter.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("unable to create salesforce auth token: %w", err)
	}
	req.Header = http.Header{
		"Content-Type":  {"application/json"},
		"Authorization": {"Bearer " + token},
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("unable to send request to salesforce: %w", err)
	}
	defer closeBody(resp)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("unexpected salesforce response code: %d", resp.StatusCode)
	}

	return resp.StatusCode, nil
}

// Delete sends a delete request to salesforce to delete an object
// - uses the baseURL, tokenGetter and http client on RequestHelper
// - the request is sent with ctx, so it is cancelled with ctx and carries any values on it (e.g. trace context)
func Delete(ctx context.Context, h *RequestHelper, name, id string) error {
	reqURL := fmt.Sprintf("%s/services/data/v%d.0/sobjects/%s/%s", h.baseURL, h.apiVersion, name, id)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err != nil {
		return fmt.Errorf("unable to create salesforce request: %w", err)
	}

	token, err := h.tokenGetter.Get(ctx)
	if err != nil {
		return fmt.Errorf("unable to create salesforce auth token: %w", err)
	}
	req.Header = http.Header{
		"Content-Type":  {"application/json"},
		"Authorization": {"Bearer " + token},
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("unable to send request to salesforce: %w", err)
	}
	defer closeBody(resp)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("unexpected salesforce response code: %d", resp.StatusCode)
	}

	return nil
}

// closeBody closes the response body so the underlying connection can be reused. It is closed on every response,
// including error status codes, which previously leaked the connection.
func closeBody(resp *http.Response) {
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
}
