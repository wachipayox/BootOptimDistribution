package serviceupdate

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testCommit = "ef9f331eea4d52cb94ab3df0c4c5b5713621b44f"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestCheckReturnsPublishedMainVersion(t *testing.T) {
	handler := &Handler{
		currentVersion: "0.2.9",
		currentCommit:  testCommit,
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body := `{"object":{"type":"commit","sha":"` + testCommit + `"}}`
			if strings.HasSuffix(r.URL.Path, "/VERSION") {
				body = "0.2.9\n"
			} else if r.URL.String() != refURL {
				t.Fatalf("unexpected GitHub request URL: %s", r.URL)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		})},
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/service-update", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"update_available":false`) || !strings.Contains(recorder.Body.String(), `"available_version":"0.2.9"`) {
		t.Fatalf("unexpected update response: %s", recorder.Body.String())
	}
}

func TestCheckFailureKeepsBrowserMessageGenericAndLogsCause(t *testing.T) {
	previousOutput := log.Writer()
	var logs strings.Builder
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousOutput) })

	handler := &Handler{
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("proxyconnect tcp: connection refused")
		})},
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/service-update", nil))

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
	if strings.Contains(recorder.Body.String(), "proxyconnect") {
		t.Fatalf("response leaked internal diagnostic: %s", recorder.Body.String())
	}
	if !strings.Contains(logs.String(), "request GitHub main reference: Get \"https://api.github.com/") || !strings.Contains(logs.String(), "connection refused") {
		t.Fatalf("journal log omitted check cause: %s", logs.String())
	}
}
