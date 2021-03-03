package commands

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/filecoin-project/sentinel-visor/schedule"
	"github.com/filecoin-project/specs-actors/actors/builtin"
	"github.com/urfave/cli/v2"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/sentinel-visor/chain"
	"github.com/filecoin-project/sentinel-visor/model"
	"github.com/filecoin-project/sentinel-visor/storage"

	"encoding/json"
	"github.com/fsnotify/fsnotify"
	"io/ioutil"
)

var Watch = &cli.Command{
	Name:  "watch",
	Usage: "Watch the head of the filecoin blockchain and process blocks as they arrive.",
	Flags: []cli.Flag{
		&cli.IntFlag{
			Name:    "indexhead-confidence",
			Usage:   "Sets the size of the cache used to hold tipsets for possible reversion before being committed to the database",
			Value:   2,
			EnvVars: []string{"VISOR_INDEXHEAD_CONFIDENCE"},
		},
		&cli.StringFlag{
			Name:    "tasks",
			Usage:   "Comma separated list of tasks to run. Each task is reported separately in the database.",
			Value:   strings.Join([]string{chain.BlocksTask, chain.MessagesTask, chain.ChainEconomicsTask, chain.ActorStatesRawTask}, ","),
			EnvVars: []string{"VISOR_WATCH_TASKS"},
		},
		&cli.StringFlag{
			Name:  "config-file",
			Usage: "Config file of visor",
			Value: "./visor.conf",
		},
	},
	Action: watch,
}

func watch(cctx *cli.Context) error {
	tasks := strings.Split(cctx.String("tasks"), ",")
	configFile := cctx.String("config-file")

	if err := setupLogging(cctx); err != nil {
		return xerrors.Errorf("setup logging: %w", err)
	}

	if err := setupMetrics(cctx); err != nil {
		return xerrors.Errorf("setup metrics: %w", err)
	}

	tcloser, err := setupTracing(cctx)
	if err != nil {
		return xerrors.Errorf("setup tracing: %w", err)
	}
	defer tcloser()

	lensOpener, lensCloser, err := setupLens(cctx)
	if err != nil {
		return xerrors.Errorf("setup lens: %w", err)
	}
	defer func() {
		lensCloser()
	}()

	var storage model.Storage = &storage.NullStorage{}
	if cctx.String("db") == "" {
		log.Warnw("database not specified, data will not be persisted")
	} else {
		db, err := setupDatabase(cctx)
		if err != nil {
			return xerrors.Errorf("setup database: %w", err)
		}
		storage = db
	}

	tsIndexer, err := chain.NewTipSetIndexer(lensOpener, storage, builtin.EpochDurationSeconds*time.Second, cctx.String("name"), tasks)
	if err != nil {
		return xerrors.Errorf("setup indexer: %w", err)
	}

	go func() {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return
		}

		err = watcher.Add(configFile)
		if err != nil {
			return
		}

		updater := func() {
			buf, err := ioutil.ReadFile(configFile)
			if err != nil {
				log.Errorf("cannot read %v [%v]", configFile, err)
				return
			}
			filter := struct {
				AddressesFilter []string `json:"addresses_filter"`
			}{}
			err = json.Unmarshal(buf, &filter)
			if err != nil {
				log.Errorf("cannot parse file %v [%v]", configFile, err)
			}
			log.Infof("add filter to indexer: %v", filter.AddressesFilter)
			tsIndexer.SetAddressFilter(chain.NewAddressFilter(filter.AddressesFilter))
		}

		updater()

		defer watcher.Close()

		ticker := time.NewTicker(5 * time.Minute)

		for {
			select {
			case ev, ok := <-watcher.Events:
				if !ok {
					log.Errorf("cannot watch file %v", configFile)
					return
				}
				if ev.Op&fsnotify.Write == fsnotify.Write {
					updater()
				}
			case ev := <-watcher.Errors:
				log.Errorf("error watch file %v [%v]", configFile, ev)
				return
			case <-ticker.C:
				updater()
			}
		}
	}()

	scheduler := schedule.NewScheduler(cctx.Duration("task-delay"))
	scheduler.Add(schedule.TaskConfig{
		Name: "Watcher",
		Task: chain.NewWatcher(tsIndexer, lensOpener, cctx.Int("indexhead-confidence")),
		// TODO: add locker
		// Locker:              NewGlobalSingleton(ChainHeadIndexerLockID, rctx.db), // only want one forward indexer anywhere to be running
		RestartOnFailure:    true,
		RestartOnCompletion: true, // we always want the indexer to be running
		RestartDelay:        time.Minute,
	})

	// Start the scheduler and wait for it to complete or to be cancelled.
	err = scheduler.Run(cctx.Context)
	if !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
