import React from 'react'

import { mdiSourceRepository } from '@mdi/js'
import { formatRepositoryStarCount, SearchResultStar } from '@sourcegraph/search-ui'
import { RepositoryMatch } from '@sourcegraph/shared/src/search/stream'

import { RepoName } from './RepoName'
import { SearchResultLayout } from './SearchResultLayout'
import { SelectableSearchResult } from './SelectableSearchResult'

export interface RepoSearchResultProps {
    match: RepositoryMatch
    selectedResult: null | string
    selectResult: (id: string) => void
}

export const RepoSearchResult: React.FunctionComponent<RepoSearchResultProps> = ({
    match,
    selectedResult,
    selectResult,
}) => {
    const formattedRepositoryStarCount = formatRepositoryStarCount(match.repoStars)
    return (
        <SelectableSearchResult match={match} selectResult={selectResult} selectedResult={selectedResult}>
            {isActive => (
                <SearchResultLayout
                    iconColumn={{ icon: mdiSourceRepository, repoName: match.repository }}
                    infoColumn={
                        formattedRepositoryStarCount && (
                            <>
                                <SearchResultStar aria-label={`${match.repoStars} stars`} />
                                <span aria-hidden={true}>{formattedRepositoryStarCount}</span>
                            </>
                        )
                    }
                    isActive={isActive}
                >
                    <RepoName repoName={match.repository} suffix={match.description} />
                </SearchResultLayout>
            )}
        </SelectableSearchResult>
    )
}
