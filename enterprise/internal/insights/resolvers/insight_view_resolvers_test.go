package resolvers

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/sourcegraph/log/logtest"

	"github.com/sourcegraph/sourcegraph/cmd/frontend/graphqlbackend"
	edb "github.com/sourcegraph/sourcegraph/enterprise/internal/database"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/insights/store"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/insights/types"
	"github.com/sourcegraph/sourcegraph/internal/actor"
	"github.com/sourcegraph/sourcegraph/internal/database"
	"github.com/sourcegraph/sourcegraph/internal/database/basestore"
	"github.com/sourcegraph/sourcegraph/internal/database/dbtest"
	"github.com/sourcegraph/sourcegraph/internal/timeutil"
	internalTypes "github.com/sourcegraph/sourcegraph/internal/types"
)

func addrStr(input string) *string {
	return &input
}

func TestFrozenInsightDataSeriesResolver(t *testing.T) {
	ctx := context.Background()

	logger := logtest.Scoped(t)

	t.Run("insight_is_frozen_returns_nil_resolvers", func(t *testing.T) {
		ivr := insightViewResolver{view: &types.Insight{IsFrozen: true}}
		resolvers, err := ivr.DataSeries(ctx)
		if err != nil || resolvers != nil {
			t.Errorf("unexpected results from frozen data series resolver")
		}
	})
	t.Run("insight_is_not_frozen_returns_real_resolvers", func(t *testing.T) {
		insightsDB := edb.NewInsightsDB(dbtest.NewInsightsDB(logger, t))
		postgres := database.NewDB(logger, dbtest.NewDB(logger, t))
		permStore := store.NewInsightPermissionStore(postgres)
		clock := timeutil.Now
		timeseriesStore := store.NewWithClock(insightsDB, permStore, clock)
		base := baseInsightResolver{
			insightStore:    store.NewInsightStore(insightsDB),
			dashboardStore:  store.NewDashboardStore(insightsDB),
			insightsDB:      insightsDB,
			workerBaseStore: basestore.NewWithHandle(postgres.Handle()),
			postgresDB:      postgres,
			timeSeriesStore: timeseriesStore,
		}

		series, err := base.insightStore.CreateSeries(ctx, types.InsightSeries{
			SeriesID:            "series1234",
			Query:               "supercoolseries",
			SampleIntervalUnit:  string(types.Month),
			SampleIntervalValue: 1,
			GenerationMethod:    types.Search,
		})
		if err != nil {
			t.Fatal(err)
		}
		view, err := base.insightStore.CreateView(ctx, types.InsightView{
			Title:            "not frozen view",
			UniqueID:         "super not frozen",
			PresentationType: types.Line,
			IsFrozen:         false,
		}, []store.InsightViewGrant{store.GlobalGrant()})
		if err != nil {
			t.Fatal(err)
		}
		err = base.insightStore.AttachSeriesToView(ctx, series, view, types.InsightViewSeriesMetadata{
			Label:  "label1",
			Stroke: "blue",
		})
		if err != nil {
			t.Fatal(err)
		}
		viewWithSeries, err := base.insightStore.GetMapped(ctx, store.InsightQueryArgs{UniqueID: view.UniqueID})
		if err != nil || len(viewWithSeries) == 0 {
			t.Fatal(err)
		}
		ivr := insightViewResolver{view: &viewWithSeries[0], baseInsightResolver: base}
		resolvers, err := ivr.DataSeries(ctx)
		if err != nil || resolvers == nil {
			t.Errorf("unexpected results from unfrozen data series resolver")
		}
	})
}

func TestInsightViewDashboardConnections(t *testing.T) {

	// Test setup
	// Create 1 insight
	// Create 3 dashboards with insight
	//    1 - global and has insight
	//    2 - private to user and has insight
	//    3 - private to another user and has insight

	a := actor.FromUser(1)
	ctx := actor.WithActor(context.Background(), a)

	logger := logtest.Scoped(t)

	insightsDB := edb.NewInsightsDB(dbtest.NewInsightsDB(logger, t))
	postgresDB := database.NewDB(logger, dbtest.NewDB(logger, t))
	base := baseInsightResolver{
		insightStore:   store.NewInsightStore(insightsDB),
		dashboardStore: store.NewDashboardStore(insightsDB),
		insightsDB:     insightsDB,
		postgresDB:     postgresDB,
	}
	series, err := base.insightStore.CreateSeries(ctx, types.InsightSeries{
		SeriesID:            "series1234",
		Query:               "supercoolseries",
		SampleIntervalUnit:  string(types.Month),
		SampleIntervalValue: 1,
		GenerationMethod:    types.Search,
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := base.insightStore.CreateView(ctx, types.InsightView{
		Title:            "current view",
		UniqueID:         "current1234",
		PresentationType: types.Line,
		IsFrozen:         false,
	}, []store.InsightViewGrant{store.GlobalGrant()})
	if err != nil {
		t.Fatal(err)
	}

	err = base.insightStore.AttachSeriesToView(ctx, series, view, types.InsightViewSeriesMetadata{
		Label:  "label1",
		Stroke: "blue",
	})
	if err != nil {
		t.Fatal(err)
	}

	global := true
	globalGrants := []store.DashboardGrant{{nil, nil, &global}}
	dashboard1 := types.Dashboard{ID: 1, Title: "dashboard with view", InsightIDs: []string{view.UniqueID}}
	_, err = base.dashboardStore.CreateDashboard(ctx,
		store.CreateDashboardArgs{
			Dashboard: dashboard1,
			Grants:    globalGrants,
		})

	if err != nil {
		t.Fatal(err)
	}

	userId := 1
	privateCurrentUserGrants := []store.DashboardGrant{{&userId, nil, nil}}
	dashboard2 := types.Dashboard{ID: 2, Title: "users private dashboard with view", InsightIDs: []string{view.UniqueID}}
	_, err = base.dashboardStore.CreateDashboard(ctx,
		store.CreateDashboardArgs{
			Dashboard: dashboard2,
			Grants:    privateCurrentUserGrants,
		})
	if err != nil {
		t.Fatal(err)
	}
	notUsersId := 2
	privateDifferentUserGrants := []store.DashboardGrant{{&notUsersId, nil, nil}}
	dashboard3 := types.Dashboard{ID: 3, Title: "different users private dashboard with view", InsightIDs: []string{view.UniqueID}}
	_, err = base.dashboardStore.CreateDashboard(ctx,
		store.CreateDashboardArgs{
			Dashboard: dashboard3,
			Grants:    privateDifferentUserGrants,
		})
	if err != nil {
		t.Fatal(err)
	}

	insight, err := base.insightStore.GetMapped(ctx, store.InsightQueryArgs{UniqueID: view.UniqueID})
	if err != nil || len(insight) == 0 {
		t.Fatal(err)
	}

	t.Run("resolves global dasboard and users private dashboard", func(t *testing.T) {
		ivr := insightViewResolver{view: &insight[0], baseInsightResolver: base}
		connectionResolver := ivr.Dashboards(ctx, &graphqlbackend.InsightsDashboardsArgs{})
		dashboardResolvers, err := connectionResolver.Nodes(ctx)
		if err != nil || len(dashboardResolvers) != 2 {
			t.Errorf("unexpected results from dashboardResolvers resolver")
		}

		wantedDashboards := []types.Dashboard{dashboard1, dashboard2}
		for i, dash := range wantedDashboards {
			if diff := cmp.Diff(dash.Title, dashboardResolvers[i].Title()); diff != "" {
				t.Errorf("unexpected dashboard title (want/got): %v", diff)
			}
		}
	})

	t.Run("resolves dashboards with limit 1", func(t *testing.T) {
		ivr := insightViewResolver{view: &insight[0], baseInsightResolver: base}
		var first int32 = 1
		connectionResolver := ivr.Dashboards(ctx, &graphqlbackend.InsightsDashboardsArgs{First: &first})
		dashboardResolvers, err := connectionResolver.Nodes(ctx)
		if err != nil || len(dashboardResolvers) != 1 {
			t.Errorf("unexpected results from dashboardResolvers resolver")
		}

		wantedDashboards := []types.Dashboard{dashboard1}
		for i, dash := range wantedDashboards {
			if diff := cmp.Diff(newRealDashboardID(int64(dash.ID)).marshal(), dashboardResolvers[i].ID()); diff != "" {
				t.Errorf("unexpected dashboard title (want/got): %v", diff)
			}
		}
	})

	t.Run("resolves dashboards with after", func(t *testing.T) {
		ivr := insightViewResolver{view: &insight[0], baseInsightResolver: base}
		dash1ID := string(newRealDashboardID(int64(dashboard1.ID)).marshal())
		connectionResolver := ivr.Dashboards(ctx, &graphqlbackend.InsightsDashboardsArgs{After: &dash1ID})
		dashboardResolvers, err := connectionResolver.Nodes(ctx)
		if err != nil || len(dashboardResolvers) != 1 {
			t.Errorf("unexpected results from dashboardResolvers resolver")
		}

		wantedDashboards := []types.Dashboard{dashboard2}
		for i, dash := range wantedDashboards {
			if diff := cmp.Diff(newRealDashboardID(int64(dash.ID)).marshal(), dashboardResolvers[i].ID()); diff != "" {
				t.Errorf("unexpected dashboard title (want/got): %v", diff)
			}
		}
	})

	t.Run("no resolvers when no dashboards", func(t *testing.T) {
		nodashInsight := types.Insight{UniqueID: "nodash1234"}
		ivr := insightViewResolver{view: &nodashInsight, baseInsightResolver: base}
		connectionResolver := ivr.Dashboards(ctx, &graphqlbackend.InsightsDashboardsArgs{})
		dashboardResolvers, err := connectionResolver.Nodes(ctx)
		if err != nil || len(dashboardResolvers) != 0 {
			t.Errorf("unexpected results from dashboardResolvers resolver")
		}
	})

	t.Run("no resolvers when dashID passed for dash without user permission", func(t *testing.T) {
		ivr := insightViewResolver{view: &insight[0], baseInsightResolver: base}
		dashWithoutPermissionID := newRealDashboardID(int64(dashboard3.ID)).marshal()
		connectionResolver := ivr.Dashboards(ctx, &graphqlbackend.InsightsDashboardsArgs{ID: &dashWithoutPermissionID})
		dashboardResolvers, err := connectionResolver.Nodes(ctx)
		if err != nil || len(dashboardResolvers) != 0 {
			t.Errorf("unexpected results from dashboardResolvers resolver")
		}
	})
}

type fakeSearchContextLoader struct {
	mocks map[string]*internalTypes.SearchContext
}

func (f *fakeSearchContextLoader) GetByName(ctx context.Context, name string) (*internalTypes.SearchContext, error) {
	return f.mocks[name], nil
}
