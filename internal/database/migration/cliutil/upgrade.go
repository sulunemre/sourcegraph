package cliutil

import (
	"context"
	"fmt"

	"github.com/sourcegraph/log"
	"github.com/urfave/cli/v2"

	"github.com/sourcegraph/sourcegraph/internal/oobmigration"
	"github.com/sourcegraph/sourcegraph/lib/errors"
	"github.com/sourcegraph/sourcegraph/lib/output"
)

func Upgrade(logger log.Logger, commandName string, runnerFactory RunnerFactory, outFactory OutputFactory) *cli.Command {
	fromFlag := &cli.StringFlag{
		Name:     "from",
		Usage:    "The source instance version. TODO",
		Required: true,
	}
	toFlag := &cli.StringFlag{
		Name:     "to",
		Usage:    "The target instance version. TODO",
		Required: true,
	}

	action := makeAction(outFactory, func(ctx context.Context, cmd *cli.Context, out *output.Output) error {
		from, ok := oobmigration.NewVersionFromString(fromFlag.Get(cmd))
		if !ok {
			return errors.New("bad format for -from")
		}
		to, ok := oobmigration.NewVersionFromString(toFlag.Get(cmd))
		if !ok {
			return errors.New("bad format for -to")
		}

		// Construct inclusive upgrade version range `[from, to]`. This also checks
		// for known major version upgrades (e.g., 3.0.0 -> 4.0.0) and ensures that
		// the given values are in the correct order (e.g., from < to).
		versionRange, err := oobmigration.MakeUpgradeRange(from, to)
		if err != nil {
			return err
		}

		// TODO - document
		steps, err := planUpgrade(versionRange)
		if err != nil {
			return err
		}

		// TODO - document
		if err := runUpgrade(steps); err != nil {
			return err
		}

		return nil
	})

	return &cli.Command{
		Name:        "upgrade",
		UsageText:   fmt.Sprintf("%s upgrades -from=<version> -to=<version>", commandName),
		Usage:       "TODO",
		Description: ConstructLongHelp(),
		Action:      action,
		Flags: []cli.Flag{
			fromFlag,
			toFlag,
		},
	}
}
