// Command backtest replays stored candles through a strategy and reports its
// performance net of fees and slippage.
//
// It is the measuring instrument the rest of the system is judged by, which
// is why it was built before there was anything to measure. It refuses to
// report a number over data it does not trust, and every simplification it
// makes appears in the report rather than in the code alone.
//
// This binary reads market data and writes a report. It cannot place an
// order: nothing it imports can reach a trading endpoint.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/config"
	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/logger"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/report"
	_backtest_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/usecase"
	_candle_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/repository"
	_candle_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/usecase"
	_datagap_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/repository"
	_datagap_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/usecase"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// exit codes, so a script can tell a refusal from a crash.
const (
	exitOK = 0
	// exitUsage is a bad invocation: unparseable flags, unknown strategy.
	exitUsage = 2
	// exitIncompleteData is the trust gate refusing to run. It is deliberately
	// distinct from a failure: nothing is broken, the data is not good enough.
	exitIncompleteData = 3
	// exitFailure is anything that actually went wrong.
	exitFailure = 1
)

// options are the flags of one run.
type options struct {
	strategyName   string
	from           string
	to             string
	timeframe      string
	allowGaps      string
	out            string
	equity         string
	listStrategies bool
}

func main() {
	os.Exit(run())
}

// run is main's body, separated so deferred cleanup still happens before the
// process exits with a code.
func run() int {
	opts, err := parseFlags(os.Args[1:])
	if err != nil {
		must(fmt.Fprintln(os.Stderr, err))
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		return exitUsage
	}

	log := logger.New(os.Stderr, logger.Options{
		Level:  cfg.App.LogLevel,
		Format: logger.FormatForEnv(cfg.App.Env),
	})
	slog.SetDefault(log)

	if opts.listStrategies {
		printStrategies(os.Stdout)
		return exitOK
	}

	params, err := buildParams(opts, cfg)
	if err != nil {
		must(fmt.Fprintln(os.Stderr, err))
		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.NewPool(ctx, database.PoolOptions{
		DSN:            cfg.Database.URL,
		MaxConns:       cfg.Database.MaxConns,
		ConnectTimeout: cfg.Database.ConnectTimeout,
	})
	if err != nil {
		log.Error("could not open the database", "error", err)
		return exitFailure
	}
	defer pool.Close()

	engine := _backtest_us.NewBacktestUsecaseImpl(
		log,
		_candle_us.NewCandleUsecaseImpl(_candle_repo.NewCandleRepoImpl(pool)),
		_datagap_us.NewDataGapUsecaseImpl(_datagap_repo.NewDataGapRepoImpl(pool)),
		_indicator_us.DefaultSetConfig(),
	)

	result, runErr := engine.Run(ctx, params)

	// A halted run still has a result worth printing: it carries the gaps
	// that caused the halt, which is the whole reason the gate exists.
	if errors.Is(runErr, constants.ErrDataIncomplete) {
		must(fmt.Fprintf(os.Stderr,
			"\nrefusing to report a number over incomplete data:\n  %v\n\n", runErr))
		printGaps(os.Stderr, result)
		must(io.WriteString(os.Stderr,
			"re-run with --allow-gaps=skip to exclude these ranges, or\n"+
				"--allow-gaps=ignore to run through them (the report is then stamped "+
				report.DataIncompleteStamp+").\n"))
		return exitIncompleteData
	}
	if runErr != nil {
		log.Error("backtest failed", "error", runErr)
		return exitFailure
	}

	stats := report.Compute(result)
	if err := report.WriteSummary(os.Stdout, result, stats); err != nil {
		log.Error("could not write the summary", "error", err)
		return exitFailure
	}

	if opts.out != "" {
		if err := writeJSONReport(opts.out, result, stats); err != nil {
			log.Error("could not write the json report", "error", err)
			return exitFailure
		}
		must(fmt.Fprintf(os.Stdout, "\njson report written to %s\n", opts.out))
	}
	return exitOK
}

// parseFlags reads the command line.
func parseFlags(args []string) (options, error) {
	var opts options

	fs := flag.NewFlagSet("backtest", flag.ContinueOnError)
	fs.StringVar(&opts.strategyName, "strategy", "", "strategy to run (see --list-strategies)")
	fs.StringVar(&opts.from, "from", "", "start of the range, RFC3339 (required)")
	fs.StringVar(&opts.to, "to", "", "end of the range, RFC3339 (required)")
	fs.StringVar(&opts.timeframe, "timeframe", constants.Timeframe1m.String(), "candle interval to replay")
	fs.StringVar(&opts.allowGaps, "allow-gaps", backtest.GapHalt.String(),
		"what to do about unfilled gaps: halt, skip or ignore")
	fs.StringVar(&opts.out, "out", "", "write the JSON report to this path")
	fs.StringVar(&opts.equity, "initial-equity", "10000", "starting balance in quote currency")
	fs.BoolVar(&opts.listStrategies, "list-strategies", false, "list the strategies this binary can run")

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if opts.listStrategies {
		return opts, nil
	}

	if opts.from == "" || opts.to == "" {
		fs.Usage()
		return options{}, errors.New("--from and --to are required")
	}
	return opts, nil
}

// buildParams turns flags and configuration into a run.
//
// Costs come from configuration and there is no flag that changes them. A
// backtest whose costs could be tuned from the command line would eventually
// be tuned until it looked good.
func buildParams(opts options, cfg *config.Config) (backtest.RunParams, error) {
	from, err := time.Parse(time.RFC3339, opts.from)
	if err != nil {
		return backtest.RunParams{}, fmt.Errorf("--from %q is not an RFC3339 timestamp", opts.from)
	}
	to, err := time.Parse(time.RFC3339, opts.to)
	if err != nil {
		return backtest.RunParams{}, fmt.Errorf("--to %q is not an RFC3339 timestamp", opts.to)
	}

	timeframe, err := constants.ParseTimeframe(opts.timeframe)
	if err != nil {
		return backtest.RunParams{}, fmt.Errorf("--timeframe: %w", err)
	}
	policy, err := backtest.ParseGapPolicy(opts.allowGaps)
	if err != nil {
		return backtest.RunParams{}, fmt.Errorf("--allow-gaps: %w", err)
	}
	equity, err := decimal.NewFromString(opts.equity)
	if err != nil {
		return backtest.RunParams{}, fmt.Errorf("--initial-equity %q is not a number", opts.equity)
	}

	strat, err := lookupStrategy(opts.strategyName)
	if err != nil {
		return backtest.RunParams{}, err
	}

	return backtest.RunParams{
		Symbol:        cfg.Market.Symbol,
		MarketType:    cfg.Market.Type,
		Timeframe:     timeframe,
		From:          from.UTC(),
		To:            to.UTC(),
		InitialEquity: equity,
		Costs: backtest.Costs{
			FeeTakerPct:   cfg.Market.FeeTakerPct,
			SlippageTicks: cfg.Market.SlippageTicks,
			TickSize:      cfg.Market.TickSize,
		},
		GapPolicy: policy,
		Strategy:  strat,
	}, nil
}

// lookupStrategy resolves the --strategy flag.
//
// Phase 04 has no strategies: the engine exists before anything to measure,
// on purpose. The error says so rather than reporting an empty registry as a
// typo, so nobody goes looking for a name that was never there.
func lookupStrategy(name string) (strategy.Strategy, error) {
	if name == "" {
		return nil, errors.New("--strategy is required, but no strategy is registered yet " +
			"(phase 04 builds the engine; strategies arrive in phase 06)")
	}
	return nil, fmt.Errorf("unknown strategy %q: no strategy is registered yet "+
		"(phase 04 builds the engine; strategies arrive in phase 06)", name)
}

// printStrategies lists what can be run.
func printStrategies(w io.Writer) {
	must(io.WriteString(w, "no strategies are registered.\n\n"+
		"The backtest engine is deliberately built before any strategy exists:\n"+
		"without a measuring instrument there is no way to tell whether a\n"+
		"strategy works. Strategies arrive in phase 06 and register here.\n"))
}

// printGaps lists the ranges that stopped a run.
func printGaps(w io.Writer, result backtest.Result) {
	var b strings.Builder
	for _, gap := range result.UnfilledGaps {
		fmt.Fprintf(&b, "  %s .. %s  (%d fill attempts) %s\n",
			gap.GapStart.Format(time.RFC3339), gap.GapEnd.Format(time.RFC3339),
			gap.FillAttempts, gap.Note)
	}
	b.WriteString("\n")
	must(io.WriteString(w, b.String()))
}

// must discards a write result. Failing to write a diagnostic to a closed
// stderr is not something this process can do anything useful about, and
// checking it at every call site would bury the diagnostics themselves.
func must(int, error) {}

// writeJSONReport writes the machine-readable report.
func writeJSONReport(path string, result backtest.Result, stats report.Statistics) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}

	if err := report.WriteJSON(file, report.BuildDocument(result, stats)); err != nil {
		// Closed on the way out, and its error dropped: the write already
		// failed and that is the one worth reporting.
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}
