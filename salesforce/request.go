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

type HttpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// RequestHelper a helper struct for sending requests to salesforce
// for more on this see https://ellogroup.atlassian.net/wiki/spaces/EP/pages/13402137/Salesforce+Package
type RequestHelper struct {
	tokenGetter TokenGetter
	client      HttpClient
	baseUrl     string
	apiVersion  int
}

func NewRequestHelper(client HttpClient, tg TokenGetter, baseUrl string, apiVersion int) (*RequestHelper, error) {
	if len(baseUrl) == 0 {
		return nil, fmt.Errorf("baseUrl needs to be provided")
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
		baseUrl:     baseUrl,
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
// - uses the baseUrl, tokenGetter and http client on RequestHelper to query salesforce
// - QueryError returned if status code != 200 with status code of response
func Query[E any](ctx context.Context, h *RequestHelper, q string) (*QueryResponse[E], error) {
	reqUrl := fmt.Sprintf("%s/services/data/v%d.0/query?q=%s", h.baseUrl, h.apiVersion, url.QueryEscape(q))
	req, err := http.NewRequest(http.MethodGet, reqUrl, nil)
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
	if resp.StatusCode != 200 {
		return nil, QueryError{statusCode: resp.StatusCode, queryUsed: q}
	}
	resBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var parsedResp *QueryResponse[E]
	if err = json.Unmarshal(resBody, &parsedResp); err != nil {
		return nil, err
	}
	return parsedResp, nil
}

// Patch sends a patch request to salesforce to update a resource
// - uses the baseUrl, tokenGetter and http client on RequestHelper to query salesforce
// - returns the status code in the response, as patch requests could result in 200, 201 or 204
func Patch(ctx context.Context, h *RequestHelper, name, id string, record any) (int, error) {
	reqUrl := fmt.Sprintf("%s/services/data/v%d.0/sobjects/%s/%s", h.baseUrl, h.apiVersion, name, id)

	reqBody, err := json.Marshal(record)
	if err != nil {
		return 0, fmt.Errorf("unable to create salesforce payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPatch, reqUrl, bytes.NewReader(reqBody))
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

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("unexpected salesforce response code: %d", resp.StatusCode)
	}

	return resp.StatusCode, nil
}
