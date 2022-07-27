package cliutil

import (
	"github.com/sourcegraph/sourcegraph/internal/database/migration/definition"
	"github.com/sourcegraph/sourcegraph/internal/database/migration/schemas"
	"github.com/sourcegraph/sourcegraph/internal/database/migration/stitch"
	"github.com/sourcegraph/sourcegraph/internal/oobmigration"
)

// TODO - document
type upgradeStep struct {
	InstanceVersion          oobmigration.Version
	LeafMigrationIDsBySchema map[string][]int
	OutOfBandMigrationIDs    []int
}

// TODO - document
func planUpgrade(versionRange []oobmigration.Version) ([]upgradeStep, error) {
	if len(versionRange) == 0 {
		return nil, nil
	}
	from, to := versionRange[0], versionRange[len(versionRange)-1]

	// TODO - document
	metadataBySchemaName, err := metadataBySchemaNameForVersion(versionRange)
	if err != nil {
		return nil, err
	}
	makeUpgradeStep := func(version oobmigration.Version, migrationIDs []int) upgradeStep {
		leavesBySchemaName := make(map[string][]int, len(metadataBySchemaName))
		for schemaName, metadata := range metadataBySchemaName {
			leavesBySchemaName[schemaName] = metadata.leafIDsByRev[version.GitTag()]
		}

		return upgradeStep{
			InstanceVersion:          version,
			LeafMigrationIDsBySchema: leavesBySchemaName,
			OutOfBandMigrationIDs:    migrationIDs,
		}
	}

	// TODO - document
	interrupts, err := oobmigration.ScheduleMigrationInterrupts(from, to)
	if err != nil {
		return nil, err
	}

	// TODO - document
	steps := make([]upgradeStep, 0, len(interrupts)+1)
	for _, interrupt := range interrupts {
		steps = append(steps, makeUpgradeStep(interrupt.Version, interrupt.MigrationIDs))
	}

	// TODO - document
	return append(steps, makeUpgradeStep(to, nil)), nil
}

// TODO - document
type migrationGraphMetadata struct {
	definitions  *definition.Definitions
	leafIDsByRev map[string][]int
}

// TODO - document
// TODO - precompile all of this
func metadataBySchemaNameForVersion(versionRange []oobmigration.Version) (map[string]migrationGraphMetadata, error) {
	metadataBySchemaName := map[string]migrationGraphMetadata{}
	for _, schemaName := range schemas.SchemaNames {
		definitions, leafIDsByRev, err := stitch.StitchDefinitions(schemaName, "/Users/efritz/dev/sourcegraph/sourcegraph", gitTags(versionRange))
		if err != nil {
			return nil, err
		}

		metadataBySchemaName[schemaName] = migrationGraphMetadata{definitions, leafIDsByRev}
	}

	return metadataBySchemaName, nil
}

// TODO - document
func gitTags(versions []oobmigration.Version) []string {
	tags := make([]string, 0, len(versions))
	for _, version := range versions {
		tags = append(tags, version.GitTag())
	}

	return tags
}
