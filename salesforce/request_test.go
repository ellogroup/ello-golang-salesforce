package salesforce

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"io"
	"net/http"
	"strings"
	"testing"
)

type recordStub struct {
	Attributes Attributes `json:"attributes"`
	Foo        string     `json:"foo"`
}

type HttpClientMock struct {
	mock.Mock
}

func (m *HttpClientMock) Do(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	r := args.Get(0).(*http.Response)
	return r, args.Error(1)
}

func newHttpClientMock(resp *http.Response, err error) *HttpClientMock {
	m := new(HttpClientMock)
	m.On("Do", mock.Anything).Return(resp, err)
	return m
}

type TokenGetterMock struct {
	mock.Mock
}

func (m *TokenGetterMock) Get(ctx context.Context) (string, error) {
	args := m.Called(ctx)
	return args.String(0), args.Error(1)
}

func newTokenGetterMock(tok string, err error) *TokenGetterMock {
	m := new(TokenGetterMock)
	m.On("Get", mock.Anything).Return(tok, err)
	return m
}

func TestNewRequestHelper(t *testing.T) {
	type args struct {
		tg         TokenGetter
		baseUrl    string
		apiVersion int
	}
	tests := []struct {
		name    string
		args    args
		want    *RequestHelper
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "successfully create RequestHelper",
			args: args{
				tg:         new(TokenGetterMock),
				baseUrl:    "baseUrl",
				apiVersion: 55,
			},
			want: &RequestHelper{
				tokenGetter: new(TokenGetterMock),
				client:      new(HttpClientMock),
				baseUrl:     "baseUrl",
				apiVersion:  55,
			},
			wantErr: assert.NoError,
		},
		{
			name: "cache nil  return error",
			args: args{
				baseUrl:    "baseUrl",
				apiVersion: 55,
			},
			wantErr: assert.Error,
		},
		{
			name: "baseUrl not set  return error",
			args: args{
				tg:         new(TokenGetterMock),
				apiVersion: 55,
			},
			wantErr: assert.Error,
		},
		{
			name: "version not set return error",
			args: args{
				tg:      new(TokenGetterMock),
				baseUrl: "base/url",
			},
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpClientMock := new(HttpClientMock)
			got, err := NewRequestHelper(httpClientMock, tt.args.tg, tt.args.baseUrl, tt.args.apiVersion)

			if !tt.wantErr(t, err, fmt.Sprintf("NewRequestHelper(<HttpClientMock>, %v, %v, %v)", tt.args.tg, tt.args.baseUrl, tt.args.apiVersion)) {
				return
			}
			assert.Equalf(t, tt.want, got, "NewRequestHelper(<HttpClientMock>, %v, %v, %v)", tt.args.tg, tt.args.baseUrl, tt.args.apiVersion)
		})
	}
}

func TestQuery(t *testing.T) {
	type testCase[E any] struct {
		name    string
		h       *RequestHelper
		args    string
		want    *QueryResponse[recordStub]
		wantErr assert.ErrorAssertionFunc
	}
	tests := []testCase[any]{
		{
			name: "successful query request  queryResponse returned",
			h: &RequestHelper{
				client: newHttpClientMock(&http.Response{Body: io.NopCloser(
					bytes.NewReader([]byte(`{"totalSize": 1, "done":true}`))),
					StatusCode: 200,
				}, nil),
				tokenGetter: newTokenGetterMock("token", nil),
				baseUrl:     "baseUrl",
				apiVersion:  55,
			},
			args: "query",
			want: &QueryResponse[recordStub]{
				TotalSize: 1,
				Done:      true,
			},
			wantErr: assert.NoError,
		},
		{
			name: "400 status code  code returned",
			h: &RequestHelper{
				client: newHttpClientMock(&http.Response{Body: io.NopCloser(nil),
					StatusCode: 400,
				}, nil),
				tokenGetter: newTokenGetterMock("token", nil),
				baseUrl:     "baseUrl",
				apiVersion:  55,
			},
			args: "query",
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				errType := &QueryError{}
				return assert.ErrorAs(t, err, errType, i...)
			},
		},
		{
			name: "500 status code  code returned",
			h: &RequestHelper{
				client: newHttpClientMock(&http.Response{Body: io.NopCloser(nil),
					StatusCode: 500,
				}, nil),
				tokenGetter: newTokenGetterMock("token", nil),
				baseUrl:     "baseUrl",
				apiVersion:  55,
			},
			args: "query",
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				errType := &QueryError{}
				return assert.ErrorAs(t, err, errType, i...)
			},
		},
		{
			name: "http.Do() returns error  error returned",
			h: &RequestHelper{
				client:      newHttpClientMock(&http.Response{Body: io.NopCloser(nil), StatusCode: 0}, fmt.Errorf("http client error")),
				tokenGetter: newTokenGetterMock("token", nil),
				baseUrl:     "baseUrl",
				apiVersion:  55,
			},
			args:    "query",
			wantErr: assert.Error,
		},
		{
			name: "successful query request with concrete type  queryResponse returned",
			h: &RequestHelper{
				client: newHttpClientMock(&http.Response{Body: io.NopCloser(
					bytes.NewReader([]byte(`{"totalSize": 1, "done":true, "records":[{"attributes":{"type":"type", "url":"url"}, "foo":"bar"}]}`))),
					StatusCode: 200,
				}, nil),
				tokenGetter: newTokenGetterMock("token", nil),
				baseUrl:     "baseUrl",
				apiVersion:  55,
			},
			args: "query",
			want: &QueryResponse[recordStub]{
				TotalSize: 1,
				Done:      true,
				Records: []recordStub{{
					Attributes: Attributes{
						Type: "type",
						Url:  "url",
					},
					Foo: "bar",
				}},
			},
			wantErr: assert.NoError,
		},
		{
			name: "query has space  replaced with +",
			h: &RequestHelper{
				client: newHttpClientMock(&http.Response{Body: io.NopCloser(
					bytes.NewReader([]byte(`{"totalSize": 1, "done":true}`))),
					StatusCode: 200,
				}, nil),
				tokenGetter: newTokenGetterMock("token", nil),
				baseUrl:     "baseUrl",
				apiVersion:  55,
			},
			args: "query query",
			want: &QueryResponse[recordStub]{
				TotalSize: 1,
				Done:      true,
			},
			wantErr: assert.NoError,
		},
		{
			name: "custom sf version set  queryResponse returned with custom url",
			h: &RequestHelper{
				client: newHttpClientMock(&http.Response{Body: io.NopCloser(
					bytes.NewReader([]byte(`{"totalSize": 1, "done":true}`))),
					StatusCode: 200,
				}, nil),
				tokenGetter: newTokenGetterMock("token", nil),
				baseUrl:     "baseUrl",
				apiVersion:  70,
			},
			args: "query",
			want: &QueryResponse[recordStub]{
				TotalSize: 1,
				Done:      true,
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Query[recordStub](context.Background(), tt.h, tt.args)

			if !tt.wantErr(t, err, fmt.Sprintf("Query(<context> %v, %v)", tt.h, tt.args)) {
				return
			}
			assert.Equalf(t, tt.want, got, "Query(<context>, %v, %v)", tt.h, tt.args)
		})
	}
}

func TestPost(t *testing.T) {
	newRecord := struct {
		One string `json:"one"`
		Two int    `json:"two"`
	}{
		One: "test",
		Two: 123,
	}
	type args struct {
		ctx    context.Context
		h      *RequestHelper
		name   string
		record any
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "successful response, returns created id",
			args: args{
				ctx: context.Background(),
				h: &RequestHelper{
					tokenGetter: newTokenGetterMock("token", nil),
					client: newHttpClientMock(&http.Response{
						StatusCode: 201,
						Body:       io.NopCloser(strings.NewReader(`{"id":"id-123","success":true}`)),
					}, nil),
					baseUrl:    "baseUrl",
					apiVersion: 55,
				},
				name:   "object-123",
				record: newRecord,
			},
			want:    "id-123",
			wantErr: assert.NoError,
		},
		{
			name: "response contains failed status, returns error",
			args: args{
				ctx: context.Background(),
				h: &RequestHelper{
					tokenGetter: newTokenGetterMock("token", nil),
					client: newHttpClientMock(&http.Response{
						StatusCode: 201,
						Body:       io.NopCloser(strings.NewReader(`{"id":"id-123","success":false}`)),
					}, nil),
					baseUrl:    "baseUrl",
					apiVersion: 55,
				},
				name:   "object-123",
				record: newRecord,
			},
			want:    "",
			wantErr: assert.Error,
		},
		{
			name: "response contains invalid json, returns error",
			args: args{
				ctx: context.Background(),
				h: &RequestHelper{
					tokenGetter: newTokenGetterMock("token", nil),
					client: newHttpClientMock(&http.Response{
						StatusCode: 201,
						Body:       io.NopCloser(strings.NewReader(`{invalid:json}`)),
					}, nil),
					baseUrl:    "baseUrl",
					apiVersion: 55,
				},
				name:   "object-123",
				record: newRecord,
			},
			want:    "",
			wantErr: assert.Error,
		},
		{
			name: "response status code is 400, returns error",
			args: args{
				ctx: context.Background(),
				h: &RequestHelper{
					tokenGetter: newTokenGetterMock("token", nil),
					client: newHttpClientMock(&http.Response{
						StatusCode: 400,
					}, nil),
					baseUrl:    "baseUrl",
					apiVersion: 55,
				},
				name:   "object-123",
				record: newRecord,
			},
			want:    "",
			wantErr: assert.Error,
		},
		{
			name: "client error, returns error",
			args: args{
				ctx: context.Background(),
				h: &RequestHelper{
					tokenGetter: newTokenGetterMock("token", nil),
					client:      newHttpClientMock(nil, errors.New("http error")),
					baseUrl:     "baseUrl",
					apiVersion:  55,
				},
				name:   "object-123",
				record: newRecord,
			},
			want:    "",
			wantErr: assert.Error,
		},
		{
			name: "token getter error, returns error",
			args: args{
				ctx: context.Background(),
				h: &RequestHelper{
					tokenGetter: newTokenGetterMock("", errors.New("token getter error")),
					baseUrl:     "baseUrl",
					apiVersion:  55,
				},
				name:   "object-123",
				record: newRecord,
			},
			want:    "",
			wantErr: assert.Error,
		},
		{
			name: "unable to create request, returns error",
			args: args{
				ctx: context.Background(),
				h: &RequestHelper{
					baseUrl:    ":",
					apiVersion: 55,
				},
				name:   "object-123",
				record: newRecord,
			},
			want:    "",
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Post(tt.args.ctx, tt.args.h, tt.args.name, tt.args.record)
			if !tt.wantErr(t, err, fmt.Sprintf("Post(%v, %v, %v, %v)", tt.args.ctx, tt.args.h, tt.args.name, tt.args.record)) {
				return
			}
			assert.Equalf(t, tt.want, got, "Post(%v, %v, %v, %v)", tt.args.ctx, tt.args.h, tt.args.name, tt.args.record)
		})
	}
}

func TestPatch(t *testing.T) {
	validRecord := struct {
		One int    `json:"one"`
		Two string `json:"two"`
	}{123, "test"}

	type args struct {
		h      *RequestHelper
		name   string
		id     string
		record any
	}
	tests := []struct {
		name    string
		args    args
		want    int
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "client returns successful response, 200 and no error returned",
			args: args{
				h: &RequestHelper{
					client: newHttpClientMock(&http.Response{
						Body: io.NopCloser(
							bytes.NewReader([]byte(`{"totalSize": 1, "done":true}`))),
						StatusCode: 200,
					}, nil),
					tokenGetter: newTokenGetterMock("token", nil),
					baseUrl:     "baseUrl",
					apiVersion:  55,
				},
				name:   "name",
				id:     "abc123",
				record: validRecord,
			},
			want:    200,
			wantErr: assert.NoError,
		},
		{
			name: "client returns 400 response, 400 and error returned",
			args: args{
				h: &RequestHelper{
					client: newHttpClientMock(&http.Response{
						Body: io.NopCloser(
							bytes.NewReader([]byte(`{"totalSize": 1, "done":true}`))),
						StatusCode: 400,
					}, nil),
					tokenGetter: newTokenGetterMock("token", nil),
					baseUrl:     "baseUrl",
					apiVersion:  55,
				},
				name:   "name",
				id:     "abc123",
				record: validRecord,
			},
			want:    400,
			wantErr: assert.Error,
		},
		{
			name: "client returns error, 0 and error returned",
			args: args{
				h: &RequestHelper{
					client:      newHttpClientMock(nil, errors.New("an error happened")),
					tokenGetter: newTokenGetterMock("token", nil),
					baseUrl:     "baseUrl",
					apiVersion:  55,
				},
				name:   "name",
				id:     "abc123",
				record: validRecord,
			},
			want:    0,
			wantErr: assert.Error,
		},
		{
			name: "token cache returns error, 0 and error returned",
			args: args{
				h: &RequestHelper{
					client:      nil,
					tokenGetter: newTokenGetterMock("", errors.New("a token error happened")),
					baseUrl:     "baseUrl",
					apiVersion:  55,
				},
				name:   "name",
				id:     "abc123",
				record: validRecord,
			},
			want:    0,
			wantErr: assert.Error,
		},
		{
			name: "error creating request, 0 and error returned",
			args: args{
				h: &RequestHelper{
					client:      nil,
					tokenGetter: nil,
					baseUrl:     ":",
					apiVersion:  55,
				},
				name:   "name",
				id:     "abc123",
				record: validRecord,
			},
			want:    0,
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Patch(context.Background(), tt.args.h, tt.args.name, tt.args.id, tt.args.record)

			if !tt.wantErr(t, err, fmt.Sprintf("Patch(<context>, %v, %v, %v, %v)", tt.args.h, tt.args.name, tt.args.id, tt.args.record)) {
				return
			}
			assert.Equalf(t, tt.want, got, "Patch(<context>, %v, %v, %v, %v)", tt.args.h, tt.args.name, tt.args.id, tt.args.record)
		})
	}
}

func TestDelete(t *testing.T) {
	type args struct {
		ctx  context.Context
		h    *RequestHelper
		name string
		id   string
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "successful response, returns no error",
			args: args{
				ctx: context.Background(),
				h: &RequestHelper{
					tokenGetter: newTokenGetterMock("token", nil),
					client: newHttpClientMock(&http.Response{
						StatusCode: 204,
					}, nil),
					baseUrl:    "baseUrl",
					apiVersion: 55,
				},
				name: "object-123",
				id:   "id-123",
			},
			wantErr: assert.NoError,
		},
		{
			name: "response status code is 400, returns error",
			args: args{
				ctx: context.Background(),
				h: &RequestHelper{
					tokenGetter: newTokenGetterMock("token", nil),
					client: newHttpClientMock(&http.Response{
						StatusCode: 400,
					}, nil),
					baseUrl:    "baseUrl",
					apiVersion: 55,
				},
				name: "object-123",
				id:   "id-123",
			},
			wantErr: assert.Error,
		},
		{
			name: "client error, returns error",
			args: args{
				ctx: context.Background(),
				h: &RequestHelper{
					tokenGetter: newTokenGetterMock("token", nil),
					client:      newHttpClientMock(nil, errors.New("http error")),
					baseUrl:     "baseUrl",
					apiVersion:  55,
				},
				name: "object-123",
				id:   "id-123",
			},
			wantErr: assert.Error,
		},
		{
			name: "token getter error, returns error",
			args: args{
				ctx: context.Background(),
				h: &RequestHelper{
					tokenGetter: newTokenGetterMock("", errors.New("token getter error")),
					baseUrl:     "baseUrl",
					apiVersion:  55,
				},
				name: "object-123",
				id:   "id-123",
			},
			wantErr: assert.Error,
		},
		{
			name: "unable to create request, returns error",
			args: args{
				ctx: context.Background(),
				h: &RequestHelper{
					baseUrl:    ":",
					apiVersion: 55,
				},
				name: "object-123",
				id:   "id-123",
			},
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, Delete(tt.args.ctx, tt.args.h, tt.args.name, tt.args.id), fmt.Sprintf("Delete(%v, %v, %v, %v)", tt.args.ctx, tt.args.h, tt.args.name, tt.args.id))
		})
	}
}
