package resolvers

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/hexops/autogold"

	"github.com/sourcegraph/log/logtest"

	"github.com/sourcegraph/sourcegraph/cmd/frontend/graphqlbackend"
	edb "github.com/sourcegraph/sourcegraph/enterprise/internal/database"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/insights/store"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/insights/types"
	"github.com/sourcegraph/sourcegraph/internal/actor"
	"github.com/sourcegraph/sourcegraph/internal/database"
	"github.com/sourcegraph/sourcegraph/internal/database/dbtest"
	internalTypes "github.com/sourcegraph/sourcegraph/internal/types"
)

// TestResolver_InsightSeries tests that the InsightSeries GraphQL resolver works.
func TestResolver_InsightSeries(t *testing.T) {
	testSetup := func(t *testing.T) (context.Context, [][]graphqlbackend.InsightSeriesResolver) {
		// Setup the GraphQL resolver.
		ctx := actor.WithInternalActor(context.Background())
		now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Truncate(time.Microsecond)
		logger := logtest.Scoped(t)
		clock := func() time.Time { return now }
		insightsDB := edb.NewInsightsDB(dbtest.NewInsightsDB(logger, t))
		postgres := database.NewDB(logger, dbtest.NewDB(logger, t))
		resolver := newWithClock(insightsDB, postgres, clock)

		insightMetadataStore := store.NewMockInsightMetadataStore()
		insightMetadataStore.GetMappedFunc.SetDefaultReturn([]types.Insight{
			{
				UniqueID:    "unique1",
				Title:       "title1",
				Description: "desc1",
				Series: []types.InsightViewSeries{
					{
						UniqueID:           "unique1",
						SeriesID:           "1234567",
						Title:              "title1",
						Description:        "desc1",
						Query:              "query1",
						CreatedAt:          now,
						OldestHistoricalAt: now,
						LastRecordedAt:     now,
						NextRecordingAfter: now,
						Label:              "label1",
						LineColor:          "color1",
					},
				},
			},
		}, nil)
		resolver.insightMetadataStore = insightMetadataStore

		// Create the insights connection resolver and query series.
		conn, err := resolver.Insights(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}

		nodes, err := conn.Nodes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var series [][]graphqlbackend.InsightSeriesResolver
		for _, node := range nodes {
			series = append(series, node.Series())
		}
		return ctx, series
	}

	t.Run("Points", func(t *testing.T) {
		ctx, insights := testSetup(t)
		autogold.Want("insights length", int(1)).Equal(t, len(insights))

		autogold.Want("insights[0].length", int(1)).Equal(t, len(insights[0]))

		// Issue a query against the actual DB.
		points, err := insights[0][0].Points(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		autogold.Want("insights[0][0].Points", []graphqlbackend.InsightsDataPointResolver{}).Equal(t, points)

	})
}

func TestFilterRepositories(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name           string
		repositories   []string
		filters        types.InsightViewFilters
		want           []string
		searchContexts []struct {
			name  string
			query string
		}
	}{
		{name: "test one exclude",
			repositories: []string{"github.com/sourcegraph/sourcegraph", "gitlab.com/myrepo/repo"},
			filters:      types.InsightViewFilters{ExcludeRepoRegex: addrStr("gitlab.com")},
			want:         []string{"github.com/sourcegraph/sourcegraph"},
		},
		{name: "test one include",
			repositories: []string{"github.com/sourcegraph/sourcegraph", "gitlab.com/myrepo/repo"},
			filters:      types.InsightViewFilters{IncludeRepoRegex: addrStr("gitlab.com")},
			want:         []string{"gitlab.com/myrepo/repo"},
		},
		{name: "test no filters",
			repositories: []string{"github.com/sourcegraph/sourcegraph", "gitlab.com/myrepo/repo"},
			filters:      types.InsightViewFilters{},
			want:         []string{"github.com/sourcegraph/sourcegraph", "gitlab.com/myrepo/repo"},
		},
		{name: "test exclude and include",
			repositories: []string{"github.com/sourcegraph/sourcegraph", "gitlab.com/myrepo/repo", "gitlab.com/yourrepo/yourrepo"},
			filters:      types.InsightViewFilters{ExcludeRepoRegex: addrStr("github.*"), IncludeRepoRegex: addrStr("myrepo")},
			want:         []string{"gitlab.com/myrepo/repo"},
		},
		{name: "test exclude all",
			repositories: []string{"github.com/sourcegraph/sourcegraph", "gitlab.com/myrepo/repo", "gitlab.com/yourrepo/yourrepo"},
			filters:      types.InsightViewFilters{ExcludeRepoRegex: addrStr(".*")},
			want:         []string{},
		},
		{name: "test include all",
			repositories: []string{"github.com/sourcegraph/sourcegraph", "gitlab.com/myrepo/repo", "gitlab.com/yourrepo/yourrepo"},
			filters:      types.InsightViewFilters{IncludeRepoRegex: addrStr(".*")},
			want:         []string{"github.com/sourcegraph/sourcegraph", "gitlab.com/myrepo/repo", "gitlab.com/yourrepo/yourrepo"},
		},
		{name: "test context include",
			repositories: []string{"github.com/sourcegraph/sourcegraph", "gitlab.com/myrepo/repo", "gitlab.com/yourrepo/yourrepo"},
			filters:      types.InsightViewFilters{SearchContexts: []string{"@dev/mycontext123"}},
			searchContexts: []struct {
				name  string
				query string
			}{
				{name: "@dev/mycontext123", query: "repo:^github\\.com/sourcegraph/.*"},
			},
			want: []string{"github.com/sourcegraph/sourcegraph"},
		},
		{name: "test context exclude",
			repositories: []string{"github.com/sourcegraph/sourcegraph", "gitlab.com/myrepo/repo", "gitlab.com/yourrepo/yourrepo"},
			filters:      types.InsightViewFilters{SearchContexts: []string{"@dev/mycontext123"}},
			searchContexts: []struct {
				name  string
				query string
			}{
				{name: "@dev/mycontext123", query: "-repo:^github\\.com/sourcegraph/.*"},
			},
			want: []string{"gitlab.com/myrepo/repo", "gitlab.com/yourrepo/yourrepo"},
		},
		{name: "test context exclude include",
			repositories: []string{"github.com/sourcegraph/sourcegraph", "gitlab.com/myrepo/repo", "gitlab.com/yourrepo/yourrepo"},
			filters:      types.InsightViewFilters{SearchContexts: []string{"@dev/mycontext123"}},
			searchContexts: []struct {
				name  string
				query string
			}{
				{name: "@dev/mycontext123", query: "-repo:^github.* repo:myrepo"},
			},
			want: []string{"gitlab.com/myrepo/repo"},
		},
		{name: "test context exclude regex include",
			repositories: []string{"github.com/sourcegraph/sourcegraph", "gitlab.com/myrepo/repo", "gitlab.com/yourrepo/yourrepo"},
			filters:      types.InsightViewFilters{SearchContexts: []string{"@dev/mycontext123"}, IncludeRepoRegex: addrStr("myrepo")},
			searchContexts: []struct {
				name  string
				query string
			}{
				{name: "@dev/mycontext123", query: "-repo:^github.*"},
			},
			want: []string{"gitlab.com/myrepo/repo"},
		},
		{name: "test context include regex exclude",
			repositories: []string{"github.com/sourcegraph/sourcegraph", "gitlab.com/myrepo/repo", "gitlab.com/yourrepo/yourrepo"},
			filters:      types.InsightViewFilters{SearchContexts: []string{"@dev/mycontext123"}, ExcludeRepoRegex: addrStr("^github.*")},
			searchContexts: []struct {
				name  string
				query string
			}{
				{name: "@dev/mycontext123", query: "repo:myrepo"},
			},
			want: []string{"gitlab.com/myrepo/repo"},
		},
		{name: "test context and regex include",
			repositories: []string{"github.com/sourcegraph/sourcegraph", "gitlab.com/myrepo/repo", "gitlab.com/yourrepo/yourrepo"},
			filters:      types.InsightViewFilters{SearchContexts: []string{"@dev/mycontext123"}, IncludeRepoRegex: addrStr("myrepo")},
			searchContexts: []struct {
				name  string
				query string
			}{
				{name: "@dev/mycontext123", query: "repo:gitlab"},
			},
			want: []string{"gitlab.com/myrepo/repo"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mocks := make(map[string]*internalTypes.SearchContext)
			for _, searchContextDef := range test.searchContexts {
				mocks[searchContextDef.name] = &internalTypes.SearchContext{Name: searchContextDef.name, Query: searchContextDef.query}
			}

			got, err := filterRepositories(ctx, test.filters, test.repositories, &fakeSearchContextLoader{mocks: mocks})
			if err != nil {
				t.Error(err)
			}
			// sort for test determinism
			sort.Slice(got, func(i, j int) bool {
				return got[i] < got[j]
			})
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("unexpected repository result (want/got): %v", diff)
			}
		})
	}
}
