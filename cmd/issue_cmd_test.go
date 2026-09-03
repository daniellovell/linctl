package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dorkitude/linctl/pkg/api"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func resetIssueCommandFlags(t *testing.T, command *cobra.Command, names ...string) {
	t.Helper()
	for _, name := range names {
		flag := command.Flags().Lookup(name)
		if flag == nil {
			t.Fatalf("missing flag %q", name)
		}
		if err := flag.Value.Set(flag.DefValue); err != nil {
			t.Fatalf("reset flag %q: %v", name, err)
		}
		flag.Changed = false
	}
}

func resetIssueCreateStateFlags(t *testing.T) {
	t.Helper()
	resetIssueCommandFlags(t, issueCreateCmd, "title", "team", "state", "estimate")
}

func TestIssueCreateCmdStateResolvesToStateID(t *testing.T) {
	origTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = origTransport }()
	t.Setenv("LINCTL_API_KEY", "test-key")
	viper.Set("plaintext", true)
	viper.Set("json", false)
	resetIssueCreateStateFlags(t)

	var sawStateID string
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var gqlReq gqlCommandTestRequest
		if err := json.NewDecoder(req.Body).Decode(&gqlReq); err != nil {
			t.Fatalf("decode GraphQL request: %v", err)
		}

		switch {
		case strings.Contains(gqlReq.Query, "query Team("):
			body := `{"data":{"team":{"id":"team-1","key":"ENG","name":"Engineering","description":"","private":false,"issueCount":0}}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		case strings.Contains(gqlReq.Query, "query TeamStates("):
			body := `{"data":{"team":{"states":{"nodes":[{"id":"state-2","name":"In Progress","type":"started","color":"#00ff00","description":"","position":2}]}}}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		case strings.Contains(gqlReq.Query, "mutation CreateIssue("):
			input, ok := gqlReq.Variables["input"].(map[string]interface{})
			if !ok {
				t.Fatalf("expected input map, got %#v", gqlReq.Variables["input"])
			}
			if v, ok := input["stateId"].(string); ok {
				sawStateID = v
			}
			body := `{"data":{"issueCreate":{"issue":{"id":"i1","identifier":"ENG-1","title":"Fix bug","description":"","priority":3,"estimate":0,"createdAt":"2026-03-02T00:00:00Z","updatedAt":"2026-03-02T00:00:00Z","dueDate":"","state":{"id":"state-2","name":"In Progress","type":"started","color":"#00ff00"},"assignee":null,"team":{"id":"team-1","key":"ENG","name":"Engineering"},"labels":{"nodes":[]},"project":null,"projectMilestone":null,"parent":null}}}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		default:
			t.Fatalf("unexpected GraphQL operation: %s", gqlReq.Query)
			return nil, nil
		}
	})

	_ = issueCreateCmd.Flags().Set("title", "Fix bug")
	_ = issueCreateCmd.Flags().Set("team", "ENG")
	_ = issueCreateCmd.Flags().Set("state", "In Progress")
	issueCreateCmd.Run(issueCreateCmd, nil)

	if sawStateID != "state-2" {
		t.Fatalf("expected stateId state-2, got %q", sawStateID)
	}
}

func TestIssueCreateCmdEstimateSentInInput(t *testing.T) {
	origTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = origTransport }()
	t.Setenv("LINCTL_API_KEY", "test-key")
	viper.Set("plaintext", true)
	viper.Set("json", false)
	resetIssueCommandFlags(t, issueCreateCmd, "title", "team", "state", "estimate")

	var sawEstimate float64
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var gqlReq gqlCommandTestRequest
		if err := json.NewDecoder(req.Body).Decode(&gqlReq); err != nil {
			t.Fatalf("decode GraphQL request: %v", err)
		}

		switch {
		case strings.Contains(gqlReq.Query, "query Team("):
			body := `{"data":{"team":{"id":"team-1","key":"ENG","name":"Engineering","description":"","private":false,"issueCount":0}}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		case strings.Contains(gqlReq.Query, "mutation CreateIssue("):
			input, ok := gqlReq.Variables["input"].(map[string]interface{})
			if !ok {
				t.Fatalf("expected input map, got %#v", gqlReq.Variables["input"])
			}
			v, ok := input["estimate"].(float64)
			if !ok {
				t.Fatalf("expected estimate in input, got %#v", input["estimate"])
			}
			sawEstimate = v
			body := `{"data":{"issueCreate":{"issue":{"id":"i1","identifier":"ENG-1","title":"Fix bug","description":"","priority":3,"estimate":3,"createdAt":"2026-03-02T00:00:00Z","updatedAt":"2026-03-02T00:00:00Z","dueDate":"","state":{"id":"state-1","name":"Todo","type":"unstarted","color":"#999999"},"assignee":null,"team":{"id":"team-1","key":"ENG","name":"Engineering"},"labels":{"nodes":[]},"project":null,"projectMilestone":null,"parent":null}}}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		default:
			t.Fatalf("unexpected GraphQL operation: %s", gqlReq.Query)
			return nil, nil
		}
	})

	_ = issueCreateCmd.Flags().Set("title", "Fix bug")
	_ = issueCreateCmd.Flags().Set("team", "ENG")
	_ = issueCreateCmd.Flags().Set("estimate", "3")
	issueCreateCmd.Run(issueCreateCmd, nil)

	if sawEstimate != 3 {
		t.Fatalf("expected estimate 3, got %v", sawEstimate)
	}
}

func TestIssueUpdateCmdEstimateSentInInput(t *testing.T) {
	origTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = origTransport }()
	t.Setenv("LINCTL_API_KEY", "test-key")
	viper.Set("plaintext", true)
	viper.Set("json", false)
	resetIssueCommandFlags(t, issueUpdateCmd, "title", "description", "assignee", "state", "priority", "due-date", "delegate", "parent", "project", "project-milestone", "estimate")

	var sawEstimate float64
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var gqlReq gqlCommandTestRequest
		if err := json.NewDecoder(req.Body).Decode(&gqlReq); err != nil {
			t.Fatalf("decode GraphQL request: %v", err)
		}

		switch {
		case strings.Contains(gqlReq.Query, "mutation UpdateIssue("):
			input, ok := gqlReq.Variables["input"].(map[string]interface{})
			if !ok {
				t.Fatalf("expected input map, got %#v", gqlReq.Variables["input"])
			}
			v, ok := input["estimate"].(float64)
			if !ok {
				t.Fatalf("expected estimate in input, got %#v", input["estimate"])
			}
			sawEstimate = v
			body := `{"data":{"issueUpdate":{"issue":{"id":"i1","identifier":"ENG-1","title":"Fix bug","description":"","priority":3,"estimate":5,"createdAt":"2026-03-02T00:00:00Z","updatedAt":"2026-03-02T00:00:00Z","dueDate":"","state":{"id":"state-1","name":"Todo","type":"unstarted","color":"#999999"},"assignee":null,"team":{"id":"team-1","key":"ENG","name":"Engineering"},"labels":{"nodes":[]},"project":null,"projectMilestone":null,"parent":null}}}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		default:
			t.Fatalf("unexpected GraphQL operation: %s", gqlReq.Query)
			return nil, nil
		}
	})

	_ = issueUpdateCmd.Flags().Set("estimate", "5")
	issueUpdateCmd.Run(issueUpdateCmd, []string{"ENG-1"})

	if sawEstimate != 5 {
		t.Fatalf("expected estimate 5, got %v", sawEstimate)
	}
}

func TestExtractUploadsLinearURLs(t *testing.T) {
	text := `
Main [doc](https://uploads.linear.app/abc-123/spec.md) and plain https://uploads.linear.app/def-456/log.txt.
Ignore https://example.com/file.txt
`

	got := extractUploadsLinearURLs(text)
	want := []string{
		"https://uploads.linear.app/abc-123/spec.md",
		"https://uploads.linear.app/def-456/log.txt",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestCollectIssueAttachmentEntries(t *testing.T) {
	issue := &api.Issue{
		Description: "See https://uploads.linear.app/path/from-description.md",
		Attachments: &api.Attachments{
			Nodes: []api.Attachment{
				{ID: "att-1", Title: "Canonical", URL: "https://uploads.linear.app/path/from-attachment.md"},
			},
		},
		Comments: &api.Comments{
			Nodes: []api.Comment{
				{Body: "Another link https://uploads.linear.app/path/from-comment.md"},
				{Body: "Duplicate https://uploads.linear.app/path/from-attachment.md should not repeat"},
			},
		},
	}

	entries := collectIssueAttachmentEntries(issue)
	if len(entries) != 3 {
		t.Fatalf("expected 3 unique entries, got %d: %#v", len(entries), entries)
	}

	if entries[0].Source != "attachment" || entries[0].ID != "att-1" {
		t.Fatalf("expected first entry to be canonical attachment, got %#v", entries[0])
	}
}

func TestSelectAttachmentEntriesForDownload(t *testing.T) {
	entries := []issueAttachmentEntry{
		{ID: "a-1", Title: "spec.md", URL: "https://uploads.linear.app/x/spec.md", Source: "attachment"},
		{ID: "a-2", Title: "notes.md", URL: "https://uploads.linear.app/y/notes.md", Source: "attachment"},
	}

	gotAll, skippedAll, err := selectAttachmentEntriesForDownload(entries, true, "", "")
	if err != nil || len(gotAll) != 2 || len(skippedAll) != 0 {
		t.Fatalf("expected all entries selected without skips, got selected=%d skipped=%d err=%v", len(gotAll), len(skippedAll), err)
	}

	gotID, skippedID, err := selectAttachmentEntriesForDownload(entries, false, "a-2", "")
	if err != nil || len(gotID) != 1 || gotID[0].ID != "a-2" {
		t.Fatalf("expected id selection a-2, got %#v err=%v", gotID, err)
	}
	if len(skippedID) != 0 {
		t.Fatalf("expected no skipped entries for id selection, got %#v", skippedID)
	}

	gotName, skippedName, err := selectAttachmentEntriesForDownload(entries, false, "", "spec.md")
	if err != nil || len(gotName) != 1 || gotName[0].ID != "a-1" {
		t.Fatalf("expected name selection spec.md, got %#v err=%v", gotName, err)
	}
	if len(skippedName) != 0 {
		t.Fatalf("expected no skipped entries for name selection, got %#v", skippedName)
	}
}

func TestSelectAttachmentEntriesForDownloadAllSkipsNonDownloadableLinks(t *testing.T) {
	entries := []issueAttachmentEntry{
		{ID: "a-1", Title: "pr", URL: "https://github.com/org/repo/pull/123", Source: "attachment"},
		{ID: "a-2", Title: "file.md", URL: "https://uploads.linear.app/abc/file.md", Source: "markdown"},
	}

	selected, skipped, err := selectAttachmentEntriesForDownload(entries, true, "", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(selected) != 1 || selected[0].ID != "a-2" {
		t.Fatalf("expected only uploads entry selected, got %#v", selected)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected one skipped entry, got %#v", skipped)
	}
	if skipped[0].Status != "skipped" || skipped[0].Reason != "non-downloadable-link" {
		t.Fatalf("unexpected skipped metadata: %#v", skipped[0])
	}
}

func TestHasAttachmentDownloadFailuresIgnoresSkipped(t *testing.T) {
	results := []issueAttachmentDownloadResult{
		{Status: "skipped", Success: false},
		{Status: "downloaded", Success: true},
	}
	if hasAttachmentDownloadFailures(results) {
		t.Fatalf("skipped entries should not count as failures")
	}

	results = append(results, issueAttachmentDownloadResult{Status: "failed", Success: false})
	if !hasAttachmentDownloadFailures(results) {
		t.Fatalf("failed entries must count as failures")
	}
}

func TestValidateAttachmentURL(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{name: "Linear HTTPS upload", rawURL: "https://uploads.linear.app/x"},
		{name: "Linear HTTP upload", rawURL: "http://uploads.linear.app/x", wantErr: true},
		{name: "external host", rawURL: "https://evil.example/x", wantErr: true},
		{name: "deceptive subdomain", rawURL: "https://uploads.linear.app.evil.example/x", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateAttachmentDownloadURL(tt.rawURL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateAttachmentDownloadURL(%q) error = %v, wantErr %t", tt.rawURL, err, tt.wantErr)
			}
		})
	}
}

func TestAttachmentClientRejectsExternalRedirect(t *testing.T) {
	redirectURL, err := url.Parse("https://evil.example/attachment")
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}
	err = newAttachmentHTTPClient().CheckRedirect(&http.Request{URL: redirectURL}, nil)
	if err == nil {
		t.Fatal("expected external redirect to be rejected")
	}
}

func TestDownloadAttachmentEntryWithClientRejectsOversizedBody(t *testing.T) {
	const testLimit int64 = 1024
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="oversized.bin"`)
		_, _ = io.CopyN(w, strings.NewReader(strings.Repeat("x", int(testLimit+1))), testLimit+1)
	}))
	defer server.Close()

	attachmentURL, err := url.Parse(server.URL + "/oversized.bin")
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	outputDir := t.TempDir()
	_, err = downloadAttachmentEntryWithClient(context.Background(), server.Client(), attachmentURL, "", issueAttachmentEntry{
		Title:  "oversized.bin",
		URL:    attachmentURL.String(),
		Source: "attachment",
	}, outputDir, "", testLimit)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("expected size-limit error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outputDir, "oversized.bin")); !os.IsNotExist(statErr) {
		t.Fatalf("expected partial file to be removed, stat error = %v", statErr)
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain filename", input: "report.pdf", want: "report.pdf"},
		{name: "parent path", input: "../secret.txt", want: "secret.txt"},
		{name: "absolute path", input: "/tmp/report.pdf", want: "report.pdf"},
		{name: "empty name", input: "", want: ""},
		{name: "current directory", input: ".", want: ""},
		{name: "parent directory", input: "..", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeFilename(tt.input); got != tt.want {
				t.Fatalf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIssueAttachmentFlagsRegistered(t *testing.T) {
	if issueGetCmd.Flags().Lookup("download-attachments") == nil {
		t.Fatalf("issue get is missing --download-attachments")
	}
	if issueGetCmd.Flags().Lookup("output-dir") == nil {
		t.Fatalf("issue get is missing --output-dir")
	}

	for _, name := range []string{"all", "id", "name", "output", "output-dir"} {
		if issueAttachmentDownloadCmd.Flags().Lookup(name) == nil {
			t.Fatalf("issue attachment download is missing --%s", name)
		}
	}
}

func TestEstimateFlagRegistered(t *testing.T) {
	if issueCreateCmd.Flags().Lookup("estimate") == nil {
		t.Fatal("issue create is missing --estimate flag")
	}
	if issueUpdateCmd.Flags().Lookup("estimate") == nil {
		t.Fatal("issue update is missing --estimate flag")
	}
}
