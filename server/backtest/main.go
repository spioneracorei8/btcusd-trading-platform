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
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/config"
	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/logger"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/report"
	_backtest_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	_candle_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/repository"
	_candle_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/candle/usecase"
	_datagap_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/repository"
	_datagap_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap/usecase"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
	_strategy_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
	_trend_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/trend/usecase"
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
	strategyName    string
	from            string
	to              string
	timeframe       string
	allowGaps       string
	out             string
	equity          string
	trendFilter     string
	noTrendFilter   bool
	compare         bool
	dataset         string
	riskPct         string
	allIn           bool
	costSweep       bool
	holdoutNote     string
	listStrategies  bool
	experimentLog   string
	noExperimentLog bool

	neighbourhood bool

	// params and filterParams are the overrides collected from repeated
	// --param / --filter-param flags.
	params       paramFlag
	filterParams paramFlag
}

// paramFlag collects repeated key=value flags.
//
// A map rather than a slice: setting the same key twice is a mistake worth
// reporting, not a silent last-one-wins. A run that quietly ignored half of
// what was typed would be recorded in the experiment log under parameters it
// did not use.
type paramFlag map[string]string

func (f paramFlag) String() string {
	if len(f) == 0 {
		return ""
	}

	keys := make([]string, 0, len(f))
	for key := range f {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+f[key])
	}
	return strings.Join(parts, " ")
}

func (f paramFlag) Set(raw string) error {
	key, value, found := strings.Cut(raw, "=")
	key = strings.TrimSpace(key)

	if !found || key == "" {
		return fmt.Errorf("%q is not key=value", raw)
	}
	if _, dup := f[key]; dup {
		return fmt.Errorf("%s given twice", key)
	}
	f[key] = strings.TrimSpace(value)
	return nil
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

	// The CLI opens no socket, so HTTP_PORT is none of its business. It also
	// loads the repository .env itself, which is what stops every invocation
	// having to be preceded by sourcing the same file.
	cfg, err := config.Load(config.WithoutHTTPServer())
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		return exitUsage
	}

	log := logger.New(os.Stderr, logger.Options{
		Level:  cfg.App.LogLevel,
		Format: logger.FormatForEnv(cfg.App.Env),
	})
	slog.SetDefault(log)

	// Which file the configuration came from, because "why is it using that
	// symbol" is otherwise answered by guessing. Debug rather than info: it is
	// noise on a run that is working and the first thing asked for on one that
	// is not.
	if cfg.EnvFile != "" {
		log.Debug("configuration filled from an environment file", "path", cfg.EnvFile)
	}

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

	candles := _candle_us.NewCandleUsecaseImpl(_candle_repo.NewCandleRepoImpl(pool))
	engine := _backtest_us.NewBacktestUsecaseImpl(
		log,
		candles,
		_datagap_us.NewDataGapUsecaseImpl(_datagap_repo.NewDataGapRepoImpl(pool)),
		_indicator_us.DefaultSetConfig(),
	)

	if opts.compare {
		// --compare measures a filter's contribution by running the same
		// strategy with and without it. A strategy whose entry condition is
		// already multi-timeframe agreement has no unfiltered counterpart:
		// removing the filter does not remove the alignment, so the two sides
		// would differ by whatever second gate happened to be configured and
		// the difference would be attributed to the wrong thing.
		//
		// Refused rather than answered. A comparison that means something
		// different from what it says is worse than no comparison.
		if multi, ok := strategyIsMultiTimeframe(opts, cfg); ok {
			log.Error("--compare does not apply to this strategy",
				"strategy", multi.Name(), "timeframes", multi.RequiredTimeframes())
			must(fmt.Fprintf(os.Stderr,
				"\n%s reads %v itself: alignment is its entry condition, not a veto\n"+
					"applied to one. There is no unfiltered counterpart to compare it\n"+
					"against — run it with --no-trend-filter and read its own numbers.\n\n",
				multi.Name(), multi.RequiredTimeframes()))
			return exitFailure
		}
		return runComparison(ctx, log, engine, candles, params, opts, cfg)
	}

	if opts.neighbourhood {
		return runNeighbourhood(ctx, log, engine, candles, params, opts, cfg)
	}

	params, err = prepareRun(params, opts, cfg, candles, opts.trendFilter != "")
	if err != nil {
		log.Error("could not prepare the run", "error", err)
		return exitFailure
	}

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

	// The failure modes, always. A strategy can clear every threshold and
	// still be a few lucky bars, and the headline figures cannot tell.
	analysis := report.Analyse(result, stats)
	if err := report.WriteAnalysis(os.Stdout, analysis, quoteUnit(params.Symbol)); err != nil {
		log.Error("could not write the analysis", "error", err)
		return exitFailure
	}

	var sweep []report.CostSensitivity
	if opts.costSweep {
		sweep, err = runCostSweep(ctx, log, engine, candles, params, opts, cfg)
		if err != nil {
			log.Error("could not run the cost sweep", "error", err)
			return exitFailure
		}
	}

	// Recorded before the holdout log and before the JSON report, because a
	// run that happened is a run the denominator has to include. Everything
	// after this point is output; the run itself is already finished.
	if err := recordExperiment(log, opts, result, stats, analysis, sweep, nil); err != nil {
		log.Error("could not append to the experiment log", "error", err)
		return exitFailure
	}

	// The holdout is recorded before the numbers are read, so a run whose
	// result was disliked is still on the record.
	if dataset, err := backtest.ParseDataset(opts.dataset); err == nil && dataset.Spent() {
		if err := report.AppendHoldoutUse(holdoutLogPath(),
			report.HoldoutEntryFor(result, stats, time.Now().UTC(), opts.holdoutNote)); err != nil {
			log.Error("could not append to the holdout log", "error", err)
			return exitFailure
		}
		must(fmt.Fprintf(os.Stdout,
			"\nthis holdout use was recorded in %s\n", holdoutLogPath()))
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
	fs.StringVar(&opts.from, "from", "", "start of the range, RFC3339 (overrides --dataset)")
	fs.StringVar(&opts.to, "to", "", "end of the range, RFC3339 (overrides --dataset)")
	fs.StringVar(&opts.dataset, "dataset", backtest.DatasetDev.String(),
		"dev (2023-2024, iterate freely) or holdout (2025+, run once — every use is logged)")
	fs.StringVar(&opts.riskPct, "risk-pct", "1", "percent of equity risked per trade")
	fs.BoolVar(&opts.allIn, "all-in", false,
		"commit the whole account per trade instead of sizing against the stop")
	fs.BoolVar(&opts.costSweep, "cost-sweep", false,
		"also run at 1.5x and 2x the assumed cost and print the sensitivity")
	fs.StringVar(&opts.holdoutNote, "note", "", "note recorded in the holdout log")
	fs.StringVar(&opts.timeframe, "timeframe", constants.Timeframe1m.String(), "candle interval to replay")
	fs.StringVar(&opts.allowGaps, "allow-gaps", backtest.GapHalt.String(),
		"what to do about unfilled gaps: halt, skip or ignore")
	fs.StringVar(&opts.out, "out", "", "write the JSON report to this path")
	fs.StringVar(&opts.equity, "initial-equity", constants.DefaultInitialEquity,
		"starting balance in quote currency")
	fs.StringVar(&opts.trendFilter, "trend-filter", _trend_us.FilterName,
		"multi-timeframe trend filter to gate entries with")
	fs.BoolVar(&opts.noTrendFilter, "no-trend-filter", false,
		"run unfiltered; this is the control a filtered run is compared against")
	fs.BoolVar(&opts.compare, "compare", false,
		"run twice, filtered and unfiltered, and print both side by side")
	fs.BoolVar(&opts.listStrategies, "list-strategies", false, "list the strategies this binary can run")
	fs.BoolVar(&opts.neighbourhood, "neighbourhood", false,
		"also run one step either side of every --param, and print them together")
	fs.StringVar(&opts.experimentLog, "experiment-log", report.ExperimentLogPath,
		"append every completed run to this log")
	opts.params = paramFlag{}
	opts.filterParams = paramFlag{}
	fs.Var(opts.params, "param",
		"strategy parameter as key=value; repeatable (see --list-strategies)")
	fs.Var(opts.filterParams, "filter-param",
		"trend filter parameter as key=value; repeatable")
	fs.BoolVar(&opts.noExperimentLog, "no-experiment-log", false,
		"withhold this run's details from the experiment log; the entry number is still spent")

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if opts.listStrategies {
		return opts, nil
	}

	dataset, err := backtest.ParseDataset(opts.dataset)
	if err != nil {
		return options{}, fmt.Errorf("--dataset: %w", err)
	}
	// Explicit dates mean the run is neither of the two sets that carry
	// meaning, and it must not be labelled as one.
	if (opts.from != "" || opts.to != "") && dataset != backtest.DatasetCustom {
		if opts.from == "" || opts.to == "" {
			return options{}, errors.New("--from and --to must be given together")
		}
		opts.dataset = backtest.DatasetCustom.String()
	}
	if opts.noTrendFilter && opts.compare {
		return options{}, errors.New(
			"--no-trend-filter and --compare contradict each other: comparing needs the filtered run")
	}
	if opts.noTrendFilter {
		opts.trendFilter = ""
	}
	if opts.trendFilter != "" && opts.trendFilter != _trend_us.FilterName {
		return options{}, fmt.Errorf("unknown trend filter %q; this binary ships %q",
			opts.trendFilter, _trend_us.FilterName)
	}
	return opts, nil
}

// buildParams turns flags and configuration into a run.
//
// Costs come from configuration and there is no flag that changes them. A
// backtest whose costs could be tuned from the command line would eventually
// be tuned until it looked good.
func buildParams(opts options, cfg *config.Config) (backtest.RunParams, error) {
	dataset, err := backtest.ParseDataset(opts.dataset)
	if err != nil {
		return backtest.RunParams{}, err
	}

	from, to, err := resolveRange(opts, dataset)
	if err != nil {
		return backtest.RunParams{}, err
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

	// A spot account cannot short, so a two-sided rule is built long-only
	// there. The engine's hard refusal stays as the backstop.
	longOnly := cfg.Market.Type == constants.MarketTypeSpot

	strategyOverrides, exitOverrides, err := splitOverrides(opts.params)
	if err != nil {
		return backtest.RunParams{}, err
	}

	strat, changedParams, err := lookupStrategy(
		opts.strategyName, strategyOverrides, roundTripCostPct(cfg), longOnly)
	if err != nil {
		return backtest.RunParams{}, err
	}

	exits, changedExits, err := buildExits(exitOverrides)
	if err != nil {
		return backtest.RunParams{}, err
	}
	changedParams = append(changedParams, changedExits...)

	sizing := backtest.DefaultSizing()
	if opts.allIn {
		sizing = backtest.AllInSizing()
	} else {
		risk, err := decimal.NewFromString(opts.riskPct)
		if err != nil {
			return backtest.RunParams{}, fmt.Errorf("--risk-pct %q is not a number", opts.riskPct)
		}
		sizing.RiskPct = risk
	}
	sizing.MaxLeverage = cfg.Market.MaxLeverage

	return backtest.RunParams{
		Symbol:        cfg.Market.Symbol,
		MarketType:    cfg.Market.Type,
		Timeframe:     timeframe,
		From:          from.UTC(),
		To:            to.UTC(),
		InitialEquity: equity,
		Costs: backtest.Costs{
			FeeTakerPct:   cfg.Market.FeeTakerPct,
			FeeMakerPct:   cfg.Market.FeeMakerPct,
			SlippageTicks: cfg.Market.SlippageTicks,
			TickSize:      cfg.Market.TickSize,

			Model:            cfg.Market.CostModel,
			SpreadPoints:     cfg.Market.SpreadPoints,
			PointValue:       cfg.Market.PointValue,
			ContractSize:     cfg.Market.ContractSize,
			MinLot:           cfg.Market.MinLot,
			LotStep:          cfg.Market.LotStep,
			CommissionPerLot: cfg.Market.CommissionPerLot,
		},
		Execution: backtest.Execution{
			EntryOrderType:   cfg.Market.EntryOrderType,
			ExitOrderType:    cfg.Market.ExitOrderType,
			LimitTimeoutBars: cfg.Market.LimitOrderTimeoutBars,
		},
		GapPolicy:      policy,
		Sizing:         sizing,
		Exits:          exits,
		Strategy:       strat,
		StrategyParams: changedParams,
	}, nil
}

// resolveRange turns a dataset, or explicit dates, into a window.
func resolveRange(opts options, dataset backtest.Dataset) (from, to time.Time, err error) {
	if opts.from != "" || opts.to != "" {
		from, err = time.Parse(time.RFC3339, opts.from)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--from %q is not an RFC3339 timestamp", opts.from)
		}
		to, err = time.Parse(time.RFC3339, opts.to)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--to %q is not an RFC3339 timestamp", opts.to)
		}
		return from.UTC(), to.UTC(), nil
	}

	from, to, ok := dataset.Range(time.Now())
	if !ok {
		return time.Time{}, time.Time{}, fmt.Errorf(
			"--dataset=%s has no range of its own; give --from and --to", dataset)
	}
	return from, to, nil
}

// roundTripCostPct is what one entry and one exit cost in fees, in percent.
// A strategy configuration is validated against it, so a different fee tier
// changes what is accepted.
func roundTripCostPct(cfg *config.Config) float64 {
	fee, _ := cfg.Market.FeeTakerPct.Float64()
	return fee * 2
}

// lookupStrategy resolves the --strategy flag against the registry.
func lookupStrategy(
	name string,
	overrides map[string]string,
	roundTripCostPct float64,
	longOnly bool,
) (strategy.Strategy, []helper.ParamChange, error) {
	if name == "" {
		return nil, nil, fmt.Errorf("--strategy is required; this binary ships %s",
			strings.Join(_strategy_us.Names(), ", "))
	}

	entry, err := _strategy_us.Lookup(name)
	if err != nil {
		return nil, nil, err
	}

	strat, config, err := entry.BuildWith(overrides, roundTripCostPct, longOnly)
	if err != nil {
		return nil, nil, err
	}

	// Measured against a fresh default rather than taken from what was typed,
	// so a parameter passed at its own default is correctly reported as
	// unchanged. The header answers "what is different about this run", not
	// "what did somebody type".
	changed, err := helper.ChangedParams(entry.Defaults(), config)
	if err != nil {
		return nil, nil, err
	}
	return strat, changed, nil
}

// printStrategies lists what can be run.
func printStrategies(w io.Writer) {
	var b strings.Builder
	b.WriteString("registered strategies:\n\n")

	for _, entry := range _strategy_us.All() {
		fmt.Fprintf(&b, "  %s\n", entry.Name)

		specs, err := entry.Params()
		if err != nil {
			fmt.Fprintf(&b, "      (parameters unavailable: %v)\n\n", err)
			continue
		}
		for _, spec := range specs {
			step := spec.Step
			if step == "" {
				step = "-"
			}
			fmt.Fprintf(&b, "      %-18s %-7s default %-10s neighbourhood step %s\n",
				spec.Name, spec.Kind, spec.Default, step)
		}
		b.WriteString("\n")
	}

	// The engine's own exit mechanisms are set through the same flag and
	// stepped by the same --neighbourhood, so they are listed in the same
	// place. Remembering which flag a name lives behind is a way of producing
	// runs configured differently from how they were meant to be.
	b.WriteString("  every strategy, through the engine\n")
	if exits, err := helper.DescribeParams(&backtest.Exits{}); err == nil {
		for _, spec := range exits {
			step := spec.Step
			if step == "" {
				step = "-"
			}
			fmt.Fprintf(&b, "      %-22s %-7s default %-10s neighbourhood step %s\n",
				spec.Name, spec.Kind, spec.Default, step)
		}
	}
	b.WriteString("      (zero disables each of these, which is what every evaluation\n")
	b.WriteString("       before phase 06 ran with)\n\n")

	b.WriteString("Set them with --param name=value, repeated. An unknown name is an error\n")
	b.WriteString("rather than a warning: a typo that ran the default while you believed\n")
	b.WriteString("otherwise is not visible in any report afterwards.\n")

	b.WriteString("\nThese are experiments, not recommendations. Most rules of this kind\n")
	b.WriteString("fail at 1m-5m once 0.1% a round trip is charged; they were chosen for\n")
	b.WriteString("failing in legible ways. Judge every one against docs/acceptance-criteria.md.\n")
	must(io.WriteString(w, b.String()))
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

// attachTrendFilter builds the filter and its aligner onto a run.
//
// The aligner's cursors start a full warm-up before the requested range, so
// the filter has an opinion from the first scored bar rather than spending the
// range earning one. At the production EMA(200) that is six weeks of hourly
// history — which is why it is computed rather than guessed.
func attachTrendFilter(
	params *backtest.RunParams,
	candles candle.CandleUsecase,
	overrides map[string]string,
) error {
	// The contributor set depends on the base: the right timeframes to watch
	// from 1m are not the right ones to watch from 1h (ADR 0018).
	base, err := trend.DefaultConfigFor(params.Timeframe)
	if err != nil {
		return err
	}

	// ForBase still runs. The per-base sets are already above their base, so
	// it drops nothing — but it is what enforces that, and a future edit to
	// the table that got it wrong should fail here rather than silently
	// admit a look-ahead hazard.
	config, err := base.ForBase(params.Timeframe)
	if err != nil {
		return err
	}

	// Overrides land after ForBase, so weight_5m on a 1h run is rejected as the
	// mistake it is rather than silently written to a contributor that had
	// already been dropped for closing no less often than the base.
	defaults := config
	config, err = config.WithParams(overrides)
	if err != nil {
		return err
	}

	changed, err := helper.ChangedParams(defaults, config)
	if err != nil {
		return err
	}
	params.FilterParams = changedFilterParams(defaults, config, changed)

	if err := requireCandles(*params, candles, config.Timeframes()); err != nil {
		return err
	}

	filter, err := _trend_us.NewFilterImpl(config)
	if err != nil {
		return err
	}

	higher := config.Timeframes()
	aligner, err := _trend_us.NewAlignerImpl(_trend_us.AlignerConfig{
		Symbol:     params.Symbol,
		MarketType: params.MarketType,
		Base:       params.Timeframe,
		Higher:     higher,
		From:       trendCursorStart(params, higher),
		To:         params.To,
		Indicators: _indicator_us.DefaultSetConfig(),
	}, candles)
	if err != nil {
		return err
	}

	params.TrendFilter = filter
	params.TrendAligner = aligner
	params.TrendConfig = config
	return nil
}

// attachStrategyAligner gives a multi-timeframe strategy the readings it asked
// for, and does nothing for any other strategy.
//
// # Why this does not reuse the trend filter's aligner
//
// The two answer different questions from different configurations: the filter
// watches what ADR 0018's table says to watch from this base, while a
// multi-timeframe strategy names its own contributors. More practically, such
// a strategy is normally run with --no-trend-filter — alignment is already its
// entry condition, and gating it again would be scoring the same evidence
// twice — so sharing would leave it with nothing to read in exactly the
// configuration it is meant to run in.
func attachStrategyAligner(params *backtest.RunParams, candles candle.CandleUsecase) error {
	multi, ok := params.Strategy.(strategy.MultiTimeframe)
	if !ok {
		return nil
	}

	higher := multi.RequiredTimeframes()
	if len(higher) == 0 {
		return fmt.Errorf("%s declares no timeframes to read", params.Strategy.Name())
	}

	// The same check the filter gets, for the same reason: without stored
	// candles the aligner opens a cursor over an empty series, every reading
	// stays cold, the strategy correctly declines every bar, and the run
	// reports a clean zero rather than an error.
	if err := requireCandles(*params, candles, higher); err != nil {
		return err
	}

	aligner, err := _trend_us.NewAlignerImpl(_trend_us.AlignerConfig{
		Symbol:     params.Symbol,
		MarketType: params.MarketType,
		Base:       params.Timeframe,
		Higher:     higher,
		From:       trendCursorStart(params, higher),
		To:         params.To,
		Indicators: _indicator_us.DefaultSetConfig(),
	}, candles)
	if err != nil {
		return err
	}

	params.StrategyAligner = aligner
	return nil
}

// changedFilterParams appends the per-timeframe weight changes to whatever the
// generic comparison found.
//
// The weights live in a slice rather than a field per timeframe — a map would
// randomise iteration and break the byte-identical report — so they have no
// field for the reflection-based comparison to look at, and are diffed here
// instead.
func changedFilterParams(defaults, configured trend.Config, changed []helper.ParamChange) []helper.ParamChange {
	was := make(map[constants.Timeframe]float64, len(defaults.Weights))
	for _, weight := range defaults.Weights {
		was[weight.Timeframe] = weight.Weight
	}

	for _, weight := range configured.Weights {
		before, existed := was[weight.Timeframe]
		if existed && before == weight.Weight {
			continue
		}
		changed = append(changed, helper.ParamChange{
			Name: "weight_" + weight.Timeframe.String(),
			From: strconv.FormatFloat(before, 'g', -1, 64),
			To:   strconv.FormatFloat(weight.Weight, 'g', -1, 64),
		})
	}
	return changed
}

// requireCandles refuses a filter whose contributors have no stored data.
//
// Without this the run completes and reports nothing wrong: the aligner opens
// a cursor over an empty series, the filter never becomes ready, every bar is
// counted as not-ready, and the result is a full run that gated on nothing.
// That is worse than an error, because it produces a number.
//
// The check is one indexed lookup per contributor — an index scan on the
// candles primary key, measured at well under a millisecond — so it costs
// nothing against a run that is about to replay hundreds of thousands of bars.
func requireCandles(
	params backtest.RunParams,
	candles candle.CandleUsecase,
	timeframes []constants.Timeframe,
) error {
	for _, timeframe := range timeframes {
		_, err := candles.FetchEarliestCandle(
			context.Background(), params.Symbol, params.MarketType, timeframe)
		if errors.Is(err, constants.ErrNotFound) {
			return fmt.Errorf(
				"the trend filter for a %s base needs %s candles and none are stored for %s. "+
					"Add %s to MARKET_TIMEFRAMES and let the collector backfill it, or run with "+
					"--no-trend-filter",
				params.Timeframe, timeframe, params.Symbol, timeframe)
		}
		if err != nil {
			return fmt.Errorf("check stored %s candles: %w", timeframe, err)
		}
	}
	return nil
}

// trendCursorStart is how far before the range each higher-timeframe cursor
// must begin so its indicators are warm when the range opens.
func trendCursorStart(params *backtest.RunParams, higher []constants.Timeframe) time.Time {
	set, err := _indicator_us.NewSet(_indicator_us.DefaultSetConfig())
	if err != nil {
		// DefaultSetConfig is known good; a failure here would be a
		// programming error, and starting at the range is the safe fallback.
		return params.From
	}

	longest := time.Duration(0)
	for _, timeframe := range higher {
		if required := time.Duration(set.WarmupPeriod()) * timeframe.Duration(); required > longest {
			longest = required
		}
	}
	return params.From.Add(-longest)
}

// runComparison runs the same strategy twice and prints the difference.
//
// Two full runs rather than one run with the veto counted but not applied: a
// vetoed entry changes what the strategy is holding on every subsequent bar,
// so the unfiltered path cannot be reconstructed from the filtered one.
func runComparison(
	ctx context.Context,
	log *slog.Logger,
	engine backtest.BacktestUsecase,
	candles candle.CandleUsecase,
	params backtest.RunParams,
	opts options,
	cfg *config.Config,
) int {
	// Both passes are built the same way, from the same base, each with its own
	// state. Previously the unfiltered pass ran on whatever params the caller
	// happened to hand over, which was unfiltered only because the caller
	// returned here before attaching a filter — correct by accident of
	// ordering. Moving the compare check three lines down would have silently
	// made "unfiltered" a second filtered run.
	unfilteredParams, err := prepareRun(params, opts, cfg, candles, false)
	if err != nil {
		log.Error("could not prepare the unfiltered run", "error", err)
		return exitFailure
	}
	unfiltered, err := engine.Run(ctx, unfilteredParams)
	if err != nil {
		log.Error("the unfiltered run failed", "error", err)
		return exitFailure
	}

	filteredParams, err := prepareRun(params, opts, cfg, candles, true)
	if err != nil {
		log.Error("could not prepare the filtered run", "error", err)
		return exitFailure
	}
	filtered, err := engine.Run(ctx, filteredParams)
	if err != nil {
		log.Error("the filtered run failed", "error", err)
		return exitFailure
	}

	comparison := report.NewComparison(unfiltered, filtered)
	if err := report.WriteComparison(os.Stdout, comparison); err != nil {
		log.Error("could not write the comparison", "error", err)
		return exitFailure
	}

	// --cost-sweep asked for alongside --compare used to be dropped on the
	// floor, because this function returns before main reaches the sweep. A
	// silently ignored flag is the same defect as a silently dropped trend
	// contributor. The sweep runs against the filtered configuration, which is
	// the headline of a comparison.
	var sweep []report.CostSensitivity
	if opts.costSweep {
		sweep, err = runCostSweep(ctx, log, engine, candles, filteredParams, opts, cfg)
		if err != nil {
			log.Error("could not run the cost sweep", "error", err)
			return exitFailure
		}
	}

	// A comparison is two runs and one entry: the filtered run is what the
	// comparison is about, and logging both would inflate the denominator with
	// a control that was never a candidate.
	// One entry, carrying both halves. Two invocations of the same cell —
	// once for --compare, once for --cost-sweep — recorded it twice and
	// inflated the denominator the log exists to keep honest.
	if err := recordExperiment(log, opts, filtered,
		comparison.FilteredStats, report.Analyse(filtered, comparison.FilteredStats), sweep,
		&report.ComparisonLine{
			UnfilteredNetReturn: comparison.UnfilteredStats.NetReturn,
			UnfilteredTrades:    comparison.UnfilteredStats.TradeCount,
			FilteredNetReturn:   comparison.FilteredStats.NetReturn,
			FilteredTrades:      comparison.FilteredStats.TradeCount,
		}); err != nil {
		log.Error("could not append to the experiment log", "error", err)
		return exitFailure
	}

	if opts.out != "" {
		if err := writeJSONReport(opts.out, filtered, comparison.FilteredStats); err != nil {
			log.Error("could not write the json report", "error", err)
			return exitFailure
		}
		must(fmt.Fprintf(os.Stdout, "\nfiltered json report written to %s\n", opts.out))
	}
	return exitOK
}

// holdoutLogPath resolves the log relative to the repository root.
//
// The CLI is usually run from server/, so the plain relative path would put
// the log somewhere nobody looks. Walking up to find docs/ keeps every use in
// one file whatever directory the command was typed in — which is the whole
// point of a log that is meant to be read later.
func holdoutLogPath() string {
	return repoRelative(report.HoldoutLogPath)
}

// runCostSweep repeats the run at higher assumed costs.
//
// An edge that vanishes under modest slippage was never robust enough to
// trade. The assumed cost is a guess about a number that moves against a
// retail account over time, so a result that only survives at exactly the
// assumed figure has no margin at all.
func runCostSweep(
	ctx context.Context,
	log *slog.Logger,
	engine backtest.BacktestUsecase,
	candles candle.CandleUsecase,
	params backtest.RunParams,
	opts options,
	cfg *config.Config,
) ([]report.CostSensitivity, error) {
	var runs []report.CostSensitivity

	// Whether the base run was filtered decides whether each pass is. A sweep
	// that quietly dropped the filter would be answering a different question
	// from the run it is attached to.
	withFilter := params.TrendFilter != nil

	for _, multiplier := range []float64{1, 1.5, 2} {
		// Every stateful component rebuilt, not just the strategy. Rebuilding
		// the strategy alone is what left the aligner parked at the end of the
		// range: the second pass then tried to restart at the beginning and
		// hit the forward-only invariant.
		scaled, err := prepareRun(params, opts, cfg, candles, withFilter)
		if err != nil {
			return nil, fmt.Errorf("cost sweep at %.1fx: %w", multiplier, err)
		}

		scaled.Costs = scaleCosts(params.Costs, multiplier)

		result, err := engine.Run(ctx, scaled)
		if err != nil {
			return nil, fmt.Errorf("cost sweep at %.1fx: %w", multiplier, err)
		}
		stats := report.Compute(result)

		runs = append(runs, report.CostSensitivity{
			Multiplier:   multiplier,
			NetReturn:    stats.NetReturn,
			TradeCount:   stats.TradeCount,
			ProfitFactor: stats.ProfitFactor,
		})
		log.Debug("cost sweep run finished",
			"multiplier", multiplier, "net_return", stats.NetReturn)
	}
	return runs, report.WriteCostSensitivity(os.Stdout, runs, report.CostSensitivityHeading(params))
}

// runNeighbourhood runs the chosen configuration and one step either side of
// every parameter that was varied.
//
// # Why only the parameters that were varied
//
// Stepping everything would be a grid search with extra steps, and phase 06
// puts automated optimisation out of scope for a reason: every additional
// combination is another chance for a result to look good by accident, and
// fifty-seven runs already sit in the log. Varying a parameter is a decision
// somebody made; this checks that decision and nothing else.
//
// # One log entry, not five
//
// It is one experiment — "is this value on a plateau" — and recording each row
// separately would inflate the denominator the log exists to protect, then
// invite the best row to be quoted on its own.
func runNeighbourhood(
	ctx context.Context,
	log *slog.Logger,
	engine backtest.BacktestUsecase,
	candles candle.CandleUsecase,
	params backtest.RunParams,
	opts options,
	cfg *config.Config,
) int {
	varied := make([]string, 0, len(opts.params))
	for name := range opts.params {
		varied = append(varied, name)
	}
	sort.Strings(varied)

	if len(varied) == 0 {
		must(io.WriteString(os.Stderr,
			"\n--neighbourhood checks whether the values you chose sit on a plateau,\n"+
				"so it needs values to have been chosen: pass at least one --param.\n"+
				"With none, the only row would be the defaults compared against nothing.\n\n"))
		return exitUsage
	}

	entry, err := _strategy_us.Lookup(opts.strategyName)
	if err != nil {
		must(fmt.Fprintln(os.Stderr, err))
		return exitUsage
	}

	// The base row first, then each parameter moved down and up. Deterministic
	// order, because this table is read by eye and compared against earlier
	// ones.
	type plan struct {
		label     string
		overrides map[string]string
	}
	plans := []plan{{label: report.NeighbourhoodBaseLabel, overrides: opts.params}}

	for _, name := range varied {
		for _, direction := range []int{-1, +1} {
			stepped, err := steppedOverrides(entry, opts.params, name, direction)
			if err != nil {
				must(fmt.Fprintf(os.Stderr, "\n--neighbourhood: %v\n\n", err))
				return exitUsage
			}
			plans = append(plans, plan{
				label:     fmt.Sprintf("%s%+d", name, direction),
				overrides: stepped,
			})
		}
	}

	var (
		rows      []report.NeighbourResult
		baseRun   backtest.Result
		baseStats report.Statistics
		haveBase  bool
	)

	for _, p := range plans {
		row := report.NeighbourResult{Label: p.label}
		for _, name := range varied {
			row.Values = append(row.Values, p.overrides[name])
		}

		runOpts := opts
		runOpts.params = p.overrides

		result, stats, err := runOnce(ctx, engine, candles, params, runOpts, cfg)
		if err != nil {
			// A neighbour that cannot run is a finding, not a reason to stop:
			// a value one step away being *invalid* is itself information
			// about how narrow the chosen one is.
			row.Failed = shortReason(err)
			log.Debug("neighbour did not run", "label", p.label, "error", err)
			rows = append(rows, row)
			continue
		}

		row.NetReturn = stats.NetReturn
		row.ProfitFactor = stats.ProfitFactor
		row.TradeCount = stats.TradeCount
		rows = append(rows, row)

		if p.label == report.NeighbourhoodBaseLabel {
			baseRun, baseStats, haveBase = result, stats, true
		}
	}

	if !haveBase {
		must(io.WriteString(os.Stderr,
			"\nthe chosen configuration itself did not run, so there is nothing to\n"+
				"compare its neighbours against.\n\n"))
		return exitFailure
	}

	if err := report.WriteSummary(os.Stdout, baseRun, baseStats); err != nil {
		log.Error("could not write the summary", "error", err)
		return exitFailure
	}
	if err := report.WriteNeighbourhood(os.Stdout, varied, rows); err != nil {
		log.Error("could not write the neighbourhood", "error", err)
		return exitFailure
	}

	analysis := report.Analyse(baseRun, baseStats)
	if err := recordExperiment(log, opts, baseRun, baseStats, analysis, nil, nil,
		withNeighbourhood(varied, rows)); err != nil {
		log.Error("could not record the experiment", "error", err)
		return exitFailure
	}
	return exitOK
}

// entryOption adjusts a log entry before it is appended.
type entryOption func(*report.ExperimentEntry)

// withNeighbourhood attaches the stability table to the entry.
func withNeighbourhood(columns []string, rows []report.NeighbourResult) entryOption {
	return func(entry *report.ExperimentEntry) {
		entry.NeighbourhoodColumns = columns
		entry.Neighbourhood = rows
	}
}

// steppedOverrides copies the chosen parameters with one moved a single step.
func steppedOverrides(
	entry _strategy_us.Registered,
	chosen map[string]string,
	name string,
	direction int,
) (map[string]string, error) {
	forStrategy, forExits, err := splitOverrides(chosen)
	if err != nil {
		return nil, err
	}

	// Whichever configuration owns the parameter is the one that gets stepped.
	config := entry.Defaults()
	if err := helper.ApplyParams(config, forStrategy); err != nil {
		return nil, err
	}
	exits := &backtest.Exits{}
	if err := helper.ApplyParams(exits, forExits); err != nil {
		return nil, err
	}

	owner := any(config)
	if _, isExit := forExits[name]; isExit {
		owner = exits
	}
	if err := helper.StepParam(owner, name, direction); err != nil {
		return nil, err
	}

	// Read back from the stepped configuration rather than from what differs
	// against the defaults.
	//
	// A stepped value can land exactly on its own default — resume_bars=1
	// stepped up is 2, which is the default — and it then differs from
	// nothing. Deriving the row from the changes would drop it and fall back
	// to the chosen value, producing a row labelled +1 that is a copy of the
	// base and quietly reports the neighbour as behaving identically.
	current, err := helper.ParamValues(config)
	if err != nil {
		return nil, err
	}
	exitValues, err := helper.ParamValues(exits)
	if err != nil {
		return nil, err
	}
	for key, value := range exitValues {
		current[key] = value
	}

	// Only the parameters the run actually varied. The rest stay unmentioned
	// so they keep their defaults, and the row differs from the base in
	// exactly one place.
	stepped := make(map[string]string, len(chosen))
	for key := range chosen {
		value, ok := current[key]
		if !ok {
			return nil, fmt.Errorf("unknown parameter %q", key)
		}
		stepped[key] = value
	}
	return stepped, nil
}

// runOnce builds and runs one configuration, leaving nothing shared with the
// next.
func runOnce(
	ctx context.Context,
	engine backtest.BacktestUsecase,
	candles candle.CandleUsecase,
	base backtest.RunParams,
	opts options,
	cfg *config.Config,
) (backtest.Result, report.Statistics, error) {
	built, err := buildParams(opts, cfg)
	if err != nil {
		return backtest.Result{}, report.Statistics{}, err
	}

	// Everything from the caller's params that is not derived from the
	// parameters themselves, so the rows differ only by what was stepped.
	built.From, built.To = base.From, base.To

	prepared, err := prepareRun(built, opts, cfg, candles, opts.trendFilter != "")
	if err != nil {
		return backtest.Result{}, report.Statistics{}, err
	}

	result, err := engine.Run(ctx, prepared)
	if err != nil {
		return backtest.Result{}, report.Statistics{}, err
	}
	return result, report.Compute(result), nil
}

// shortReason trims an error to something a table cell can hold.
func shortReason(err error) string {
	reason := err.Error()
	if line, _, found := strings.Cut(reason, "\n"); found {
		reason = line
	}
	const most = 48
	if len(reason) > most {
		reason = reason[:most-1] + "…"
	}
	return reason
}

// scaleCosts raises every assumed cost by the same multiplier.
//
// # Why all of them, and not just the headline rate
//
// Scaling only the taker rate would make a maker-configured run look
// progressively cheaper relative to its own assumption, which is the opposite
// of what a stress test is for. The same argument covers the spread: on a
// floating-spread venue widening is the realistic failure mode rather than a
// pessimistic one — 25 USD is a typical figure, not a guaranteed one, and it
// widens exactly when a strategy most wants to trade. A 2x sweep there is a
// normal Tuesday during a news release.
//
// Which of these actually bite depends on the cost model in force, and that is
// deliberate: the sweep does not need to know which one is running.
func scaleCosts(base backtest.Costs, multiplier float64) backtest.Costs {
	factor := decimal.NewFromFloat(multiplier)

	scaled := base
	scaled.FeeTakerPct = base.FeeTakerPct.Mul(factor)
	scaled.FeeMakerPct = base.MakerFeePct().Mul(factor)
	scaled.CommissionPerLot = base.CommissionPerLot.Mul(factor)

	// Points and ticks are integers on the venue, so they round rather than
	// carrying a fraction no quote could have.
	scaled.SpreadPoints = int(math.Round(float64(base.SpreadPoints) * multiplier))
	scaled.SlippageTicks = int(math.Round(float64(base.SlippageTicks) * multiplier))

	return scaled
}

// quoteUnit names the currency the money figures are in.
func quoteUnit(symbol string) string {
	for _, quote := range []string{"USDT", "USDC", "BUSD", "USD"} {
		if strings.HasSuffix(symbol, quote) {
			return quote
		}
	}
	return "quote"
}

// freshStrategy builds a new instance of the selected strategy.
//
// Every run needs its own. A strategy accumulates state across bars, so
// handing the same instance to a second run starts it mid-stream — and the
// two features that exist to compare runs, --compare and --cost-sweep, are
// exactly the ones that would be quietly wrong.
func freshStrategy(opts options, cfg *config.Config) (strategy.Strategy, error) {
	strategyOverrides, _, err := splitOverrides(opts.params)
	if err != nil {
		return nil, err
	}

	strat, _, err := lookupStrategy(
		opts.strategyName,
		strategyOverrides,
		roundTripCostPct(cfg),
		cfg.Market.Type == constants.MarketTypeSpot,
	)
	return strat, err
}

// splitOverrides routes each --param to whichever configuration owns it.
//
// # Why one flag rather than two
//
// A trailing stop is a parameter of the run in exactly the way an EMA period
// is, and asking somebody to remember which flag a given name lives behind is
// a way of producing runs configured differently from how they were meant to
// be. It also lets --neighbourhood step them on the same terms: if
// trailing_atr_mult=2.0 works and 1.75 and 2.25 collapse, that is an artefact
// on exactly the same terms as a fitted EMA period.
//
// Ownership is decided by the engine's own descriptors, not by a list kept
// here, so a field added to Exits is routed correctly without this function
// being touched.
func splitOverrides(all map[string]string) (forStrategy, forExits map[string]string, err error) {
	exitNames, err := helper.ParamValues(&backtest.Exits{})
	if err != nil {
		return nil, nil, err
	}

	forStrategy = map[string]string{}
	forExits = map[string]string{}

	for key, value := range all {
		if _, isExit := exitNames[key]; isExit {
			forExits[key] = value
			continue
		}
		// Everything else goes to the strategy, which rejects what it does not
		// recognise and names its own valid keys.
		forStrategy[key] = value
	}
	return forStrategy, forExits, nil
}

// buildExits applies the exit overrides to a fresh configuration.
func buildExits(overrides map[string]string) (backtest.Exits, []helper.ParamChange, error) {
	exits := backtest.Exits{}
	if err := helper.ApplyParams(&exits, overrides); err != nil {
		return backtest.Exits{}, nil, err
	}
	if err := exits.Validate(); err != nil {
		return backtest.Exits{}, nil, err
	}

	changed, err := helper.ChangedParams(&backtest.Exits{}, &exits)
	if err != nil {
		return backtest.Exits{}, nil, err
	}
	return exits, changed, nil
}

// strategyIsMultiTimeframe reports whether the selected strategy reads higher
// timeframes of its own.
//
// It builds a throwaway instance rather than inspecting a name, so the answer
// comes from the type rather than from a list that a new strategy could be
// left off. A build failure here is not reported: whatever is wrong with the
// strategy will be reported properly by the run itself a moment later, and
// this question is only "is --compare meaningful".
func strategyIsMultiTimeframe(opts options, cfg *config.Config) (strategy.MultiTimeframe, bool) {
	strat, err := freshStrategy(opts, cfg)
	if err != nil {
		return nil, false
	}
	multi, ok := strat.(strategy.MultiTimeframe)
	return multi, ok
}

// prepareRun returns params carrying state that belongs to this run alone.
//
// # Why every run goes through here
//
// RunParams is a struct, so `next := params` copies the fields and shares
// everything they point at. The strategy, the trend filter and the aligner are
// all stateful, and the aligner's cursors only move forward — so a second run
// built by copying inherited a filter parked at the end of the range and died
// on its first bar with "Advance called with <start> after <end>".
//
// The earlier fix rebuilt the strategy and left the filter shared, which is
// the shape of the mistake: remembering to reset one stateful component is not
// a policy, it is a thing somebody remembered once. This clears every one of
// them first, so a component that is not rebuilt below is nil and fails loudly
// rather than carrying state into a run that must not have it.
//
// Reconstruction rather than Reset(): phase 03 proves an indicator reset
// equals reconstruction, and nothing proves it for the filter. Construction is
// the version that cannot be subtly wrong.
func prepareRun(
	base backtest.RunParams,
	opts options,
	cfg *config.Config,
	candles candle.CandleUsecase,
	withFilter bool,
) (backtest.RunParams, error) {
	params := base
	params.Strategy = nil
	params.TrendFilter = nil
	params.TrendAligner = nil
	params.StrategyAligner = nil
	params.TrendConfig = trend.Config{}
	params.FilterParams = nil

	strat, err := freshStrategy(opts, cfg)
	if err != nil {
		return backtest.RunParams{}, err
	}
	params.Strategy = strat

	// A multi-timeframe strategy gets its own aligner, built from the
	// timeframes it names rather than from the filter's table. It is rebuilt
	// here with everything else stateful: an aligner may not rewind, so a
	// second pass over the same range needs a second aligner.
	if err := attachStrategyAligner(&params, candles); err != nil {
		return backtest.RunParams{}, err
	}

	if withFilter {
		err := attachTrendFilter(&params, candles, opts.filterParams)
		switch {
		case errors.Is(err, trend.ErrNoUsableContributor):
			// Run, unfiltered, and say why. Refusing here would leave the cell
			// unmeasured, and an unmeasured cell in an otherwise monotonic
			// series is where guessing is least defensible.
			params.TrendUnavailable = fmt.Sprintf(
				"no contributor above %s can warm up over this range", params.Timeframe)
		case err != nil:
			return backtest.RunParams{}, err
		}
	}
	return params, nil
}

// recordExperiment appends this run to the experiment log.
//
// # Why this is not optional
//
// The log's value is the denominator. A strategy chosen out of fifty has fifty
// chances to look good by accident, and nothing in a run's own report can tell
// you that happened — only the count of entries above it can. Left to a human,
// the entries that go missing are the ones abandoned halfway and the ones whose
// result was disappointing, which are exactly the ones the denominator needs.
//
// It runs only after a run has produced a report. A run that errored has no
// result to record and writing a half-entry would corrupt the count with
// something that was never a candidate.
func recordExperiment(
	log *slog.Logger,
	opts options,
	result backtest.Result,
	stats report.Statistics,
	analysis report.Analysis,
	sweep []report.CostSensitivity,
	comparison *report.ComparisonLine,
	options ...entryOption,
) error {
	if opts.experimentLog == "" {
		return nil
	}
	path := repoRelative(opts.experimentLog)

	// A missing or unparseable criteria file must not produce a guess. A wrong
	// pass in a permanent log is worse than no verdict at all, so the failure
	// is recorded in the entry rather than resolved.
	criteria, criteriaErr := report.LoadCriteria(repoRelative(report.CriteriaPath))
	if criteriaErr != nil {
		log.Warn("acceptance criteria could not be read; the entry will say so",
			"error", criteriaErr)
	}

	entry := report.ExperimentEntryFor(result, stats, analysis,
		criteria, criteriaErr, opts.dataset, time.Now().UTC(), sweep)
	entry.Comparison = comparison
	entry.Suppressed = opts.noExperimentLog
	for _, option := range options {
		option(&entry)
	}

	number, err := report.AppendExperiment(path, entry)
	if err != nil {
		return err
	}
	entry.Number = number

	if entry.Suppressed {
		must(fmt.Fprintf(os.Stdout,
			"\nrun %d recorded in %s as suppressed (--no-experiment-log)\n",
			entry.Number, path))
	} else {
		must(fmt.Fprintf(os.Stdout, "\nrun %d appended to %s — fill in the Note line\n",
			entry.Number, path))
	}
	return nil
}

// repoRelative resolves a docs path from wherever the CLI was invoked.
//
// Same reasoning as holdoutLogPath: the binary is usually run from server/, and
// a plain relative path would scatter logs into whichever directory somebody
// happened to be standing in. One log in one place is the entire point.
func repoRelative(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	for _, prefix := range []string{"", "../", "../../"} {
		if _, err := os.Stat(prefix + "docs"); err == nil {
			return prefix + path
		}
	}
	return path
}
