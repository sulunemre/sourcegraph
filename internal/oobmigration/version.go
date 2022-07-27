package oobmigration

import (
	"fmt"
	"strconv"

	"github.com/sourcegraph/sourcegraph/internal/lazyregexp"
	"github.com/sourcegraph/sourcegraph/lib/errors"
)

type Version struct {
	Major int
	Minor int
}

func NewVersion(major, minor int) Version {
	return Version{
		Major: major,
		Minor: minor,
	}
}

var versionPattern = lazyregexp.New(`^v?(\d+)\.(\d+)(?:\.\d+)?$`)

// TODO - document
func NewVersionFromString(v string) (Version, bool) {
	if matches := versionPattern.FindStringSubmatch(v); len(matches) >= 3 {
		major, _ := strconv.Atoi(matches[1])
		minor, _ := strconv.Atoi(matches[2])

		return NewVersion(major, minor), true
	}

	return Version{}, false
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

func (v Version) GitTag() string {
	return fmt.Sprintf("v%d.%d.0", v.Major, v.Minor)
}

// TODO - document
func MakeUpgradeRange(from, to Version) ([]Version, error) {
	if CompareVersions(from, to) == VersionOrderAfter {
		return nil, errors.Newf("invalid range (from=%s > to=%s)", from, to)
	}

	var versions []Version
	for v := from; CompareVersions(v, to) != VersionOrderAfter; v = bump(v) {
		versions = append(versions, v)
	}

	return versions, nil
}

// TODO - update
var lastInSeries = map[int]int{
	3: 47, // 3.47.0 -> 4.0.0
}

// TODO - document
func bump(v Version) Version {
	if lastInSeries[v.Major] == v.Minor {
		return NewVersion(v.Major+1, 0)
	}

	return NewVersion(v.Major, v.Minor+1)
}

type VersionOrder int

const (
	VersionOrderBefore VersionOrder = iota
	VersionOrderEqual
	VersionOrderAfter
)

// CompareVersions returns the relationship between `a (op) b`.
func CompareVersions(a, b Version) VersionOrder {
	for _, pair := range [][2]int{
		{a.Major, b.Major},
		{a.Minor, b.Minor},
	} {
		if pair[0] < pair[1] {
			return VersionOrderBefore
		}
		if pair[0] > pair[1] {
			return VersionOrderAfter
		}
	}

	return VersionOrderEqual
}

// pointIntersectsInterval returns true if point falls within the interval [lower, upper].
func pointIntersectsInterval(lower, upper, point Version) bool {
	return CompareVersions(point, lower) != VersionOrderBefore && CompareVersions(upper, point) != VersionOrderBefore
}
