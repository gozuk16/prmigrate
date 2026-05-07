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

func TestCreatePullRequest_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/pulls" {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"number":5,"html_url":"https://github.com/org/repo/pull/5"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	pr, err := c.CreatePullRequest(context.Background(), &githubapi.CreatePullRequestRequest{
		Title: "Fix bug",
		Body:  "description",
		Head:  "feature-x",
		Base:  "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 5 {
		t.Errorf("expected PR number 5, got %d", pr.Number)
	}
	if pr.HTMLURL != "https://github.com/org/repo/pull/5" {
		t.Errorf("unexpected HTMLURL: %s", pr.HTMLURL)
	}
}

func TestCreatePullRequest_validationError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"message":"Validation Failed","errors":[{"message":"No commits between main and feature-x"}]}`)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	_, err := c.CreatePullRequest(context.Background(), &githubapi.CreatePullRequestRequest{
		Title: "Fix bug",
		Body:  "description",
		Head:  "feature-x",
		Base:  "main",
	})
	if err == nil {
		t.Error("expected error for 422 response")
	}
}
