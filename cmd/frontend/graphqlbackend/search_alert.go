package graphqlbackend

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/cockroachdb/errors"
	"github.com/hashicorp/go-multierror"
	"github.com/inconshreveable/log15"

	"github.com/sourcegraph/sourcegraph/cmd/frontend/backend"
	"github.com/sourcegraph/sourcegraph/cmd/frontend/envvar"
	"github.com/sourcegraph/sourcegraph/internal/authz"
	"github.com/sourcegraph/sourcegraph/internal/comby"
	"github.com/sourcegraph/sourcegraph/internal/database/dbutil"
	"github.com/sourcegraph/sourcegraph/internal/search"
	"github.com/sourcegraph/sourcegraph/internal/search/query"
	searchrepos "github.com/sourcegraph/sourcegraph/internal/search/repos"
	"github.com/sourcegraph/sourcegraph/internal/search/run"
	"github.com/sourcegraph/sourcegraph/internal/search/searchcontexts"
	"github.com/sourcegraph/sourcegraph/internal/search/streaming"
	"github.com/sourcegraph/sourcegraph/internal/vcs/git"
)

type searchAlert struct {
	prometheusType  string
	title           string
	description     string
	proposedQueries []*searchQueryDescription
	// The higher the priority the more important is the alert.
	priority int
}

func (a searchAlert) PrometheusType() string { return a.prometheusType }

func (a searchAlert) Title() string { return a.title }

func (a searchAlert) Description() *string {
	if a.description == "" {
		return nil
	}
	return &a.description
}

func (a searchAlert) ProposedQueries() *[]*searchQueryDescription {
	if len(a.proposedQueries) == 0 {
		return nil
	}
	return &a.proposedQueries
}

func alertForCappedAndExpression() *searchAlert {
	return &searchAlert{
		prometheusType: "exceed_and_expression_search_limit",
		title:          "Too many files to search for expression",
		description:    "One expression in the query requires a lot of work! This can happen with negated text searches like '-content:', not-expressions, or and-expressions. Try using the '-file:' or '-repo:' filters to narrow your search (like excluding autogenerated files). We're working on improving this experience in https://github.com/sourcegraph/sourcegraph/issues/9824",
	}
}

// alertForQuery converts errors in the query to search alerts.
func alertForQuery(queryString string, err error) *searchAlert {
	if errors.HasType(err, &query.UnsupportedError{}) || errors.HasType(err, &query.ExpectedOperand{}) {
		return &searchAlert{
			prometheusType: "unsupported_and_or_query",
			title:          "Unable To Process Query",
			description:    `I'm having trouble understanding that query. Your query contains "and" or "or" operators that make me think they apply to filters like "repo:" or "file:". We only support "and" or "or" operators on search patterns for file contents currently. You can help me by putting parentheses around the search pattern.`,
		}
	}
	return &searchAlert{
		prometheusType: "generic_invalid_query",
		title:          "Unable To Process Query",
		description:    capFirst(err.Error()),
	}
}

func alertForTimeout(usedTime time.Duration, suggestTime time.Duration, r *searchResolver) *searchAlert {
	q, err := query.ParseLiteral(r.rawQuery()) // Invariant: query is already validated; guard against error anyway.
	if err != nil {
		return &searchAlert{
			prometheusType: "timed_out",
			title:          "Timed out while searching",
			description:    fmt.Sprintf("We weren't able to find any results in %s. Try adding timeout: with a higher value.", usedTime.Round(time.Second)),
		}
	}
	return &searchAlert{
		prometheusType: "timed_out",
		title:          "Timed out while searching",
		description:    fmt.Sprintf("We weren't able to find any results in %s.", usedTime.Round(time.Second)),
		proposedQueries: []*searchQueryDescription{
			{
				description: "query with longer timeout",
				query:       fmt.Sprintf("timeout:%v %s", suggestTime, query.OmitField(q, query.FieldTimeout)),
				patternType: r.PatternType,
			},
		},
	}
}

// reposExist returns true if one or more repos resolve. If the attempt
// returns 0 repos or fails, it returns false. It is a helper function for
// raising NoResolvedRepos alerts with suggestions when we know the original
// query does not contain any repos to search.
func (r *searchResolver) reposExist(ctx context.Context, options searchrepos.Options) bool {
	options.UserSettings = r.UserSettings
	repositoryResolver := &searchrepos.Resolver{
		DB:                  r.db,
		Zoekt:               r.zoekt,
		SearchableReposFunc: backend.Repos.ListSearchable,
	}
	resolved, err := repositoryResolver.Resolve(ctx, options)
	return err == nil && len(resolved.RepoRevs) > 0
}

type errNoResolvedRepos struct {
	PrometheusType  string
	Title           string
	Description     string
	ProposedQueries []*searchQueryDescription
}

func (e *errNoResolvedRepos) Error() string {
	return "no resolved repositories"
}

func (r *searchResolver) errorForNoResolvedRepos(ctx context.Context, q query.Q) *errNoResolvedRepos {
	globbing := getBoolPtr(r.UserSettings.SearchGlobbing, false)

	repoFilters, minusRepoFilters := q.Repositories()
	repoGroupFilters, _ := q.StringValues(query.FieldRepoGroup)
	contextFilters, _ := q.StringValues(query.FieldContext)
	onlyForks, noForks, forksNotSet := false, false, true
	if fork := q.Fork(); fork != nil {
		onlyForks = *fork == query.Only
		noForks = *fork == query.No
		forksNotSet = false
	}
	archived := q.Archived()
	archivedNotSet := archived == nil

	// Handle repogroup-only scenarios.
	if len(repoFilters) == 0 && len(repoGroupFilters) == 0 {
		return &errNoResolvedRepos{
			PrometheusType: "no_resolved_repos__no_repositories",
			Title:          "Add repositories or connect repository hosts",
			Description:    "There are no repositories to search. Add an external service connection to your code host.",
		}
	}
	if len(repoFilters) == 0 && len(repoGroupFilters) == 1 {
		return &errNoResolvedRepos{
			PrometheusType: "no_resolved_repos__repogroup_empty",
			Title:          fmt.Sprintf("Add repositories to repogroup:%s to see results", repoGroupFilters[0]),
			Description:    fmt.Sprintf("The repository group %q is empty. See the documentation for configuration and troubleshooting.", repoGroupFilters[0]),
		}
	}
	if len(repoFilters) == 0 && len(repoGroupFilters) > 1 {
		return &errNoResolvedRepos{
			PrometheusType: "no_resolved_repos__repogroup_none_in_common",
			Title:          "Repository groups have no repositories in common",
			Description:    "No repository exists in all of the specified repository groups.",
		}
	}
	if len(contextFilters) == 1 && !searchcontexts.IsGlobalSearchContextSpec(contextFilters[0]) && (len(repoFilters) > 0 || len(repoGroupFilters) > 0) {
		withoutContextFilter := query.OmitField(q, query.FieldContext)
		proposedQueries := []*searchQueryDescription{{
			description: "search in the global context",
			query:       fmt.Sprintf("context:%s %s", searchcontexts.GlobalSearchContextName, withoutContextFilter),
			patternType: r.PatternType,
		}}

		return &errNoResolvedRepos{
			PrometheusType:  "no_resolved_repos__context_none_in_common",
			Title:           fmt.Sprintf("No repositories found for your query within the context %s", contextFilters[0]),
			ProposedQueries: proposedQueries,
		}
	}

	isSiteAdmin := backend.CheckCurrentUserIsSiteAdmin(ctx, r.db) == nil
	if !envvar.SourcegraphDotComMode() {
		if needsRepoConfig, err := needsRepositoryConfiguration(ctx, r.db); err == nil && needsRepoConfig {
			if isSiteAdmin {
				return &errNoResolvedRepos{
					Title:       "No repositories or code hosts configured",
					Description: "To start searching code, first go to site admin to configure repositories and code hosts.",
				}

			} else {
				return &errNoResolvedRepos{
					Title:       "No repositories or code hosts configured",
					Description: "To start searching code, ask the site admin to configure and enable repositories.",
				}
			}
		}
	}

	if globbing {
		return &errNoResolvedRepos{
			PrometheusType: "no_resolved_repos__generic",
			Title:          "No repositories found",
			Description:    "Try using a different `repo:<regexp>` filter to see results",
		}
	}

	proposedQueries := []*searchQueryDescription{}
	if forksNotSet {
		tryIncludeForks := searchrepos.Options{
			RepoFilters:      repoFilters,
			MinusRepoFilters: minusRepoFilters,
			NoForks:          false,
		}
		if r.reposExist(ctx, tryIncludeForks) {
			proposedQueries = append(proposedQueries, &searchQueryDescription{
				description: "include forked repositories in your query.",
				query:       r.OriginalQuery + " fork:yes",
				patternType: r.PatternType,
			})
		}
	}

	if archivedNotSet {
		tryIncludeArchived := searchrepos.Options{
			RepoFilters:      repoFilters,
			MinusRepoFilters: minusRepoFilters,
			OnlyForks:        onlyForks,
			NoForks:          noForks,
			OnlyArchived:     true,
		}
		if r.reposExist(ctx, tryIncludeArchived) {
			proposedQueries = append(proposedQueries, &searchQueryDescription{
				description: "include archived repositories in your query.",
				query:       r.OriginalQuery + " archived:yes",
				patternType: r.PatternType,
			})
		}
	}

	if len(proposedQueries) > 0 {
		return &errNoResolvedRepos{
			PrometheusType:  "no_resolved_repos__repos_exist_when_altered",
			Title:           "No repositories found",
			Description:     "Try alter the query or use a different `repo:<regexp>` filter to see results",
			ProposedQueries: proposedQueries,
		}
	}

	return &errNoResolvedRepos{
		PrometheusType: "no_resolved_repos__generic",
		Title:          "No repositories found",
		Description:    "Try using a different `repo:<regexp>` filter to see results",
	}
}

type errOverRepoLimit struct {
	ProposedQueries []*searchQueryDescription
	Description     string
}

func (e *errOverRepoLimit) Error() string {
	return "Too many matching repositories"
}

func (r *searchResolver) errorForOverRepoLimit(ctx context.Context) *errOverRepoLimit {
	// Try to suggest the most helpful repo: filters to narrow the query.
	//
	// For example, suppose the query contains "repo:kubern" and it matches > 30
	// repositories, and each one of the (clipped result set of) 30 repos has
	// "kubernetes" in their path. Then it's likely that the user would want to
	// search for "repo:kubernetes". If that still matches > 30 repositories,
	// then try to narrow it further using "/kubernetes/", etc.
	//
	// (In the above sample paragraph, we assume MAX_REPOS_TO_SEARCH is 30.)
	//
	// TODO(sqs): this logic can be significantly improved, but it's better than
	// nothing for now.

	var proposedQueries []*searchQueryDescription
	description := "Use a 'repo:' or 'repogroup:' filter to narrow your search and see results."
	if envvar.SourcegraphDotComMode() {
		description = "Use a 'repo:' or 'repogroup:' filter to narrow your search and see results or set up a self-hosted Sourcegraph instance to search an unlimited number of repositories."
	}
	if backend.CheckCurrentUserIsSiteAdmin(ctx, r.db) == nil {
		description += " As a site admin, you can increase the limit by changing maxReposToSearch in site config."
	}

	buildErr := func(proposedQueries []*searchQueryDescription, description string) *errOverRepoLimit {
		return &errOverRepoLimit{
			ProposedQueries: proposedQueries,
			Description:     description,
		}
	}

	// If globbing is active we return a simple alert for now. The alert is still
	// helpful but it doesn't contain any proposed queries.
	if getBoolPtr(r.UserSettings.SearchGlobbing, false) {
		return buildErr(proposedQueries, description)
	}

	q, err := query.ParseLiteral(r.rawQuery()) // Invariant: query is already validated; guard against error anyway.
	if err != nil || !query.IsBasic(q) {
		// If the query is not basic, the assumptions that other logic
		// makes to propose queries do not hold. Return a default alert
		// without proposed queries.
		return buildErr(proposedQueries, description)
	}

	resolved, _ := r.resolveRepositories(ctx, r.Query, resolveRepositoriesOpts{})
	if len(resolved.RepoRevs) > 0 {
		paths := make([]string, len(resolved.RepoRevs))
		for i, repo := range resolved.RepoRevs {
			paths[i] = string(repo.Repo.Name)
		}

		// See if we can narrow it down by using filters like
		// repo:github.com/myorg/.
		const maxParentsToPropose = 4
		ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer cancel()
	outer:
		for i, repoParent := range pathParentsByFrequency(paths) {
			if i >= maxParentsToPropose || ctx.Err() != nil {
				break
			}
			repoParentPattern := "^" + regexp.QuoteMeta(repoParent) + "/"
			repoFieldValues, _ := q.Repositories()

			for _, v := range repoFieldValues {
				if strings.HasPrefix(v, strings.TrimSuffix(repoParentPattern, "/")) {
					continue outer // this repo: filter is already applied
				}
			}

			repoFieldValues = append(repoFieldValues, repoParentPattern)
			ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			defer cancel()
			resolved, err := r.resolveRepositories(ctx, r.Query, resolveRepositoriesOpts{
				effectiveRepoFieldValues: repoFieldValues,
			})
			if ctx.Err() != nil {
				continue
			} else if err != nil {
				return buildErr([]*searchQueryDescription{}, description)
			}

			var more string
			if resolved.OverLimit {
				more = "(further filtering required)"
			}
			// We found a more specific repo: filter that may be narrow enough. Now
			// add it to the user's query, but be smart. For example, if the user's
			// query was "repo:foo" and the parent is "foobar/", then propose "repo:foobar/"
			// not "repo:foo repo:foobar/" (which are equivalent, but shorter is better).
			newExpr := query.AddRegexpField(q, query.FieldRepo, repoParentPattern)
			proposedQueries = append(proposedQueries, &searchQueryDescription{
				description: fmt.Sprintf("in repositories under %s %s", repoParent, more),
				query:       newExpr,
				patternType: r.PatternType,
			})
		}
		if len(proposedQueries) == 0 || ctx.Err() == context.DeadlineExceeded {
			// Propose specific repos' paths if we aren't able to propose
			// anything else.
			const maxReposToPropose = 4
			shortest := append([]string{}, paths...) // prefer shorter repo names
			sort.Slice(shortest, func(i, j int) bool {
				return len(shortest[i]) < len(shortest[j]) || (len(shortest[i]) == len(shortest[j]) && shortest[i] < shortest[j])
			})
			for i, pathToPropose := range shortest {
				if i >= maxReposToPropose {
					break
				}
				newExpr := query.AddRegexpField(q, query.FieldRepo, "^"+regexp.QuoteMeta(pathToPropose)+"$")
				proposedQueries = append(proposedQueries, &searchQueryDescription{
					description: fmt.Sprintf("in the repository %s", strings.TrimPrefix(pathToPropose, "github.com/")),
					query:       newExpr,
					patternType: r.PatternType,
				})
			}
		}
	}
	return buildErr(proposedQueries, description)
}

func alertForStructuralSearchNotSet(queryString string) *searchAlert {
	return &searchAlert{
		prometheusType: "structural_search_not_set",
		title:          "No results",
		description:    "It looks like you may have meant to run a structural search, but it is not toggled.",
		proposedQueries: []*searchQueryDescription{{
			description: "Activate structural search",
			query:       queryString,
			patternType: query.SearchTypeStructural,
		}},
	}
}

type missingRepoRevsError struct {
	Missing []*search.RepositoryRevisions
}

func (*missingRepoRevsError) Error() string {
	return "missing repo revs"
}

func alertForMissingRepoRevs(missingRepoRevs []*search.RepositoryRevisions) *searchAlert {
	var description string
	if len(missingRepoRevs) == 1 {
		if len(missingRepoRevs[0].RevSpecs()) == 1 {
			description = fmt.Sprintf("The repository %s matched by your repo: filter could not be searched because it does not contain the revision %q.", missingRepoRevs[0].Repo.Name, missingRepoRevs[0].RevSpecs()[0])
		} else {
			description = fmt.Sprintf("The repository %s matched by your repo: filter could not be searched because it has multiple specified revisions: @%s.", missingRepoRevs[0].Repo.Name, strings.Join(missingRepoRevs[0].RevSpecs(), ","))
		}
	} else {
		sampleSize := 10
		if sampleSize > len(missingRepoRevs) {
			sampleSize = len(missingRepoRevs)
		}
		repoRevs := make([]string, 0, sampleSize)
		for _, r := range missingRepoRevs[:sampleSize] {
			repoRevs = append(repoRevs, string(r.Repo.Name)+"@"+strings.Join(r.RevSpecs(), ","))
		}
		b := strings.Builder{}
		_, _ = fmt.Fprintf(&b, "%d repositories matched by your repo: filter could not be searched because the following revisions do not exist, or differ but were specified for the same repository:", len(missingRepoRevs))
		for _, rr := range repoRevs {
			_, _ = fmt.Fprintf(&b, "\n* %s", rr)
		}
		if sampleSize < len(missingRepoRevs) {
			b.WriteString("\n* ...")
		}
		description = b.String()
	}
	return &searchAlert{
		prometheusType: "missing_repo_revs",
		title:          "Some repositories could not be searched",
		description:    description,
	}
}

// pathParentsByFrequency returns the most common path parents of the given paths.
// For example, given paths [a/b a/c x/y], it would return [a x] because "a"
// is a parent to 2 paths and "x" is a parent to 1 path.
func pathParentsByFrequency(paths []string) []string {
	var parents []string
	parentFreq := map[string]int{}
	for _, p := range paths {
		parent := path.Dir(p)
		if _, seen := parentFreq[parent]; !seen {
			parents = append(parents, parent)
		}
		parentFreq[parent]++
	}

	sort.Slice(parents, func(i, j int) bool {
		pi, pj := parents[i], parents[j]
		fi, fj := parentFreq[pi], parentFreq[pj]
		return fi > fj || (fi == fj && pi < pj) // freq desc, alpha asc
	})
	return parents
}

func (a searchAlert) wrapResults() *SearchResults {
	return &SearchResults{Alert: &a}
}

func (a searchAlert) wrapSearchImplementer(db dbutil.DB) *alertSearchImplementer {
	return &alertSearchImplementer{
		db:    db,
		alert: a,
	}
}

// alertSearchImplementer is a light wrapper type around an alert that implements
// SearchImplementer. This helps avoid needing to have a db on the searchAlert type
type alertSearchImplementer struct {
	db    dbutil.DB
	alert searchAlert
}

func (a alertSearchImplementer) Results(context.Context) (*SearchResultsResolver, error) {
	return &SearchResultsResolver{db: a.db, SearchResults: a.alert.wrapResults()}, nil
}

func (alertSearchImplementer) Suggestions(context.Context, *searchSuggestionsArgs) ([]SearchSuggestionResolver, error) {
	return nil, nil
}
func (alertSearchImplementer) Stats(context.Context) (*searchResultsStats, error) { return nil, nil }
func (alertSearchImplementer) Inputs() run.SearchInputs {
	return run.SearchInputs{}
}

// capFirst capitalizes the first rune in the given string. It can be safely
// used with UTF-8 strings.
func capFirst(s string) string {
	i := 0
	return strings.Map(func(r rune) rune {
		i++
		if i == 1 {
			return unicode.ToTitle(r)
		}
		return r
	}, s)
}

func alertForError(err error) *searchAlert {
	var (
		alert *searchAlert
		rErr  *run.RepoLimitError
		tErr  *run.TimeLimitError
		mErr  *missingRepoRevsError
	)

	if errors.As(err, &mErr) {
		alert = alertForMissingRepoRevs(mErr.Missing)
		alert.priority = 6
	} else if strings.Contains(err.Error(), "Worker_oomed") || strings.Contains(err.Error(), "Worker_exited_abnormally") {
		alert = &searchAlert{
			prometheusType: "structural_search_needs_more_memory",
			title:          "Structural search needs more memory",
			description:    "Running your structural search may require more memory. If you are running the query on many repositories, try reducing the number of repositories with the `repo:` filter.",
			priority:       5,
		}
	} else if strings.Contains(err.Error(), "Out of memory") {
		alert = &searchAlert{
			prometheusType: "structural_search_needs_more_memory__give_searcher_more_memory",
			title:          "Structural search needs more memory",
			description:    `Running your structural search requires more memory. You could try reducing the number of repositories with the "repo:" filter. If you are an administrator, try double the memory allocated for the "searcher" service. If you're unsure, reach out to us at support@sourcegraph.com.`,
			priority:       4,
		}
	} else if errors.As(err, &rErr) {
		alert = &searchAlert{
			prometheusType: "exceeded_diff_commit_search_limit",
			title:          fmt.Sprintf("Too many matching repositories for %s search to handle", rErr.ResultType),
			description:    fmt.Sprintf(`%s search can currently only handle searching across %d repositories at a time. Try using the "repo:" filter to narrow down which repositories to search, or using 'after:"1 week ago"'.`, strings.Title(rErr.ResultType), rErr.Max),
			priority:       2,
		}
	} else if errors.As(err, &tErr) {
		alert = &searchAlert{
			prometheusType: "exceeded_diff_commit_with_time_search_limit",
			title:          fmt.Sprintf("Too many matching repositories for %s search to handle", tErr.ResultType),
			description:    fmt.Sprintf(`%s search can currently only handle searching across %d repositories at a time. Try using the "repo:" filter to narrow down which repositories to search.`, strings.Title(tErr.ResultType), tErr.Max),
			priority:       1,
		}
	}
	return alert
}

// errorToAlert is intended to be a catch-all function for converting all errors into alerts.
// The intent here is to create alerts as close to the API boundary as possible, so this should be called
// immediately before creating the SearchResultsResolver.
func errorToAlert(err error) (*searchAlert, error) {
	if err == nil {
		return nil, nil
	}

	{
		var e *multierror.Error
		if errors.As(err, &e) {
			return multierrorToAlert(e)
		}
	}

	if errors.HasType(err, authz.ErrStalePermissions{}) {
		return alertForStalePermissions(), nil
	}

	{
		var e git.BadCommitError
		if errors.As(err, &e) {
			return alertForInvalidRevision(e.Spec), nil
		}
	}

	{
		var e *errOverRepoLimit
		if errors.As(err, &e) {
			return &searchAlert{
				prometheusType:  "over_repo_limit",
				title:           "Too many matching repositories",
				proposedQueries: e.ProposedQueries,
				description:     e.Description,
			}, nil
		}
	}

	{
		var e *errNoResolvedRepos
		if errors.As(err, &e) {
			return &searchAlert{
				prometheusType:  e.PrometheusType,
				title:           e.Title,
				proposedQueries: e.ProposedQueries,
				description:     e.Description,
			}, nil
		}
	}

	return nil, err
}

func maxAlertByPriority(a, b *searchAlert) *searchAlert {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	if a.priority < b.priority {
		return b
	}

	return a
}

// multierrorToAlert converts a multierror.Error into the highest priority alert
// for the errors contained in it, and a new error with all the errors that could
// not be converted to alerts.
func multierrorToAlert(me *multierror.Error) (resAlert *searchAlert, resErr error) {
	for _, err := range me.Errors {
		alert, err := errorToAlert(err)
		resAlert = maxAlertByPriority(resAlert, alert)
		resErr = multierror.Append(resErr, err)
	}

	return resAlert, resErr
}

func alertForStalePermissions() *searchAlert {
	return &searchAlert{
		prometheusType: "no_resolved_repos__stale_permissions",
		title:          "Permissions syncing in progress",
		description:    "Permissions are being synced from your code host, please wait for a minute and try again.",
	}
}

func alertForInvalidRevision(revision string) *searchAlert {
	revision = strings.TrimSuffix(revision, "^0")
	return &searchAlert{
		title:       "Invalid revision syntax",
		description: fmt.Sprintf("We don't know how to interpret the revision (%s) you specified. Learn more about the revision syntax in our documentation: https://docs.sourcegraph.com/code_search/reference/queries#repository-revisions.", revision),
	}
}

type alertObserver struct {
	// Inputs are used to generate alert messages based on the query.
	Inputs *run.SearchInputs

	// Update state.
	hasResults bool

	// Error state. Can be called concurrently.
	mu    sync.Mutex
	alert *searchAlert
	err   error
}

func (o *alertObserver) Error(ctx context.Context, err error) {
	// Timeouts are reported through Stats so don't report an error for them.
	if err == nil || isContextError(ctx, err) {
		return
	}

	// We can compute the alert outside of the critical section.
	alert := alertForError(err)

	o.mu.Lock()
	defer o.mu.Unlock()

	// The error can be converted into an alert.
	if alert != nil {
		o.update(alert)
		return
	}

	// Track the unexpected error for reporting when calling Done.
	o.err = multierror.Append(o.err, err)
}

// update to alert if it is more important than our current alert.
func (o *alertObserver) update(alert *searchAlert) {
	if o.alert == nil || alert.priority > o.alert.priority {
		o.alert = alert
	}
}

//  Done returns the highest priority alert and a multierror.Error containing
//  all errors that could not be converted to alerts.
func (o *alertObserver) Done(stats *streaming.Stats) (*searchAlert, error) {
	if !o.hasResults && o.Inputs.PatternType != query.SearchTypeStructural && comby.MatchHoleRegexp.MatchString(o.Inputs.OriginalQuery) {
		o.update(alertForStructuralSearchNotSet(o.Inputs.OriginalQuery))
	}

	if o.hasResults && o.err != nil {
		log15.Error("Errors during search", "error", o.err)
		return o.alert, nil
	}

	return o.alert, o.err
}
