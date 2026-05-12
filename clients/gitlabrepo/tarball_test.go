// Copyright 2021 Security Scorecard Authors
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
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

type listfileTest struct {
	predicate func(string) (bool, error)
	err       error
	outcome   []string
}

type getcontentTest struct {
	err      error
	filename string
	output   []byte
}

func isSortedString(x, y string) bool {
	return x < y
}

func setup(inputFile string) (tarballHandler, error) {
	tempDir, err := os.MkdirTemp("", repoDir)
	if err != nil {
		return tarballHandler{}, fmt.Errorf("test failed to create TempDir: %w", err)
	}
	tempFile, err := os.CreateTemp(tempDir, repoFilename)
	if err != nil {
		return tarballHandler{}, fmt.Errorf("test failed to create TempFile: %w", err)
	}
	testFile, err := os.OpenFile(inputFile, os.O_RDONLY, 0o644)
	if err != nil {
		return tarballHandler{}, fmt.Errorf("unable to open testfile: %w", err)
	}
	if _, err := io.Copy(tempFile, testFile); err != nil {
		return tarballHandler{}, fmt.Errorf("unable to do io.Copy: %w", err)
	}
	tarballHandler := tarballHandler{
		tempDir:     tempDir,
		tempTarFile: tempFile.Name(),
		once:        new(sync.Once),
	}
	tarballHandler.once.Do(func() {
		// We don't want to run the code in tarballHandler.setup(), so if we execute tarballHandler.once.Do() right
		// here, it won't get executed later when setup() is called.
	})
	return tarballHandler, nil
}

// newPreSetupHandler creates a tarballHandler with tempDir set and once
// already fired, so getFileContent can be exercised without a real download.
func newPreSetupHandler(t *testing.T) (tarballHandler, string) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "scorecard-traversal-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tempDir) })
	h := tarballHandler{tempDir: tempDir, once: new(sync.Once)}
	h.once.Do(func() {})
	return h, tempDir
}

func TestGetFileContent_PathTraversal(t *testing.T) {
	t.Parallel()
	handler, _ := newPreSetupHandler(t)
	for _, name := range []string{
		"../etc/passwd",
		"../../etc/passwd",
		"../../../etc/passwd",
		"foo/../../bar",
		"sub/dir/../../../escape",
	} {
		_, err := handler.getFileContent(name)
		if !errors.Is(err, errPathTraversal) {
			t.Errorf("getFileContent(%q) = err %v, want errPathTraversal", name, err)
		}
	}
}

func TestGetFileContent_ValidPaths(t *testing.T) {
	t.Parallel()
	handler, tempDir := newPreSetupHandler(t)

	if err := os.WriteFile(filepath.Join(tempDir, "hello.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sub := filepath.Join(tempDir, "dir1", "dir2")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "deep.txt"), []byte("deep"), 0o600); err != nil {
		t.Fatalf("WriteFile (nested): %v", err)
	}

	cases := []struct {
		filename string
		want     []byte
	}{
		{"hello.txt", []byte("hello")},
		{filepath.Join("dir1", "dir2", "deep.txt"), []byte("deep")},
		// path that cleans back inside tempDir must not be rejected
		{"dir1/../hello.txt", []byte("hello")},
	}
	for _, tc := range cases {
		got, err := handler.getFileContent(tc.filename)
		if err != nil {
			t.Errorf("getFileContent(%q) unexpected err: %v", tc.filename, err)
			continue
		}
		if !bytes.Equal(got, tc.want) {
			t.Errorf("getFileContent(%q) = %q, want %q", tc.filename, got, tc.want)
		}
	}
}

//nolint:gocognit
func TestExtractTarball(t *testing.T) {
	t.Parallel()
	testcases := []struct {
		name            string
		inputFile       string
		listfileTests   []listfileTest
		getcontentTests []getcontentTest
	}{
		{
			name:      "Basic",
			inputFile: "testdata/basic.tar.gz",
			listfileTests: []listfileTest{
				{
					// Returns all files in the tarball.
					predicate: func(string) (bool, error) { return true, nil },
					outcome:   []string{"file0", "dir1/file1", "dir1/dir2/file2"},
				},
				{
					// Skips all files inside `dir1/dir2` directory.
					predicate: func(fn string) (bool, error) { return !strings.HasPrefix(fn, "dir1/dir2"), nil },
					outcome:   []string{"file0", "dir1/file1"},
				},
				{
					// Skips all files.
					predicate: func(fn string) (bool, error) { return false, nil },
					outcome:   []string{},
				},
			},
			getcontentTests: []getcontentTest{
				{
					filename: "file0",
					output:   []byte("content0\n"),
				},
				{
					filename: "dir1/file1",
					output:   []byte("content1\n"),
				},
				{
					filename: "dir1/dir2/file2",
					output:   []byte("content2\n"),
				},
				{
					filename: "does/not/exist",
					err:      os.ErrNotExist,
				},
			},
		},
	}

	for _, testcase := range testcases {
		testcase := testcase
		t.Run(testcase.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			handler, err := setup(testcase.inputFile)
			if err != nil {
				t.Fatalf("test setup failed: %v", err)
			}

			// Extract tarball.
			if err := handler.extractTarball(); err != nil {
				t.Fatalf("test failed: %v", err)
			}

			// Test ListFiles API.
			for _, listfiletest := range testcase.listfileTests {
				matchedFiles, err := handler.listFiles(listfiletest.predicate)
				if !errors.Is(err, listfiletest.err) {
					t.Errorf("test failed: expected - %v, got - %v", listfiletest.err, err)
					continue
				}
				if !cmp.Equal(listfiletest.outcome,
					matchedFiles,
					cmpopts.SortSlices(isSortedString)) {
					t.Errorf("test failed: expected - %q, got - %q", listfiletest.outcome, matchedFiles)
				}
			}

			// Test GetFileContent API.
			for _, getcontenttest := range testcase.getcontentTests {
				content, err := handler.getFileContent(getcontenttest.filename)
				if getcontenttest.err != nil && !errors.Is(err, getcontenttest.err) {
					t.Errorf("test failed: expected - %v, got - %v", getcontenttest.err, err)
				}
				if getcontenttest.err == nil && !cmp.Equal(getcontenttest.output, content) {
					t.Errorf("test failed: expected - %s, got - %s", string(getcontenttest.output), string(content))
				}
			}

			// Test that files get deleted.
			if err := handler.cleanup(); err != nil {
				t.Errorf("test failed: %v", err)
			}
			if _, err := os.Stat(handler.tempDir); !os.IsNotExist(err) {
				t.Errorf("%v", err)
			}
			if len(handler.files) != 0 {
				t.Error("client.files not cleaned up!")
			}
		})
	}
}
