package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMergedCheckPageFailures(t *testing.T) {
	for _, tc := range []struct {
		name, data, want string
		err              error
	}{
		{"offline", "", "offline", errors.New("offline")},
		{"invalid JSON", "invalid", "decode merged checks", nil},
		{"missing commit", `{"data":{"node":null}}`, "unavailable", nil},
		{"missing rollup", `{"data":{"node":{}}}`, "unavailable", nil},
		{"missing contexts", `{"data":{"node":{"statusCheckRollup":{}}}}`, "unavailable", nil},
		{"partial error", `{"data":{"node":{"statusCheckRollup":{"contexts":{"nodes":[]}}}},"errors":[{"message":"denied"}]}`, "denied", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := ghExecFunc
			t.Cleanup(func() { ghExecFunc = saved })
			ghExecFunc = func(...string) (bytes.Buffer, bytes.Buffer, error) {
				return *bytes.NewBufferString(tc.data), bytes.Buffer{}, tc.err
			}
			_, err := fetchMergedCheckPage(&mergedQueryClient{host: "github.com"}, "commit", "next")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestMergedCheckPaginationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, id, cursor, continuation string
	}{
		{"missing commit ID", "", "next", ""},
		{"missing cursor", "commit", "", ""},
		{"empty continuation", "commit", "next", `{"nodes":[],"pageInfo":{"hasNextPage":false}}`},
		{"repeated cursor", "commit", "next", `{"nodes":[{"name":"check"}],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := ghExecFunc
			t.Cleanup(func() { ghExecFunc = saved })
			ghExecFunc = func(...string) (bytes.Buffer, bytes.Buffer, error) {
				return *bytes.NewBufferString(`{"data":{"node":{"statusCheckRollup":{"contexts":` + tc.continuation + `}}}}`), bytes.Buffer{}, nil
			}
			var node mergedPRNode
			data := `{"commits":{"nodes":[{"commit":{"id":"` + tc.id + `","statusCheckRollup":{"contexts":{"nodes":[{"name":"initial"}],"pageInfo":{"hasNextPage":true,"endCursor":"` + tc.cursor + `"}}}}}]}}`
			if err := json.Unmarshal([]byte(data), &node); err != nil {
				t.Fatal(err)
			}
			if err := completeMergedChecks(&mergedQueryClient{host: "github.com"}, &node); err == nil {
				t.Fatal("incomplete check pagination reported success")
			}
		})
	}
}

func TestMergedMissingCheckConnectionFailsClosed(t *testing.T) {
	var node mergedPRNode
	if err := json.Unmarshal([]byte(`{"commits":{"nodes":[{"commit":{"id":"commit","statusCheckRollup":{"contexts":null}}}]}}`), &node); err != nil {
		t.Fatal(err)
	}
	if err := completeMergedChecks(&mergedQueryClient{}, &node); err == nil {
		t.Fatal("missing check connection reported success")
	}
}
