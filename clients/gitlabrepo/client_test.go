// Copyright 2023 OpenSSF Scorecard Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gitlabrepo

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/xanzy/go-gitlab"

	"github.com/ossf/scorecard/v4/clients"
)

func TestValidCommitSHA(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"abc1234", true},
		{"a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4", true},
		{"ABCDEF1234567890ABCDEF1234567890ABCDEF12", true},
		{"abc&private_token=xxx", false},
		{"../../../etc/passwd", false},
		{"abc 123", false},
		{"abc123!", false},
		{"abcd12", false},
		{"", false},
	}
	for _, tc := range cases {
		got := validCommitSHA.MatchString(tc.in)
		if got != tc.want {
			t.Errorf("validCommitSHA.MatchString(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

type errStubTripper struct{}

var errStub = errors.New("stub-transport: no network in test")

func (errStubTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errStub
}

func TestInitRepo_CommitSHA_Validation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		commitSHA         string
		wantInvalidSHAErr bool
	}{
		{"URL injection via & rejected", "abc&private_token=xxx", true},
		{"path traversal-like rejected", "../../../etc/passwd", true},
		{"non-hex rejected", "branch-name", true},
		{"empty string rejected", "", true},
		{"HeadSHA sentinel accepted past the regex", clients.HeadSHA, false},
		{"valid short SHA accepted past the regex", "abc1234", false},
		{"valid full SHA-1 accepted past the regex", "a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			httpClient := &http.Client{Transport: errStubTripper{}}
			glclient, err := gitlab.NewClient("", gitlab.WithHTTPClient(httpClient))
			if err != nil {
				t.Fatalf("gitlab.NewClient: %v", err)
			}
			c := &Client{glClient: glclient}
			repo := &repoURL{owner: "owner", project: "proj", host: "gitlab.com", scheme: "https"}

			err = c.InitRepo(repo, tc.commitSHA, 1)
			if err == nil {
				t.Fatalf("expected an error from InitRepo, got nil")
			}
			gotInvalidSHA := strings.Contains(err.Error(), "invalid commit SHA")
			if gotInvalidSHA != tc.wantInvalidSHAErr {
				t.Errorf("InitRepo(commitSHA=%q) err = %v; wantInvalidSHAErr=%v", tc.commitSHA, err, tc.wantInvalidSHAErr)
			}
		})
	}
}

func TestCheckRepoInaccessible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want error
		repo *gitlab.Project
		name string
	}{
		{
			name: "if repo is enabled then it is accessible",
			repo: &gitlab.Project{
				RepositoryAccessLevel: gitlab.EnabledAccessControl,
			},
		},
		{
			name: "repo should not have public access in this case, but if it does it is accessible",
			repo: &gitlab.Project{
				RepositoryAccessLevel: gitlab.PublicAccessControl,
			},
		},
		{
			name: "if repo is disabled then is inaccessible",
			repo: &gitlab.Project{
				RepositoryAccessLevel: gitlab.DisabledAccessControl,
			},
			want: errRepoAccess,
		},
		{
			name: "if repo is private then it is accessible",
			repo: &gitlab.Project{
				RepositoryAccessLevel: gitlab.PrivateAccessControl,
			},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := checkRepoInaccessible(tt.repo)
			if !errors.Is(got, tt.want) {
				t.Errorf("checkRepoInaccessible() got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListCommits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		responsePath string
		commits      []clients.Commit
		wantErr      bool
	}{
		{
			name:         "Error in ListRawCommits",
			responsePath: "./testdata/invalid-commits",
			commits:      []clients.Commit{},
			wantErr:      true,
		},
		{
			name:         "No commits in repo",
			responsePath: "./testdata/empty-response",
			commits:      []clients.Commit{},
			wantErr:      false,
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			httpClient := &http.Client{
				Transport: suffixStubTripper{
					responsePaths: map[string]string{
						"commits": tt.responsePath, // corresponds to projects/<id>/repository/commits
					},
				},
			}
			glclient, err := gitlab.NewClient("", gitlab.WithHTTPClient(httpClient))
			if err != nil {
				t.Fatalf("gitlab.NewClient error: %v", err)
			}
			commitshandler := &commitsHandler{
				glClient: glclient,
			}

			repoURL := repoURL{
				owner:     "ossf-tests",
				commitSHA: clients.HeadSHA,
			}

			commitshandler.init(&repoURL, 30)

			gqlhandler := graphqlHandler{
				client: httpClient,
			}
			gqlhandler.init(context.Background(), &repoURL)

			client := &Client{glClient: glclient, commits: commitshandler, graphql: &gqlhandler}

			got, Err := client.ListCommits()

			if (Err != nil) != tt.wantErr {
				t.Fatalf("ListCommits, wanted Error: %v, got Error: %v", tt.wantErr, Err)
			}
			if !cmp.Equal(got, tt.commits) {
				t.Errorf("ListCommits() got %v, want %v", got, tt.commits)
			}
		})
	}
}
