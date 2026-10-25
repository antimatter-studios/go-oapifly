package oapifly

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

// These routes model REST Mail's domain create/get exchanges, without its DB or
// authentication setup. This checks the producer/consumer boundary with the real
// Vertrag executable; it does not replace REST Mail's running-server contract gate.
const domainExamplePairs = `{
  "createDomain": {
    "created": {
      "request": {"body": {"name": "example.test"}},
      "response": {"status": 201, "body": {"id": 7, "name": "example.test"}}
    },
    "missingName": {
      "request": {"body": {}, "invalid": true},
      "response": {"status": 422, "body": {"code": "validation_failed"}}
    },
    "unknownId": {
      "operation": "getDomain",
      "request": {"path": {"id": 999999}},
      "response": {"status": 404, "body": {"code": "not_found"}}
    }
  }
}`

func TestNamedExamplesWithVertrag(t *testing.T) {
	binary := os.Getenv("OAPIFLY_VERTRAG_BINARY")
	if binary == "" {
		t.Skip("set OAPIFLY_VERTRAG_BINARY to run the real Vertrag boundary check; CI requires it")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	g := New(Config{Examples: fstest.MapFS{"openapi-example.json": {Data: []byte(domainExamplePairs)}}, ExamplesFile: "openapi-example.json"})
	g.Register(
		Endpoint{Method: "POST", Path: "/api/v1/domains", OperationID: "createDomain", Request: TypeOf[exThingIn](),
			Responses: []EndpointResponse{{Status: 201, Type: TypeOf[exThing]()}, {Status: 422, Type: TypeOf[exError]()}}},
		Endpoint{Method: "GET", Path: "/api/v1/domains/{id}", OperationID: "getDomain", Request: TypeOf[exThingPath](),
			Responses: []EndpointResponse{{Status: 404, Type: TypeOf[exError]()}}},
	)
	if warnings := g.Report(); len(warnings) != 0 {
		t.Fatalf("the fixture must have no missing, invalid or orphaned pairs: %+v", warnings)
	}
	document, err := json.Marshal(g.Generate())
	if err != nil {
		t.Fatal(err)
	}

	for _, fault := range []string{"none", "wrong status", "wrong body"} {
		t.Run(fault, func(t *testing.T) {
			dir := t.TempDir()
			spec := filepath.Join(dir, "openapi.json")
			if err := os.WriteFile(spec, document, 0o600); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var body map[string]interface{}
				if r.Method == "POST" {
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("invalid request JSON: %v", err)
						w.WriteHeader(400)
						return
					}
				}
				encoded, _ := json.Marshal(body)
				mu.Lock()
				requests = append(requests, r.Method+" "+r.URL.RequestURI()+" "+string(encoded))
				mu.Unlock()
				status, response := 500, `{"code":"unexpected_request"}`
				switch {
				case r.Method == "GET" && r.URL.RequestURI() == "/api/v1/domains/999999":
					status, response = 404, `{"code":"not_found"}`
				case r.Method == "POST" && r.URL.RequestURI() == "/api/v1/domains" && len(body) == 0:
					status, response = 422, `{"code":"validation_failed"}`
					if fault == "wrong status" {
						status = 200
					}
				case r.Method == "POST" && r.URL.RequestURI() == "/api/v1/domains" && body["name"] == "example.test":
					status, response = 201, `{"id":7,"name":"example.test"}`
					if fault == "wrong body" {
						response = `{"id":"seven","name":"example.test"}`
					}
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(response))
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			reportFile := filepath.Join(dir, "report.json")
			cmd := exec.CommandContext(ctx, binary, "run", "--endpoint", server.URL, "--phases", "examples", "--no-color", "--reporter", "json", "--output", reportFile, spec)
			cmd.Dir = dir // No repository-level Vertrag config or hooks may change the fixture.
			output, runErr := cmd.CombinedOutput()
			if fault == "none" && runErr != nil {
				t.Fatalf("Vertrag failed: %v\n%s", runErr, output)
			}
			if fault != "none" {
				exit, ok := runErr.(*exec.ExitError)
				if !ok || exit.ExitCode() != 1 {
					t.Fatalf("the faulty response must fail a transaction, got %v\n%s", runErr, output)
				}
			}
			mu.Lock()
			gotRequests := append([]string(nil), requests...)
			mu.Unlock()
			sort.Strings(gotRequests)
			wantRequests := []string{`GET /api/v1/domains/999999 null`, `POST /api/v1/domains {"name":"example.test"}`, `POST /api/v1/domains {}`}
			if !reflect.DeepEqual(gotRequests, wantRequests) {
				t.Fatalf("Vertrag sent %q, want exactly %q\n%s", gotRequests, wantRequests, output)
			}
			data, err := os.ReadFile(reportFile)
			if err != nil {
				t.Fatalf("Vertrag did not write a report: %v\n%s", err, output)
			}
			var report struct {
				Summary struct{ Total, Passing, Failing, Errors, Skipped int }
				Results []struct{ Name, Expected, Actual, Status string }
			}
			if err := json.Unmarshal(data, &report); err != nil {
				t.Fatal(err)
			}
			wantPassing := 3
			if fault != "none" {
				wantPassing = 2
			}
			if report.Summary.Total != 3 || report.Summary.Passing != wantPassing || report.Summary.Failing != 3-wantPassing || report.Summary.Errors != 0 || report.Summary.Skipped != 0 || len(report.Results) != 3 {
				t.Fatalf("wrong transaction counts: %s", data)
			}
			for _, status := range []string{"201", "422", "404"} {
				count := 0
				for _, result := range report.Results {
					if result.Expected == status {
						count++
						wantStatus, wantActual := "pass", status
						if fault == "wrong status" && status == "422" {
							wantStatus, wantActual = "fail", "200"
						}
						if fault == "wrong body" && status == "201" {
							wantStatus = "fail"
						}
						if result.Status != wantStatus || result.Actual != wantActual {
							t.Errorf("%s result = %+v; want %s, actual %s", status, result, wantStatus, wantActual)
						}
					}
				}
				if count != 1 {
					t.Errorf("got %d results for %s, want one: %s", count, status, data)
				}
			}
		})
	}
}
