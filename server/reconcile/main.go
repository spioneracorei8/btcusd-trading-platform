// Command reconcile prints the live-against-backtest comparison offline.
//
// The same report GET /internal/signals/reconciliation serves, from the same
// usecase, so the two cannot disagree. It exists because reading it should not
// require the API to be up, or a JSON pretty-printer to be at hand — the
// numbers are meant to be looked at by a person, and the wait until they mean
// anything is the first thing that person needs to know.
//
// It places no orders and it changes nothing. It reads.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/config"
	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/logger"
	_backtest_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/usecase"
	_candle_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/repository"
	_candle_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/usecase"
	_datagap_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/repository"
	_datagap_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/usecase"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
	_outcome_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome/repository"
	_outcome_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome/usecase"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

type options struct {
	days         int
	from         string
	to           string
	minResolved  int
	skipBacktest bool
}

func main() { os.Exit(run()) }

func run() int {
	opts, code := parseFlags()
	if code != exitOK {
		return code
	}

	// This opens no socket, so demanding a listen port from it would be
	// asking for a value it has no use for — the same reason the backtest CLI
	// does not want one.
	cfg, err := config.Load(config.WithoutHTTPServer())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	// Diagnostics to stderr so the report on stdout stays pipeable.
	log := logger.New(os.Stderr, logger.Options{
		Level:  cfg.App.LogLevel,
		Format: logger.FormatForEnv(cfg.App.Env),
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.NewPool(ctx, database.PoolOptions{
		DSN:            cfg.Database.URL,
		MaxConns:       cfg.Database.MaxConns,
		ConnectTimeout: cfg.Database.ConnectTimeout,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitError
	}
	defer pool.Close()

	params, err := opts.params(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}

	usecase, err := _outcome_us.NewReconcileUsecaseImpl(
		_outcome_repo.NewReconcileRepoImpl(pool), log,
		_outcome_us.ReconcileConfig{
			Backtest: _outcome_us.EngineComparer{
				Engine: _backtest_us.NewBacktestUsecaseImpl(
					log,
					_candle_us.NewCandleUsecaseImpl(_candle_repo.NewCandleRepoImpl(pool)),
					_datagap_us.NewDataGapUsecaseImpl(_datagap_repo.NewDataGapRepoImpl(pool)),
					_indicator_us.DefaultSetConfig(),
				),
				Timeframe:  cfg.Strategy.Timeframe,
				Costs:      cfg.BacktestCosts(),
				Equity:     decimal.RequireFromString(constants.DefaultInitialEquity),
				MarketType: cfg.Market.Type,
			},
		},
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitError
	}

	report, err := usecase.Reconcile(ctx, params)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitError
	}

	render(os.Stdout, report)
	return exitOK
}

func parseFlags() (options, int) {
	var opts options

	fs := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	fs.IntVar(&opts.days, "days", 365, "window ending now, in days")
	fs.StringVar(&opts.from, "from", "", "window start (RFC3339); overrides --days")
	fs.StringVar(&opts.to, "to", "", "window end (RFC3339); defaults to now")
	fs.IntVar(&opts.minResolved, "min-resolved", constants.ReconcileMinResolved,
		"resolved signals a group needs before its numbers mean anything")
	fs.BoolVar(&opts.skipBacktest, "skip-backtest", false,
		"report the live side alone, without replaying the engine")

	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "reconcile — compare live signal outcomes against backtest predictions.")
		fmt.Fprint(fs.Output(), "\nIt reads. It places no orders and changes nothing.\n\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		return opts, exitUsage
	}
	return opts, exitOK
}

// params turns the flags into a window.
func (o options) params(cfg *config.Config) (outcome.ReconcileParams, error) {
	params := outcome.ReconcileParams{
		Symbol:       cfg.Market.Symbol,
		MarketType:   cfg.Market.Type,
		To:           time.Now().UTC(),
		MinResolved:  o.minResolved,
		SkipBacktest: o.skipBacktest,
	}

	if o.to != "" {
		at, err := time.Parse(time.RFC3339, o.to)
		if err != nil {
			return outcome.ReconcileParams{}, fmt.Errorf("--to %q is not an RFC3339 time", o.to)
		}
		params.To = at.UTC()
	}

	if o.days <= 0 {
		return outcome.ReconcileParams{}, fmt.Errorf("--days must be positive")
	}
	params.From = params.To.AddDate(0, 0, -o.days)

	if o.from != "" {
		at, err := time.Parse(time.RFC3339, o.from)
		if err != nil {
			return outcome.ReconcileParams{}, fmt.Errorf("--from %q is not an RFC3339 time", o.from)
		}
		params.From = at.UTC()
	}

	if params.To.Before(params.From) {
		return outcome.ReconcileParams{}, fmt.Errorf(
			"the window ends (%s) before it starts (%s)",
			params.To.Format(time.RFC3339), params.From.Format(time.RFC3339))
	}
	return params, nil
}
