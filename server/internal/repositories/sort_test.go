package repositories

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

func agentNames(agents []db.Agent) []string {
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return names
}

// TestAgentListSort covers the sortable Agents list of issue #320, through
// both List and ListFiltered.
func TestAgentListSort(t *testing.T) {
	gdb := newTestDB(t)
	repo := NewAgentRepository(gdb)
	ctx := context.Background()

	seen := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	at := func(h int) *time.Time { v := seen.Add(time.Duration(h) * time.Hour); return &v }
	// Created out of name order, so the default order is not just insertion.
	for _, a := range []*db.Agent{
		{Name: "charlie", Hostname: "Host-b", OS: "linux", Arch: "arm64", Version: "0.8.0", Status: "online", LastSeenAt: at(2)},
		{Name: "Alpha", Hostname: "host-c", OS: "linux", Arch: "amd64", Version: "0.7.0", Status: "offline", LastSeenAt: at(1)},
		{Name: "bravo", Hostname: "host-a", OS: "darwin", Arch: "arm64", Version: "0.8.1", Status: "offline"},
	} {
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("create agent: %v", err)
		}
	}

	cases := []struct {
		name string
		opts ListOptions
		want []string
	}{
		{"default is name, case-insensitive", ListOptions{}, []string{"Alpha", "bravo", "charlie"}},
		{"unknown key falls back to name", ListOptions{SortBy: "bogus; DROP TABLE agents"}, []string{"Alpha", "bravo", "charlie"}},
		{"name desc", ListOptions{SortBy: "name", SortDesc: true}, []string{"charlie", "bravo", "Alpha"}},
		{"hostname, case-insensitive", ListOptions{SortBy: "hostname"}, []string{"bravo", "charlie", "Alpha"}},
		{"os then arch", ListOptions{SortBy: "os"}, []string{"bravo", "Alpha", "charlie"}},
		{"version desc", ListOptions{SortBy: "version", SortDesc: true}, []string{"bravo", "charlie", "Alpha"}},
		{"status, ties by id", ListOptions{SortBy: "status"}, []string{"Alpha", "bravo", "charlie"}},
		{"last seen asc, never seen last", ListOptions{SortBy: "last_seen"}, []string{"Alpha", "charlie", "bravo"}},
		{"last seen desc, never seen last", ListOptions{SortBy: "last_seen", SortDesc: true}, []string{"charlie", "Alpha", "bravo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Limit = 10
			got, total, err := repo.List(ctx, tc.opts)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if names := agentNames(got); !slices.Equal(names, tc.want) || total != 3 {
				t.Errorf("List = %v (total %d), want %v (total 3)", names, total, tc.want)
			}
			got, _, err = repo.ListFiltered(ctx, AgentFilter{Search: "a"}, tc.opts)
			if err != nil {
				t.Fatalf("ListFiltered: %v", err)
			}
			if names := agentNames(got); !slices.Equal(names, tc.want) {
				t.Errorf("ListFiltered = %v, want %v", names, tc.want)
			}
		})
	}

	// Pages over equal values neither repeat nor skip an agent.
	var paged []string
	for offset := 0; offset < 3; offset++ {
		page, _, err := repo.List(ctx, ListOptions{Limit: 1, Offset: offset, SortBy: "status"})
		if err != nil {
			t.Fatalf("List page %d: %v", offset, err)
		}
		paged = append(paged, agentNames(page)...)
	}
	if want := []string{"Alpha", "bravo", "charlie"}; !slices.Equal(paged, want) {
		t.Errorf("paged by status = %v, want %v", paged, want)
	}
}

// TestPolicyListSort covers the sortable Policies list of issue #320. Sorting
// by agent joins the agents table, a soft-deleted agent included.
func TestPolicyListSort(t *testing.T) {
	gdb := newTestDB(t)
	agents := NewAgentRepository(gdb)
	repo := NewPolicyRepository(gdb)
	ctx := context.Background()

	agentID := map[string]*db.Agent{}
	for _, name := range []string{"zulu", "Mike", "alpha"} {
		a := &db.Agent{Name: name, Status: "online"}
		if err := agents.Create(ctx, a); err != nil {
			t.Fatalf("create agent: %v", err)
		}
		agentID[name] = a
	}
	for _, p := range []struct {
		name, agent string
		enabled     bool
	}{
		{"nightly", "alpha", true},
		{"Hourly", "zulu", false},
		{"archive", "Mike", true},
	} {
		policy := &db.Policy{Name: p.name, AgentID: agentID[p.agent].ID, Schedule: "@daily", Enabled: true, Sources: "[]", RepoPassword: db.EncryptedString("x")}
		if err := repo.Create(ctx, policy); err != nil {
			t.Fatalf("create policy: %v", err)
		}
		if !p.enabled {
			// enabled has a DB default of true: a false value is not inserted.
			if err := gdb.Model(policy).Update("enabled", false).Error; err != nil {
				t.Fatalf("disable policy: %v", err)
			}
		}
	}
	if err := agents.Delete(ctx, agentID["alpha"].ID); err != nil {
		t.Fatalf("delete agent: %v", err)
	}

	cases := []struct {
		name string
		opts ListOptions
		want []string
	}{
		{"default is name, case-insensitive", ListOptions{}, []string{"archive", "Hourly", "nightly"}},
		{"unknown key falls back to name", ListOptions{SortBy: "schedule"}, []string{"archive", "Hourly", "nightly"}},
		{"name desc", ListOptions{SortBy: "name", SortDesc: true}, []string{"nightly", "Hourly", "archive"}},
		{"agent, deleted agent included", ListOptions{SortBy: "agent"}, []string{"nightly", "archive", "Hourly"}},
		{"agent desc", ListOptions{SortBy: "agent", SortDesc: true}, []string{"Hourly", "archive", "nightly"}},
		{"status desc puts enabled first", ListOptions{SortBy: "status", SortDesc: true}, []string{"nightly", "archive", "Hourly"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Limit = 10
			got, total, err := repo.List(ctx, tc.opts)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			names := make([]string, len(got))
			for i, p := range got {
				names[i] = p.Name
			}
			if !slices.Equal(names, tc.want) || total != 3 {
				t.Errorf("List = %v (total %d), want %v (total 3)", names, total, tc.want)
			}
		})
	}
}
