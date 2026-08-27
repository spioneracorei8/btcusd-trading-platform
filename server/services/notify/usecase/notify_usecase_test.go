package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
	_notify_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
)

// recordingQueue is a queue that remembers what reached it, and nothing else.
type recordingQueue struct {
	mu       sync.Mutex
	queued   []models.Notification
	err      error
	fetchErr error
	markErr  error
	conflict bool
}

func (q *recordingQueue) InsertNotification(
	_ context.Context, n models.Notification,
) (models.Notification, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.err != nil {
		return models.Notification{}, false, q.err
	}
	if q.conflict {
		return models.Notification{}, false, nil
	}

	n.Id = int64(len(q.queued) + 1)
	n.Status = constants.NotificationStatusPending
	q.queued = append(q.queued, n)
	return n, true, nil
}

func (q *recordingQueue) FetchDueNotifications(
	_ context.Context, asOf time.Time, limit int32,
) ([]models.Notification, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.fetchErr != nil {
		return nil, q.fetchErr
	}

	// The same rule the SQL applies: pending, due, oldest first.
	var due []models.Notification
	for _, n := range q.queued {
		if n.Status != constants.NotificationStatusPending || n.NextAttemptAt.After(asOf) {
			continue
		}
		due = append(due, n)
		if int32(len(due)) == limit {
			break
		}
	}
	return due, nil
}

func (q *recordingQueue) MarkNotificationSent(
	_ context.Context, id int64, sentAt time.Time,
) (models.Notification, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.markErr != nil {
		return models.Notification{}, q.markErr
	}
	return q.update(id, func(n *models.Notification) {
		n.Status = constants.NotificationStatusSent
		n.Attempts++
		n.LastError = ""
		n.SentAt = &sentAt
	})
}

func (q *recordingQueue) RescheduleNotification(
	_ context.Context, id int64, lastError string, at time.Time,
) (models.Notification, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.update(id, func(n *models.Notification) {
		n.Attempts++
		n.LastError = lastError
		n.NextAttemptAt = at
	})
}

func (q *recordingQueue) FailNotification(
	_ context.Context, id int64, lastError string,
) (models.Notification, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.update(id, func(n *models.Notification) {
		n.Status = constants.NotificationStatusFailed
		n.Attempts++
		n.LastError = lastError
	})
}

// update applies a change to one row, refusing an id nothing holds — the same
// answer the repository gives when an UPDATE matches no row.
func (q *recordingQueue) update(
	id int64, change func(*models.Notification),
) (models.Notification, error) {
	for i := range q.queued {
		if q.queued[i].Id == id {
			change(&q.queued[i])
			return q.queued[i], nil
		}
	}
	return models.Notification{}, fmt.Errorf("no notification %d", id)
}

func (q *recordingQueue) row(id int64) models.Notification {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, n := range q.queued {
		if n.Id == id {
			return n
		}
	}
	return models.Notification{}
}

func (q *recordingQueue) rows() []models.Notification {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]models.Notification(nil), q.queued...)
}

func aSignal() models.Signal {
	return models.Signal{
		Id:              uuid.New(),
		Symbol:          "BTCUSDT",
		MarketType:      constants.MarketTypeSpot,
		Timeframe:       constants.Timeframe4h,
		Direction:       constants.DirectionLong,
		StrategyName:    "ema_crossover",
		StrategyVersion: "v1",
	}
}

// silentLog keeps test output to what the test itself says.
func silentLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// registeredDevices is the devices table, in a field. It stands in for
// notify.DeviceRepository; the real usecase is layered on top of it by
// deviceUsecaseOver, so the delivery tests run against the production rules
// rather than a fake of them.
//
// It answers ErrNotFound rather than an empty device when nothing is
// registered, because that distinction is what decides whether a queued alert
// waits or burns its retry budget.
type registeredDevices struct {
	mu     sync.Mutex
	device *models.Device
	err    error

	// lookups counts FetchDevice calls, so a test can assert the delivery
	// pass asks once rather than once per row.
	lookups int

	// forgetAfter drops the registration once this many lookups have been
	// served, which is the only way to reach the branch where a phone
	// deregisters while a pass is already running. Zero disables it.
	forgetAfter int
}

// mustDevices wraps a store in the real device usecase, for the call sites
// that have no *testing.T to hand. The constructor only fails on a nil
// argument, which is a programming error in the test rather than a case worth
// reporting.
func mustDevices(store *registeredDevices) notify.DeviceUsecase {
	usecase, err := _notify_us.NewDeviceUsecaseImpl(store, silentLog())
	if err != nil {
		panic(err)
	}
	return usecase
}

// deviceUsecaseOver wraps a store in the real device usecase.
func deviceUsecaseOver(t *testing.T, store *registeredDevices) notify.DeviceUsecase {
	t.Helper()

	usecase, err := _notify_us.NewDeviceUsecaseImpl(store, silentLog())
	if err != nil {
		t.Fatalf("NewDeviceUsecaseImpl() returned error: %v", err)
	}
	return usecase
}

func withDevice(token string) *registeredDevices {
	return &registeredDevices{device: &models.Device{
		Token: token, Platform: constants.DevicePlatformAndroid,
	}}
}

func (d *registeredDevices) RegisterDevice(
	_ context.Context, in models.Device,
) (models.Device, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.device = &in
	return in, nil
}

func (d *registeredDevices) FetchDevice(context.Context) (models.Device, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.lookups++
	if d.err != nil {
		return models.Device{}, d.err
	}
	if d.device == nil {
		return models.Device{}, constants.ErrNotFound
	}

	found := *d.device
	if d.forgetAfter > 0 && d.lookups >= d.forgetAfter {
		d.device = nil
	}
	return found, nil
}

func (d *registeredDevices) DeleteDevice(context.Context) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	had := d.device != nil
	d.device = nil
	return had, nil
}

// forget removes the registration mid-test, which is the only way to reach
// the "the phone deregistered while a pass was running" branch.
func (d *registeredDevices) forget() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.device = nil
}

// stubSender records what it was asked to send and answers however the test
// needs. Nothing here reaches a network.
type stubSender struct {
	mu   sync.Mutex
	sent []notify.Message
	errs []error
}

func (s *stubSender) Channel() constants.NotificationChannel {
	return constants.NotificationChannelFCM
}

func (s *stubSender) Send(_ context.Context, m notify.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sent = append(s.sent, m)
	if len(s.errs) == 0 {
		return nil
	}
	// One answer per attempt, and the last one repeats, so a test can say
	// "fails forever" without writing out five copies.
	err := s.errs[0]
	if len(s.errs) > 1 {
		s.errs = s.errs[1:]
	}
	return err
}

func (s *stubSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

// storedSignals answers the delivery worker's lookups.
type storedSignals struct {
	byId map[uuid.UUID]models.Signal
	err  error
}

func (s *storedSignals) CreateSignal(
	context.Context, models.Signal, models.Candle,
) (models.Signal, error) {
	return models.Signal{}, errors.New("not used")
}

func (s *storedSignals) SetEntryPrice(
	context.Context, uuid.UUID, decimal.Decimal,
) (models.Signal, error) {
	return models.Signal{}, errors.New("not used")
}

func (s *storedSignals) FetchSignalById(
	_ context.Context, id uuid.UUID,
) (models.Signal, error) {
	if s.err != nil {
		return models.Signal{}, s.err
	}
	found, ok := s.byId[id]
	if !ok {
		return models.Signal{}, constants.ErrNotFound
	}
	return found, nil
}

// buildUsecase wires a queueing-only usecase, with no way to deliver.
func buildUsecase(queue notify.NotifyRepository, mode constants.SignalMode) (notify.NotifyUsecase, error) {
	cfg := _notify_us.Config{Mode: mode}
	if mode.Delivers() {
		cfg.Sender = &stubSender{}
		cfg.Signals = &storedSignals{}
		cfg.Devices = mustDevices(withDevice("device-token"))
	}
	return _notify_us.NewNotifyUsecaseImpl(queue, silentLog(), cfg)
}

// deliveryFixture is one queued signal, a fake sender, and a frozen clock.
type deliveryFixture struct {
	usecase notify.NotifyUsecase
	queue   *recordingQueue
	sender  *stubSender
	devices *registeredDevices
	signal  models.Signal
	now     time.Time
	id      int64
}

// newDelivery queues one signal and returns everything needed to watch what
// happens to it.
func newDelivery(t *testing.T, senderErrs ...error) *deliveryFixture {
	t.Helper()

	stored := aSignal()
	stored.Reason = []byte(`{"trigger":"fast crossed above slow"}`)
	stored.SignalPrice = decimal.NullDecimal{Decimal: decimal.RequireFromString("64123.45678900"), Valid: true}
	stored.StopLoss = decimal.NullDecimal{Decimal: decimal.RequireFromString("63900"), Valid: true}
	stored.TakeProfit = decimal.NullDecimal{Decimal: decimal.RequireFromString("64600"), Valid: true}

	fixture := &deliveryFixture{
		queue:   &recordingQueue{},
		sender:  &stubSender{errs: senderErrs},
		devices: withDevice("device-token"),
		signal:  stored,
		now:     time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
	}

	usecase, err := _notify_us.NewNotifyUsecaseImpl(fixture.queue, silentLog(), _notify_us.Config{
		Mode:    constants.SignalModeNotify,
		Sender:  fixture.sender,
		Signals: &storedSignals{byId: map[uuid.UUID]models.Signal{stored.Id: stored}},
		Devices: mustDevices(fixture.devices),
		Now:     func() time.Time { return fixture.now },
	})
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}
	fixture.usecase = usecase

	queued, ok, err := usecase.QueueSignal(context.Background(), stored)
	if err != nil || !ok {
		t.Fatalf("QueueSignal() = %v, %v", ok, err)
	}
	fixture.id = queued.Id
	return fixture
}

// deliver runs one pass and returns what it did.
// newBacklog is newDelivery with n queued signals rather than one, for the
// cases that are only visible across several rows in a single pass.
func newBacklog(t *testing.T, n int) *deliveryFixture {
	t.Helper()

	f := newDelivery(t)
	stored := map[uuid.UUID]models.Signal{f.signal.Id: f.signal}
	for i := 1; i < n; i++ {
		extra := aSignal()
		stored[extra.Id] = extra
	}

	usecase, err := _notify_us.NewNotifyUsecaseImpl(f.queue, silentLog(), _notify_us.Config{
		Mode: constants.SignalModeNotify, Sender: f.sender,
		Signals: &storedSignals{byId: stored}, Devices: mustDevices(f.devices),
		Now: func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}
	f.usecase = usecase

	for id := range stored {
		if id == f.signal.Id {
			continue // already queued by newDelivery
		}
		if _, ok, err := usecase.QueueSignal(context.Background(), stored[id]); err != nil || !ok {
			t.Fatalf("QueueSignal() = %v, %v", ok, err)
		}
	}
	if got := len(f.queue.rows()); got != n {
		t.Fatalf("queued %d rows, want %d", got, n)
	}
	return f
}

func (f *deliveryFixture) deliver(t *testing.T) notify.DeliveryReport {
	t.Helper()

	report, err := f.usecase.DeliverDue(context.Background())
	if err != nil {
		t.Fatalf("DeliverDue() returned error: %v", err)
	}
	return report
}

// TestSilentQueuesNothing.
//
// The default mode, and the one that has to be right: a deploy that has not
// been told to alert must not alert. Silence is structural here — the usecase
// never reaches its repository — rather than a check somewhere further down
// that a later change could forget.
func TestSilentQueuesNothing(t *testing.T) {
	queue := &recordingQueue{}
	usecase, err := buildUsecase(queue, constants.SignalModeSilent)
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}

	if usecase.Delivers() {
		t.Error("silent mode reports that it delivers")
	}

	_, ok, err := usecase.QueueSignal(context.Background(), aSignal())
	if err != nil {
		t.Fatalf("QueueSignal() returned error: %v", err)
	}
	if ok {
		t.Error("silent mode reported a signal as queued")
	}
	if got := queue.rows(); len(got) != 0 {
		t.Errorf("silent mode wrote %d rows to the queue", len(got))
	}
}

// TestNotifyQueuesTheSignalRatherThanSendingIt.
//
// The queue row is the deliverable of this path. Sending from here would put
// a third party's availability in the collector's candle loop, and a Firebase
// outage would then cost signals rather than alerts.
func TestNotifyQueuesTheSignalRatherThanSendingIt(t *testing.T) {
	queue := &recordingQueue{}
	usecase, err := buildUsecase(queue, constants.SignalModeNotify)
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}

	if !usecase.Delivers() {
		t.Error("notify mode reports that it does not deliver")
	}

	signal := aSignal()
	queued, ok, err := usecase.QueueSignal(context.Background(), signal)
	if err != nil || !ok {
		t.Fatalf("QueueSignal() = %v, %v", ok, err)
	}

	rows := queue.rows()
	if len(rows) != 1 {
		t.Fatalf("queued %d rows for one signal", len(rows))
	}
	if rows[0].SignalId != signal.Id {
		t.Errorf("the queued row points at %s, want %s", rows[0].SignalId, signal.Id)
	}
	if rows[0].Channel != constants.NotificationChannelFCM {
		t.Errorf("Channel = %q, want fcm", rows[0].Channel)
	}
	if queued.Status != constants.NotificationStatusPending {
		t.Errorf("Status = %q, want pending", queued.Status)
	}
}

// TestAlreadyQueuedIsNotAnError.
//
// Offering the same signal twice happens on a restart that re-walks a bar.
// The right answer is one alert and no noise, so the repository's idempotent
// insert has to read as success and not as a fault.
func TestAlreadyQueuedIsNotAnError(t *testing.T) {
	queue := &recordingQueue{conflict: true}
	usecase, err := buildUsecase(queue, constants.SignalModeNotify)
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}

	_, ok, err := usecase.QueueSignal(context.Background(), aSignal())
	if err != nil {
		t.Errorf("an already-queued signal was reported as an error: %v", err)
	}
	if ok {
		t.Error("an already-queued signal was reported as newly queued")
	}
}

// TestAQueueFailureIsReported, because it is not the same as a duplicate and
// the caller decides what to do about it.
func TestAQueueFailureIsReported(t *testing.T) {
	queue := &recordingQueue{err: errors.New("the database is unreachable")}
	usecase, err := buildUsecase(queue, constants.SignalModeNotify)
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}

	_, ok, err := usecase.QueueSignal(context.Background(), aSignal())
	if err == nil {
		t.Fatal("a queue failure was swallowed")
	}
	if ok {
		t.Error("a failed write was reported as queued")
	}
}

// TestASignalWithNoIdIsRefused.
//
// A row pointing at nothing would fail the foreign key later, inside a
// delivery worker, where the cause is much harder to see than here.
func TestASignalWithNoIdIsRefused(t *testing.T) {
	queue := &recordingQueue{}
	usecase, err := buildUsecase(queue, constants.SignalModeNotify)
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}

	unsaved := aSignal()
	unsaved.Id = uuid.Nil

	if _, _, err := usecase.QueueSignal(context.Background(), unsaved); err == nil {
		t.Error("a signal that was never persisted was queued")
	}
	if got := queue.rows(); len(got) != 0 {
		t.Errorf("wrote %d rows for a signal with no id", len(got))
	}
}

// TestAnUnknownModeIsRefusedAtConstruction, rather than being treated as one
// of the two and picked at random by a switch's default arm.
func TestAnUnknownModeIsRefusedAtConstruction(t *testing.T) {
	for _, mode := range []constants.SignalMode{"", "uat", "test", "enabled"} {
		if _, err := buildUsecase(&recordingQueue{}, mode); err == nil {
			t.Errorf("mode %q was accepted", mode)
		}
	}
}

// ---------------------------------------------------------------------------
// Delivery
// ---------------------------------------------------------------------------

// TestADueNotificationIsDeliveredAndMarkedSent.
func TestADueNotificationIsDeliveredAndMarkedSent(t *testing.T) {
	f := newDelivery(t)

	report := f.deliver(t)
	if report.Sent != 1 || report.Attempted != 1 {
		t.Fatalf("report = %+v, want one attempted and one sent", report)
	}
	if f.sender.count() != 1 {
		t.Errorf("the sender was called %d times for one notification", f.sender.count())
	}

	row := f.queue.row(f.id)
	if row.Status != constants.NotificationStatusSent {
		t.Errorf("Status = %q, want sent", row.Status)
	}
	if row.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1; the attempt that worked counts", row.Attempts)
	}
	if row.SentAt == nil || !row.SentAt.Equal(f.now) {
		t.Errorf("SentAt = %v, want %v", row.SentAt, f.now)
	}

	// The pass that follows has nothing left to do.
	if next := f.deliver(t); !next.Quiet() {
		t.Errorf("a delivered notification was attempted again: %+v", next)
	}
}

// TestTheAlertQuotesTheReferencePriceAndNotAnEntry.
//
// A signal is decided on a bar's close and could not have filled there, so
// there is no entry price when the alert goes out — and the alert goes out
// now, because the owner needs the news immediately. Quoting the close as an
// entry would tell them they are in at a price nothing traded at.
func TestTheAlertQuotesTheReferencePriceAndNotAnEntry(t *testing.T) {
	f := newDelivery(t)
	f.deliver(t)

	if f.sender.count() != 1 {
		t.Fatalf("the sender was called %d times", f.sender.count())
	}
	message := f.sender.sent[0]

	if message.Token != "device-token" {
		t.Errorf("Token = %q, want the configured device", message.Token)
	}
	for _, want := range []string{"BTCUSDT", "4h", "LONG"} {
		if !strings.Contains(message.Title, want) {
			t.Errorf("the title %q does not say %s", message.Title, want)
		}
	}

	// Everything the spec asks for on a lock screen, with the price rounded
	// for a person to read at a glance.
	for _, want := range []string{"ref", "64123.46", "stop", "63900", "target", "64600",
		"fast crossed above slow"} {
		if !strings.Contains(message.Body, want) {
			t.Errorf("the body %q does not carry %q", message.Body, want)
		}
	}
	if strings.Contains(strings.ToLower(message.Body), "entry") {
		t.Errorf("the body %q calls the reference price an entry", message.Body)
	}

	// Exact in the payload, because a number shown to a person is not one
	// anything should compute with.
	if got := message.Data["signal_price"]; got != "64123.456789" {
		t.Errorf("data signal_price = %q, want the exact close", got)
	}
	if _, present := message.Data["entry_price"]; present {
		t.Error("the payload carries an entry price, which is not known yet")
	}
	if got := message.Data["signal_id"]; got != f.signal.Id.String() {
		t.Errorf("data signal_id = %q, want %s", got, f.signal.Id)
	}
}

// TestATransientFailureBacksOffAndIsRetried.
//
// The backoff has to be durable and it has to grow. A retry that came back
// immediately would hammer a service that is already struggling, and one that
// forgot the wait on restart would do the same after a crash loop.
func TestATransientFailureBacksOffAndIsRetried(t *testing.T) {
	outage := errors.New("502 Bad Gateway")
	f := newDelivery(t, outage, outage, nil)

	first := f.deliver(t)
	if first.Retrying != 1 {
		t.Fatalf("report = %+v, want one retrying", first)
	}

	row := f.queue.row(f.id)
	if row.Status != constants.NotificationStatusPending {
		t.Errorf("Status = %q, want pending; a transient failure is not the end", row.Status)
	}
	if !strings.Contains(row.LastError, "502") {
		t.Errorf("LastError = %q, does not say what went wrong", row.LastError)
	}

	wantAt := f.now.Add(constants.NotifyRetryBase)
	if !row.NextAttemptAt.Equal(wantAt) {
		t.Errorf("NextAttemptAt = %s, want %s", row.NextAttemptAt, wantAt)
	}

	// Still inside the backoff: nothing is due, and the sender is not called.
	f.now = wantAt.Add(-time.Second)
	if report := f.deliver(t); !report.Quiet() {
		t.Errorf("a notification was attempted inside its backoff: %+v", report)
	}
	if f.sender.count() != 1 {
		t.Errorf("the sender was called %d times, want 1", f.sender.count())
	}

	// Past it: attempted again, and the wait doubles.
	f.now = wantAt
	if report := f.deliver(t); report.Retrying != 1 {
		t.Fatalf("report = %+v, want one retrying", report)
	}
	if got, want := f.queue.row(f.id).NextAttemptAt, f.now.Add(2*constants.NotifyRetryBase); !got.Equal(want) {
		t.Errorf("the second backoff is %s, want %s — it has to grow", got, want)
	}

	// Third time it works.
	f.now = f.queue.row(f.id).NextAttemptAt
	if report := f.deliver(t); report.Sent != 1 {
		t.Fatalf("report = %+v, want one sent", report)
	}
	if got := f.queue.row(f.id).Attempts; got != 3 {
		t.Errorf("Attempts = %d, want 3", got)
	}
}

// TestItGivesUpAfterFiveAttempts, and says so where somebody will find it.
func TestItGivesUpAfterFiveAttempts(t *testing.T) {
	f := newDelivery(t, errors.New("502 Bad Gateway"))

	for attempt := 1; attempt <= constants.NotificationMaxAttempts; attempt++ {
		report := f.deliver(t)
		if report.Attempted != 1 {
			t.Fatalf("attempt %d: report = %+v, want one attempted", attempt, report)
		}

		row := f.queue.row(f.id)
		if attempt < constants.NotificationMaxAttempts {
			if row.Status != constants.NotificationStatusPending {
				t.Fatalf("attempt %d of %d: Status = %q, want pending",
					attempt, constants.NotificationMaxAttempts, row.Status)
			}
			f.now = row.NextAttemptAt
			continue
		}

		if row.Status != constants.NotificationStatusFailed {
			t.Errorf("Status after %d attempts = %q, want failed", attempt, row.Status)
		}
		if !strings.Contains(row.LastError, "502") {
			t.Errorf("LastError = %q, does not carry the last error", row.LastError)
		}
	}

	if f.sender.count() != constants.NotificationMaxAttempts {
		t.Errorf("the sender was called %d times, want %d",
			f.sender.count(), constants.NotificationMaxAttempts)
	}

	// Failed is final: nothing retries it.
	f.now = f.now.Add(24 * time.Hour)
	if report := f.deliver(t); !report.Quiet() {
		t.Errorf("a failed notification was attempted again: %+v", report)
	}
}

// TestAPermanentRejectionGivesUpAtOnce.
//
// Spending five attempts and eight minutes of backoff on a device token that
// no longer exists delays every alert behind it, and then records "gave up
// after 5 attempts" — which reads like a network problem and sends whoever
// investigates to the wrong place.
func TestAPermanentRejectionGivesUpAtOnce(t *testing.T) {
	gone := fmt.Errorf("404 UNREGISTERED: %w", notify.ErrUndeliverable)
	f := newDelivery(t, gone)

	report := f.deliver(t)
	if report.GaveUp != 1 {
		t.Fatalf("report = %+v, want one given up on", report)
	}

	row := f.queue.row(f.id)
	if row.Status != constants.NotificationStatusFailed {
		t.Errorf("Status = %q, want failed", row.Status)
	}
	if row.Attempts != 1 {
		t.Errorf("Attempts = %d; a permanent rejection must not spend the budget", row.Attempts)
	}
	if !strings.Contains(row.LastError, "UNREGISTERED") {
		t.Errorf("LastError = %q, does not say what the destination said", row.LastError)
	}
	if f.sender.count() != 1 {
		t.Errorf("the sender was called %d times for a permanent rejection", f.sender.count())
	}
}

// TestOneBadNotificationDoesNotStopTheQueue.
//
// A queue that stopped at its first undeliverable row would let one dead
// device token hold back every alert behind it.
func TestOneBadNotificationDoesNotStopTheQueue(t *testing.T) {
	f := newDelivery(t)

	// A second signal behind the first, whose lookup will succeed.
	second := aSignal()
	second.Reason = []byte(`{"trigger":"second"}`)
	signals := &storedSignals{byId: map[uuid.UUID]models.Signal{
		f.signal.Id: f.signal, second.Id: second,
	}}

	sender := &stubSender{errs: []error{
		fmt.Errorf("404 UNREGISTERED: %w", notify.ErrUndeliverable),
		nil,
	}}
	usecase, err := _notify_us.NewNotifyUsecaseImpl(f.queue, silentLog(), _notify_us.Config{
		Mode: constants.SignalModeNotify, Sender: sender, Signals: signals,
		Devices: mustDevices(withDevice("device-token")), Now: func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}
	if _, ok, err := usecase.QueueSignal(context.Background(), second); err != nil || !ok {
		t.Fatalf("QueueSignal() = %v, %v", ok, err)
	}

	report, err := usecase.DeliverDue(context.Background())
	if err != nil {
		t.Fatalf("DeliverDue() returned error: %v", err)
	}
	if report.Attempted != 2 {
		t.Fatalf("report = %+v, want both rows attempted", report)
	}
	if report.GaveUp != 1 || report.Sent != 1 {
		t.Errorf("report = %+v, want one given up on and one sent", report)
	}
}

// TestASignalThatNoLongerExistsIsNotRetriedForever.
//
// The foreign key cascades, so a queue row whose signal cannot be read means
// the signal was deleted. There is nothing left to tell the owner about and
// retrying would re-read the same absence five times.
func TestASignalThatNoLongerExistsIsNotRetriedForever(t *testing.T) {
	f := newDelivery(t)

	usecase, err := _notify_us.NewNotifyUsecaseImpl(f.queue, silentLog(), _notify_us.Config{
		Mode: constants.SignalModeNotify, Sender: f.sender,
		Signals: &storedSignals{byId: map[uuid.UUID]models.Signal{}},
		Devices: mustDevices(withDevice("device-token")), Now: func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}

	report, err := usecase.DeliverDue(context.Background())
	if err != nil {
		t.Fatalf("DeliverDue() returned error: %v", err)
	}
	if report.GaveUp != 1 {
		t.Errorf("report = %+v, want one given up on", report)
	}
	if f.sender.count() != 0 {
		t.Error("a message was sent for a signal that could not be read")
	}
	if f.queue.row(f.id).Status != constants.NotificationStatusFailed {
		t.Errorf("Status = %q, want failed", f.queue.row(f.id).Status)
	}
}

// TestSilentDeliversNothingEvenWithAQueueFullOfRows.
//
// The mode is the whole switch. A queue that had rows in it from an earlier
// notify run must not drain the moment somebody turns delivery off.
func TestSilentDeliversNothingEvenWithAQueueFullOfRows(t *testing.T) {
	f := newDelivery(t)

	silent, err := _notify_us.NewNotifyUsecaseImpl(f.queue, silentLog(), _notify_us.Config{
		Mode: constants.SignalModeSilent, Now: func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("NewNotifyUsecaseImpl() returned error: %v", err)
	}

	report, err := silent.DeliverDue(context.Background())
	if err != nil {
		t.Fatalf("DeliverDue() returned error: %v", err)
	}
	if !report.Quiet() {
		t.Errorf("silent mode delivered: %+v", report)
	}
	if f.sender.count() != 0 {
		t.Error("silent mode sent a message")
	}
	if f.queue.row(f.id).Status != constants.NotificationStatusPending {
		t.Error("silent mode changed a queued row")
	}
}

// TestNotifyModeWithNothingToSendThroughIsRefused, because a mode that claims
// to deliver and cannot looks like it is working.
func TestNotifyModeWithNothingToSendThroughIsRefused(t *testing.T) {
	full := _notify_us.Config{
		Mode: constants.SignalModeNotify, Sender: &stubSender{},
		Signals: &storedSignals{}, Devices: mustDevices(withDevice("device-token")),
	}

	for name, damage := range map[string]func(*_notify_us.Config){
		"no sender":  func(c *_notify_us.Config) { c.Sender = nil },
		"no signals": func(c *_notify_us.Config) { c.Signals = nil },
		"no devices": func(c *_notify_us.Config) { c.Devices = nil },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := full
			damage(&cfg)

			if _, err := _notify_us.NewNotifyUsecaseImpl(&recordingQueue{}, silentLog(), cfg); err == nil {
				t.Error("it was accepted")
			}
		})
	}
}

// TestAQueuedAlertWaitsWhenNoDeviceHasRegistered.
//
// # What this prevents
//
// The device token now comes from the phone, so every deployment is in the
// "notify mode, nothing registered" state between switching the mode on and
// opening the app for the first time. Signals produced in that window are
// recorded and queued, which is right.
//
// What must not happen is that they are spent. The retry budget is five
// attempts over about eight minutes; if a missing registration counted as a
// failed attempt, every alert produced before the app was installed would be
// marked failed long before anybody could install it — and the recorded
// reason would read like a network problem rather than "there was nowhere to
// send this". Nothing retries a failed row, so those alerts would be gone.
//
// The row must therefore come out of the pass exactly as it went in.
func TestAQueuedAlertWaitsWhenNoDeviceHasRegistered(t *testing.T) {
	f := newDelivery(t)
	f.devices.forget()

	before := f.queue.row(f.id)
	report := f.deliver(t)

	if report.Waiting != 1 {
		t.Errorf("Waiting = %d, want 1", report.Waiting)
	}
	if report.Attempted != 0 {
		t.Errorf("Attempted = %d, want 0: nothing was tried", report.Attempted)
	}
	if report.Sent+report.Retrying+report.GaveUp != 0 {
		t.Errorf("report = %+v, want no outcomes at all", report)
	}
	if f.sender.count() != 0 {
		t.Errorf("the sender was called %d times with nowhere to send", f.sender.count())
	}

	after := f.queue.row(f.id)
	if after.Attempts != before.Attempts {
		t.Errorf("attempts went from %d to %d; a missing device must not spend one",
			before.Attempts, after.Attempts)
	}
	if after.Status != constants.NotificationStatusPending {
		t.Errorf("status = %q, want pending", after.Status)
	}
	if after.LastError != "" {
		t.Errorf("last_error = %q, want empty: nothing failed", after.LastError)
	}
	if !after.NextAttemptAt.Equal(before.NextAttemptAt) {
		t.Errorf("next_attempt_at moved from %s to %s; the row must still be due",
			before.NextAttemptAt, after.NextAttemptAt)
	}
}

// TestAWaitingAlertDeliversOnceAPhoneRegisters.
//
// The other half: waiting is only the right answer if the alert still goes out
// afterwards. This is the sequence a first install actually produces — mode
// switched on, a signal recorded, then the app opened.
func TestAWaitingAlertDeliversOnceAPhoneRegisters(t *testing.T) {
	f := newDelivery(t)
	f.devices.forget()

	if report := f.deliver(t); report.Waiting != 1 {
		t.Fatalf("Waiting = %d, want 1", report.Waiting)
	}

	if _, err := f.devices.RegisterDevice(context.Background(), models.Device{
		Token: "registered-later", Platform: constants.DevicePlatformAndroid,
	}); err != nil {
		t.Fatalf("RegisterDevice() returned error: %v", err)
	}

	report := f.deliver(t)
	if report.Sent != 1 {
		t.Fatalf("Sent = %d, want 1 once a phone registered: %+v", report.Sent, report)
	}
	if f.queue.row(f.id).Status != constants.NotificationStatusSent {
		t.Errorf("the row is %q, want sent", f.queue.row(f.id).Status)
	}
	if got := f.sender.sent[0].Token; got != "registered-later" {
		t.Errorf("delivered to %q, want the token registered after the wait", got)
	}
}

// TestTheTokenIsReadAtSendTimeRatherThanHeldFromStartUp.
//
// FCM rotates tokens and the app re-registers. A usecase that captured the
// token when it was built would go on sending to the retired one — which
// Firebase rejects as unregistered, which the worker correctly treats as
// permanent, which means alerts stop for good after a rotation nobody saw.
func TestTheTokenIsReadAtSendTimeRatherThanHeldFromStartUp(t *testing.T) {
	f := newDelivery(t)

	if _, err := f.devices.RegisterDevice(context.Background(), models.Device{
		Token: "rotated-token", Platform: constants.DevicePlatformAndroid,
	}); err != nil {
		t.Fatalf("RegisterDevice() returned error: %v", err)
	}

	if report := f.deliver(t); report.Sent != 1 {
		t.Fatalf("Sent = %d, want 1: %+v", report.Sent, report)
	}
	if got := f.sender.sent[0].Token; got != "rotated-token" {
		t.Fatalf("delivered to %q, want the token registered after the usecase was built; "+
			"the token must be read per delivery, not captured at start-up", got)
	}
}

// TestTheRegistrationIsLookedUpOncePerPassRatherThanPerRow.
//
// Whether a phone is registered is a fact about the deployment, not about any
// one queued row. A backlog with nothing registered must not make one lookup
// per row to reach the same answer, so the pass asks before the loop rather
// than inside it.
//
// Three rows, so removing that pre-check is visible: without it the count
// tracks the backlog.
func TestTheRegistrationIsLookedUpOncePerPassRatherThanPerRow(t *testing.T) {
	f := newBacklog(t, 3)
	f.devices.forget()
	f.devices.lookups = 0

	report := f.deliver(t)
	if report.Waiting != 3 {
		t.Fatalf("Waiting = %d, want all 3: %+v", report.Waiting, report)
	}
	if f.devices.lookups != 1 {
		t.Errorf("the registration was looked up %d times for 3 rows, want 1",
			f.devices.lookups)
	}
}

// TestAPhoneThatDeregistersMidPassLeavesTheRestUntouched.
//
// The pre-check answers for the pass, but the registration can go away between
// it and any given row — a DELETE /api/v1/device while a backlog is draining.
// Everything from that point on is in the "nowhere to send" position and must
// come out of the pass exactly as it went in, not marked failed because the
// answer changed halfway through.
func TestAPhoneThatDeregistersMidPassLeavesTheRestUntouched(t *testing.T) {
	f := newBacklog(t, 3)

	// The pre-check is lookup one and the first row's is lookup two; the
	// registration disappears as that one is served.
	f.devices.forgetAfter = 2

	before := f.queue.rows()
	report := f.deliver(t)

	if report.Sent != 1 {
		t.Errorf("Sent = %d, want the one row served before the registration went: %+v",
			report.Sent, report)
	}
	if report.Waiting != 2 {
		t.Errorf("Waiting = %d, want the 2 rows behind it: %+v", report.Waiting, report)
	}
	if report.GaveUp != 0 || report.Retrying != 0 {
		t.Errorf("report = %+v, want nothing failed or rescheduled", report)
	}

	after := f.queue.rows()
	for i := 1; i < len(after); i++ {
		if after[i].Attempts != before[i].Attempts {
			t.Errorf("row %d spent an attempt (%d to %d) with nowhere to send",
				i, before[i].Attempts, after[i].Attempts)
		}
		if after[i].Status != constants.NotificationStatusPending {
			t.Errorf("row %d is %q, want pending", i, after[i].Status)
		}
	}
}

// TestAPassThatOnlyWaitedIsNotQuiet.
//
// Quiet() decides whether the pass says anything. A pass that delivered
// nothing because there is nowhere to deliver is exactly the state somebody
// needs told about — it is the difference between a strategy that is silent
// and a phone that was never registered.
func TestAPassThatOnlyWaitedIsNotQuiet(t *testing.T) {
	if (notify.DeliveryReport{Waiting: 1}).Quiet() {
		t.Error("a pass with alerts waiting on a registration reported itself as quiet")
	}
	if !(notify.DeliveryReport{}).Quiet() {
		t.Error("an empty pass did not report itself as quiet")
	}
}

// TestNotifyModeStartsWithoutARegisteredDevice.
//
// The chicken-and-egg this design exists to avoid: the phone registers through
// the api after the app is installed, so a collector that refused to start
// without a registration could never reach the state where one exists. What is
// required is the ability to look one up, not the presence of one.
func TestNotifyModeStartsWithoutARegisteredDevice(t *testing.T) {
	empty := &registeredDevices{}

	usecase, err := _notify_us.NewNotifyUsecaseImpl(&recordingQueue{}, silentLog(),
		_notify_us.Config{
			Mode:    constants.SignalModeNotify,
			Sender:  &stubSender{},
			Signals: &storedSignals{},
			Devices: mustDevices(empty),
		})
	if err != nil {
		t.Fatalf("notify mode was refused with no device registered: %v", err)
	}
	if !usecase.Delivers() {
		t.Error("the usecase reports that it does not deliver")
	}
}

// TestRunStopsWhenTheContextIsCancelled, so a shutdown is not held by the
// delivery loop.
func TestRunStopsWhenTheContextIsCancelled(t *testing.T) {
	f := newDelivery(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.usecase.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() returned %v on cancellation, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return when its context was cancelled")
	}
}

func (s *storedSignals) ListSignals(
	context.Context, signal.ListParams,
) ([]models.Signal, int64, error) {
	return nil, 0, errors.New("not used")
}
