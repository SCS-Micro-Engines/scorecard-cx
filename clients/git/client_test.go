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

package git

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	gitV5 "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ossf/scorecard/v4/clients"
	"github.com/ossf/scorecard/v4/clients/localdir"
)

func createTestRepo(t *testing.T) (path string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "testrepo")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(dir)
	})
	r, err := gitV5.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("Failed to initialize git repo: %v", err)
	}

	w, err := r.Worktree()
	if err != nil {
		t.Fatalf("Failed to get worktree: %v", err)
	}

	// Create a new file
	filePath := filepath.Join(dir, "file")
	err = os.WriteFile(filePath, []byte("Hello, World!"), 0o600)
	if err != nil {
		t.Fatalf("Failed to write a file: %v", err)
	}

	// Add it to the staging area
	_, err = w.Add("file")
	if err != nil {
		t.Fatalf("Failed to add a file to staging area: %v", err)
	}

	// Commit
	_, err = w.Commit("Initial commit", &gitV5.CommitOptions{
		Author: &object.Signature{
			Name:  "Test Author",
			Email: "author@example.com",
			When:  time.Now(),
		},
	})

	if err != nil {
		t.Fatalf("Failed to make initial commit: %v", err)
	}

	return dir
}

func TestInitRepo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		commitSHA   string
		expectedErr string
		commitDepth int
	}{
		{
			name:        "Success",
			commitSHA:   "HEAD",
			commitDepth: 1,
		},
		{
			name:        "NegativeCommitDepth",
			commitSHA:   "HEAD",
			commitDepth: -1,
		},
	}

	repoPath := createTestRepo(t)

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			uri := repoPath

			client := &Client{}
			repo, err := localdir.MakeLocalDirRepo(uri)
			if err != nil {
				t.Fatalf("MakeLocalDirRepo(%s) failed: %v", uri, err)
			}
			err = client.InitRepo(repo, test.commitSHA, test.commitDepth)
			if (test.expectedErr != "") != (err != nil) {
				t.Errorf("Unexpected error during InitRepo: %v", err)
			}
		})
	}
}

func TestListCommits(t *testing.T) {
	t.Parallel()
	repoPath := createTestRepo(t)

	client := &Client{}
	commitDepth := 1
	expectedLen := 1
	commitSHA := "HEAD"
	uri := repoPath
	repo, err := localdir.MakeLocalDirRepo(uri)
	if err != nil {
		t.Fatalf("MakeLocalDirRepo(%s) failed: %v", uri, err)
	}
	if err := client.InitRepo(repo, commitSHA, commitDepth); err != nil {
		t.Fatalf("InitRepo(%s) failed: %v", uri, err)
	}

	// Act
	commits, err := client.ListCommits()
	if err != nil {
		t.Fatalf("ListCommits() failed: %v", err)
	}

	// Assert
	if len(commits) != expectedLen {
		t.Errorf("ListCommits() returned %d commits, want %d", len(commits), expectedLen)
	}
}

// stubRepo bypasses localdir.MakeLocalDirRepo's own validation so we can
// directly exercise the file:// URI checks in Client.InitRepo.
type stubRepo struct{ uri string }

func (s *stubRepo) URI() string              { return s.uri }
func (s *stubRepo) String() string           { return s.uri }
func (s *stubRepo) Host() string             { return "" }
func (s *stubRepo) IsValid() error           { return nil }
func (s *stubRepo) Metadata() []string       { return nil }
func (s *stubRepo) AppendMetadata(...string) {}

var _ clients.Repo = (*stubRepo)(nil)

func TestInitRepo_FileURI_Validation(t *testing.T) {
	t.Parallel()

	goodDir, err := os.MkdirTemp("", "scorecard-good-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(goodDir) })

	tmpFile, err := os.CreateTemp("", "not-a-dir-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	tmpFile.Close()
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	cases := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		{
			name:    "relative path rejected",
			uri:     "file://../etc",
			wantErr: true,
		},
		{
			name:    "path with .. segment rejected (not clean)",
			uri:     "file://" + goodDir + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "etc",
			wantErr: true,
		},
		{
			name:    "current-dir prefix rejected (not clean)",
			uri:     "file://." + string(os.PathSeparator) + "foo",
			wantErr: true,
		},
		{
			name:    "empty path rejected",
			uri:     "file://",
			wantErr: true,
		},
		{
			name:    "non-existent absolute path rejected",
			uri:     "file://" + goodDir + string(os.PathSeparator) + "does-not-exist-xyz",
			wantErr: true,
		},
		{
			name:    "file (not directory) rejected",
			uri:     "file://" + tmpFile.Name(),
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := &Client{}
			gotErr := client.InitRepo(&stubRepo{uri: tc.uri}, "HEAD", 1)
			if (gotErr != nil) != tc.wantErr {
				t.Errorf("InitRepo(%s) err = %v, wantErr = %v", tc.uri, gotErr, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(gotErr, errInvalidFileURI) {
				t.Errorf("InitRepo(%s) err = %v, want errInvalidFileURI", tc.uri, gotErr)
			}
		})
	}
}

func TestGetFileContent_PathTraversal(t *testing.T) {
	t.Parallel()

	tempDir, err := os.MkdirTemp("", "scorecard-traversal-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tempDir) })

	c := &Client{tempDir: tempDir}

	for _, name := range []string{
		"../etc/passwd",
		"../../etc/passwd",
		"foo/../../bar",
		"sub/dir/../../../escape",
	} {
		_, err := c.GetFileContent(name)
		if !errors.Is(err, errPathTraversal) {
			t.Errorf("GetFileContent(%q) = err %v, want errPathTraversal", name, err)
		}
	}
}

func TestGetFileContent_ValidPaths(t *testing.T) {
	t.Parallel()

	tempDir, err := os.MkdirTemp("", "scorecard-good-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tempDir) })

	if err := os.WriteFile(filepath.Join(tempDir, "ok.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	c := &Client{tempDir: tempDir}

	// Normal read.
	got, err := c.GetFileContent("ok.txt")
	if err != nil {
		t.Fatalf("GetFileContent unexpected err: %v", err)
	}
	if !bytes.Equal(got, []byte("ok")) {
		t.Errorf("GetFileContent = %q, want %q", got, "ok")
	}

	// Path that cleans back inside tempDir must not be rejected.
	if _, err := c.GetFileContent("dir/../ok.txt"); err != nil {
		t.Errorf("expected dir/../ok.txt to be accepted; got err: %v", err)
	}
}

func TestSearch(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		request  clients.SearchRequest
		expected clients.SearchResponse
	}{
		{
			name: "Search with valid query",
			request: clients.SearchRequest{
				Query: "Hello",
			},
			expected: clients.SearchResponse{
				Results: []clients.SearchResult{
					{
						Path: "file",
					},
					{
						Path: "test.txt",
					},
				},
				Hits: 2,
			},
		},
		{
			name: "Search with zero results",
			request: clients.SearchRequest{
				Query: "Invalid",
			},
			expected: clients.SearchResponse{
				Hits: 0,
			},
		},
	}

	// Use the same test repo for all test cases.
	repoPath := createTestRepo(t)
	filePath := filepath.Join(repoPath, "test.txt")
	err := os.WriteFile(filePath, []byte("Hello, World!"), 0o600)
	if err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	// Make a commit that adds the file.
	r, err := gitV5.PlainOpen(repoPath)
	if err != nil {
		t.Fatalf("PlainOpen() failed: %v", err)
	}
	w, err := r.Worktree()
	if err != nil {
		t.Fatalf("Worktree() failed: %v", err)
	}
	_, err = w.Add("test.txt")
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}
	_, err = w.Commit("Add test.txt", &gitV5.CommitOptions{
		Author: &object.Signature{
			Name:  "Test Author",
			Email: "author@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("Commit() failed: %v", err)
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := &Client{}
			uri := repoPath
			repo, err := localdir.MakeLocalDirRepo(uri)
			if err != nil {
				t.Fatalf("MakeLocalDirRepo(%s) failed: %v", uri, err)
			}
			if err := client.InitRepo(repo, "HEAD", 1); err != nil {
				t.Fatalf("InitRepo(%s) failed: %v", uri, err)
			}

			response, err := client.Search(tc.request)
			if err != nil {
				t.Fatalf("Search() failed: %v", err)
			}

			if diff := cmp.Diff(tc.expected, response, cmpopts.IgnoreUnexported(clients.SearchResult{})); diff != "" {
				t.Errorf("Search() returned diff (-want +got):\n%s", diff)
			}
		})
	}
}
