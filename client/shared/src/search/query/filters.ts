import { Omit } from 'utility-types'

import { SearchSuggestion } from '../suggestions'

import { predicateCompletion } from './predicates'
import { selectorCompletion } from './selectFilter'
import { Filter, Literal } from './token'

export enum FilterType {
    after = 'after',
    archived = 'archived',
    author = 'author',
    before = 'before',
    case = 'case',
    committer = 'committer',
    content = 'content',
    context = 'context',
    count = 'count',
    file = 'file',
    fork = 'fork',
    lang = 'lang',
    message = 'message',
    patterntype = 'patterntype',
    repo = 'repo',
    repogroup = 'repogroup',
    repohascommitafter = 'repohascommitafter',
    repohasfile = 'repohasfile',
    // eslint-disable-next-line unicorn/prevent-abbreviations
    rev = 'rev',
    select = 'select',
    timeout = 'timeout',
    type = 'type',
    visibility = 'visibility',
}

/* eslint-disable unicorn/prevent-abbreviations */
export enum AliasedFilterType {
    f = 'file',
    g = 'repogroup',
    l = 'lang',
    language = 'lang',
    m = 'message',
    msg = 'message',
    r = 'repo',
    revision = 'rev',
    since = 'after',
    until = 'before',
}
/* eslint-enable unicorn/prevent-abbreviations */

export const ALIASES: Record<string, string> = {
    r: 'repo',
    g: 'repogroup',
    f: 'file',
    l: 'lang',
    language: 'language',
    since: 'after',
    until: 'before',
    m: 'message',
    msg: 'message',
    revision: 'rev',
}

export const resolveFieldAlias = (field: string): string => ALIASES[field] || field

export const isFilterType = (filter: string): filter is FilterType => filter in FilterType
export const isAliasedFilterType = (filter: string): boolean => filter in AliasedFilterType

export const filterTypeKeys: FilterType[] = Object.keys(FilterType) as FilterType[]
export const filterTypeKeysWithAliases: (FilterType | AliasedFilterType)[] = [
    ...filterTypeKeys,
    ...Object.keys(AliasedFilterType),
] as (FilterType | AliasedFilterType)[]

export enum NegatedFilters {
    author = '-author',
    committer = '-committer',
    content = '-content',
    f = '-f',
    file = '-file',
    l = '-l',
    lang = '-lang',
    message = '-message',
    r = '-r',
    repo = '-repo',
    repohasfile = '-repohasfile',
}

/** The list of filters that are able to be negated. */
export type NegatableFilter =
    | FilterType.repo
    | FilterType.file
    | FilterType.repohasfile
    | FilterType.lang
    | FilterType.content
    | FilterType.committer
    | FilterType.author
    | FilterType.message

export const isNegatableFilter = (filter: FilterType): filter is NegatableFilter =>
    Object.keys(NegatedFilters).includes(filter)

/** The list of all negated filters. i.e. all valid filters that have `-` as a suffix. */
export const negatedFilters = Object.values(NegatedFilters)

export const isNegatedFilter = (filter: string): filter is NegatedFilters =>
    negatedFilters.includes(filter as NegatedFilters)

const negatedFilterToNegatableFilter: { [key: string]: NegatableFilter } = {
    '-author': FilterType.author,
    '-committer': FilterType.committer,
    '-content': FilterType.content,
    '-f': FilterType.file,
    '-file': FilterType.file,
    '-l': FilterType.lang,
    '-lang': FilterType.lang,
    '-message': FilterType.message,
    '-r': FilterType.repo,
    '-repo': FilterType.repo,
    '-repohasfile': FilterType.repohasfile,
}

export const resolveNegatedFilter = (filter: NegatedFilters): NegatableFilter => negatedFilterToNegatableFilter[filter]

/**
 * Completion values generated by filters. By default
 * the completion value is `label`, unless `insertText`
 * is specified, which overrides the default completion
 * value. If asSnippet is set, the insertText will be
 * treated as a snippet for completion.
 */
export interface Completion {
    label: string
    insertText?: string
    asSnippet?: boolean
}

interface BaseFilterDefinition {
    alias?: string
    description: string
    discreteValues?: (value: Literal | undefined, isSourcegraphDotCom?: boolean) => Completion[]
    suggestions?: SearchSuggestion['__typename']
    default?: string
    /** Whether the filter may only be used 0 or 1 times in a query. */
    singular?: boolean
}

interface NegatableFilterDefinition extends Omit<BaseFilterDefinition, 'description'> {
    negatable: true
    description: (negated: boolean) => string
}

export type FilterDefinition = BaseFilterDefinition | NegatableFilterDefinition

export const LANGUAGES: string[] = [
    'Assembly',
    'Bash',
    'C',
    'C++',
    'C#',
    'CSS',
    'Dart',
    'Elixir',
    'Erlang',
    'Go',
    'GraphQL',
    'Groovy',
    'Haskell',
    'HTML',
    'Java',
    'JavaScript',
    'Kotlin',
    'JSON',
    'Julia',
    'Lua',
    'Markdown',
    'Objective-C',
    'OCaml',
    'PHP',
    'PowerShell',
    'Python',
    'R',
    'Ruby',
    'Rust',
    'Sass',
    'Scala',
    'SQL',
    'Swift',
    'TypeScript',
    'VBA',
    'XML',
    'Zig',
]

const SOURCEGRAPH_DOT_COM_REPO_COMPLETION: Completion[] = [
    {
        label: 'Search a GitHub organization',
        // eslint-disable-next-line no-template-curly-in-string
        insertText: '^github\\.com/${1:ORGANIZATION}/.*',
        asSnippet: true,
    },
    {
        label: 'Search a single GitHub repository',
        // eslint-disable-next-line no-template-curly-in-string
        insertText: '^github\\.com/${1:ORGANIZATION}/${2:REPO-NAME}$',
        asSnippet: true,
    },
    {
        label: 'Search for repositories with fuzzy string search',
        // eslint-disable-next-line no-template-curly-in-string
        insertText: '${1:STRING}',
        asSnippet: true,
    },
]

export const FILTERS: Record<NegatableFilter, NegatableFilterDefinition> &
    Record<Exclude<FilterType, NegatableFilter>, BaseFilterDefinition> = {
    [FilterType.after]: {
        alias: 'since',
        description: 'Commits made after a certain date',
    },
    [FilterType.archived]: {
        description: 'Include results from archived repositories.',
        singular: true,
    },
    [FilterType.author]: {
        negatable: true,
        description: negated => `${negated ? 'Exclude' : 'Include only'} commits or diffs authored by a user.`,
    },
    [FilterType.before]: {
        alias: 'unitl',
        description: 'Commits made before a certain date',
    },
    [FilterType.case]: {
        description: 'Treat the search pattern as case-sensitive.',
        discreteValues: () => ['yes', 'no'].map(value => ({ label: value })),
        default: 'no',
        singular: true,
    },
    [FilterType.committer]: {
        description: (negated: boolean): string =>
            `${negated ? 'Exclude' : 'Include only'} commits and diffs committed by a user.`,
        negatable: true,
        singular: true,
    },
    [FilterType.content]: {
        description: (negated: boolean): string =>
            `${negated ? 'Exclude' : 'Include only'} results from files if their content matches the search pattern.`,
        negatable: true,
        singular: true,
    },
    [FilterType.context]: {
        description: 'Search only repositories within a specified context',
        singular: true,
        suggestions: 'SearchContext',
    },
    [FilterType.count]: {
        description: 'Number of results to fetch (integer) or "all"',
        singular: true,
    },
    [FilterType.file]: {
        alias: 'f',
        negatable: true,
        description: negated =>
            `${negated ? 'Exclude' : 'Include only'} results from files matching the given search pattern.`,
        suggestions: 'File',
    },
    [FilterType.fork]: {
        discreteValues: () => ['yes', 'no', 'only'].map(value => ({ label: value })),
        description: 'Include results from forked repositories.',
        singular: true,
    },
    [FilterType.lang]: {
        alias: 'l',
        discreteValues: () => LANGUAGES.map(value => ({ label: value })),
        negatable: true,
        description: negated => `${negated ? 'Exclude' : 'Include only'} results from the given language`,
    },
    [FilterType.message]: {
        alias: 'm',
        negatable: true,
        description: negated =>
            `${negated ? 'Exclude' : 'Include only'} Commits with messages matching a certain string`,
    },
    [FilterType.patterntype]: {
        discreteValues: () => ['regexp', 'literal', 'structural'].map(value => ({ label: value })),
        description: 'The pattern type (regexp, literal, structural) in use',
        singular: true,
    },
    [FilterType.repo]: {
        alias: 'r',
        negatable: true,
        discreteValues: (_value, isSourcegraphDotCom) => [
            ...(isSourcegraphDotCom === true ? SOURCEGRAPH_DOT_COM_REPO_COMPLETION : []),
            ...predicateCompletion('repo'),
        ],
        description: negated =>
            `${negated ? 'Exclude' : 'Include only'} results from repositories matching the given search pattern.`,
        suggestions: 'Repository',
    },
    [FilterType.repogroup]: {
        alias: 'g',
        description: 'group-name (include results from the named group)',
        singular: true,
        suggestions: 'RepoGroup',
    },
    [FilterType.repohascommitafter]: {
        description: '"string specifying time frame" (filter out stale repositories without recent commits)',
        singular: true,
    },
    [FilterType.repohasfile]: {
        negatable: true,
        description: negated =>
            `${negated ? 'Exclude' : 'Include only'} results from repos that contain a matching file`,
    },
    [FilterType.rev]: {
        alias: 'rev',
        description: 'Search a revision (branch, commit hash, or tag) instead of the default branch.',
        singular: true,
    },
    [FilterType.select]: {
        discreteValues: value => selectorCompletion(value).map(value => ({ label: value })),
        description: 'Selects the kind of result to display.',
        singular: true,
    },
    [FilterType.timeout]: {
        description: 'Duration before timeout',
        singular: true,
    },
    [FilterType.type]: {
        description: 'Limit results to the specified type.',
        discreteValues: () => ['diff', 'commit', 'symbol', 'repo', 'path', 'file'].map(value => ({ label: value })),
    },
    [FilterType.visibility]: {
        discreteValues: () => ['any', 'private', 'public'].map(value => ({ label: value })),
        description: 'Include results from repositories with the matching visibility (private, public, any).',
        singular: true,
    },
}

export const discreteValueAliases: { [key: string]: string[] } = {
    yes: ['yes', 'y', 'Y', 'YES', 'Yes', '1', 't', 'T', 'true', 'TRUE', 'True'],
    no: ['n', 'N', 'no', 'NO', 'No', '0', 'f', 'F', 'false', 'FALSE', 'False'],
    only: ['o', 'only', 'ONLY', 'Only'],
}

/**
 * Returns the {@link FilterDefinition} for the given filterType if it exists, or `undefined` otherwise.
 */
export const resolveFilter = (
    filterType: string
):
    | { type: NegatableFilter; negated: boolean; definition: NegatableFilterDefinition }
    | { type: Exclude<FilterType, NegatableFilter>; definition: BaseFilterDefinition }
    | undefined => {
    filterType = filterType.toLowerCase()

    if (isAliasedFilterType(filterType)) {
        const aliasKey = filterType as keyof typeof AliasedFilterType
        filterType = AliasedFilterType[aliasKey]
    }

    if (isNegatedFilter(filterType)) {
        const type = resolveNegatedFilter(filterType)
        return {
            type,
            definition: FILTERS[type],
            negated: true,
        }
    }
    if (isFilterType(filterType)) {
        if (isNegatableFilter(filterType)) {
            return {
                type: filterType,
                definition: FILTERS[filterType],
                negated: false,
            }
        }
        if (FILTERS[filterType]) {
            return { type: filterType, definition: FILTERS[filterType] }
        }
    }
    for (const [type, definition] of Object.entries(FILTERS as Record<FilterType, FilterDefinition>)) {
        if (definition.alias && filterType === definition.alias) {
            return {
                type: type as Exclude<FilterType, NegatableFilter>,
                definition: definition as BaseFilterDefinition,
            }
        }
    }
    return undefined
}

/**
 * Checks whether a discrete value is valid for a given filter, accounting for valid aliases.
 */
const isValidDiscreteValue = (
    definition: NegatableFilterDefinition | BaseFilterDefinition,
    input: Literal,
    value: string
): boolean => {
    if (
        !definition.discreteValues ||
        definition
            .discreteValues(input)
            .map(value => value.label)
            .includes(value)
    ) {
        return true
    }
    const validDiscreteValuesForDefinition = Object.keys(discreteValueAliases).filter(
        key =>
            !definition.discreteValues ||
            definition
                .discreteValues(input)
                .map(value => value.label)
                .includes(key)
    )

    for (const discreteValue of validDiscreteValuesForDefinition) {
        if (discreteValueAliases[discreteValue].includes(value)) {
            return true
        }
    }
    return false
}

/**
 * Validates a filter given its field and value.
 */
export const validateFilter = (
    field: string,
    value: Filter['value']
): { valid: true } | { valid: false; reason: string } => {
    const typeAndDefinition = resolveFilter(field)
    if (!typeAndDefinition) {
        return { valid: false, reason: 'Invalid filter type.' }
    }
    if (typeAndDefinition.type === FilterType.repo) {
        // Repo filter is made exempt from checking discrete valid values, since a valid `contain` predicate
        // has infinite valid discrete values. TODO(rvantonder): value validation should be separated to
        // account for finite discrete values and exemption of checks.
        return { valid: true }
    }
    if (typeAndDefinition.type === FilterType.lang) {
        // Lang filter is exempt because our discrete completion values are only a subset of all valid
        // language values, which are captured by a Go library. The backend takes care of returning an
        // alert for invalid values.
        return { valid: true }
    }
    const { definition } = typeAndDefinition
    if (definition.discreteValues && (!value || !isValidDiscreteValue(definition, value, value.value))) {
        return {
            valid: false,
            reason: `Invalid filter value, expected one of: ${definition
                .discreteValues(value)
                .map(value => value.label)
                .join(', ')}.`,
        }
    }
    return { valid: true }
}

/**
 * Prepends a \ to spaces, taking care to skip over existing escape sequences. We apply this to
 * regexp field values like repo: and file:.
 *
 * @param value the value to escape
 */
export const escapeSpaces = (value: string): string => {
    const escaped: string[] = []
    let current = 0
    while (value[current]) {
        switch (value[current]) {
            case '\\': {
                if (value[current + 1]) {
                    escaped.push('\\', value[current + 1])
                    current = current + 2 // Continue past escaped value.
                    continue
                }
                escaped.push('\\')
                current = current + 1
                continue
            }
            case ' ': {
                escaped.push('\\', ' ')
                current = current + 1
                continue
            }
            default:
                escaped.push(value[current])
                current = current + 1
                continue
        }
    }
    return escaped.join('')
}
