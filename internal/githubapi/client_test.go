package githubapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gozuk16/prmigrate/internal/githubapi"
)

func TestBranchExists_found(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/org/repo/branches/feature-x" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"name":"feature-x"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	exists, err := c.BranchExists(context.Background(), "feature-x")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("expected branch to exist")
	}
}

func TestBranchExists_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	exists, err := c.BranchExists(context.Background(), "feature-x")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("expected branch to not exist")
	}
}

func TestBranchExists_serverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	_, err := c.BranchExists(context.Background(), "feature-x")
	if err == nil {
		t.Error("expected error for 5xx response")
	}
}
