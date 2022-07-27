package cliutil

import (
	"fmt"

	"github.com/sourcegraph/sourcegraph/lib/errors"
)

// TODO - document
// TODO - implement
func runUpgrade(steps []upgradeStep) error {
	fmt.Printf("PLAN:\n")
	for _, step := range steps {
		fmt.Printf("  - Upgrade schemas to %s:\n", step.InstanceVersion)
		for schemaName, leafMigrationIDs := range step.LeafMigrationIDsBySchema {
			fmt.Printf("    - Upgrade schema %q leaves=%v\n", schemaName, leafMigrationIDs)
		}

		if len(step.OutOfBandMigrationIDs) > 0 {
			fmt.Printf("  - Run/validate out of band migrations:\n")
			for _, id := range step.OutOfBandMigrationIDs {
				fmt.Printf("    - Wait for out of band migration #%d to complete\n", id)
			}
		}
	}

	return errors.New("unimplemented tho")
}
