// Package fcm delivers messages through Firebase Cloud Messaging.
//
// It is a repository: everything here is about talking to Google, and none of
// the wire shapes below leave this package. Callers hand it a notify.Message
// and get back an error or nothing.
//
// # What this package may not do
//
// Nothing here touches an order, a trade, a balance or a withdrawal, and no
// credential it holds could reach one: the service account is scoped to
// firebase.messaging and Firebase has no such endpoints. See CLAUDE.md §1.
package fcm

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
)

// messagingScope is the only scope this system asks for.
//
// It permits sending messages and nothing else. A broader scope would be a
// credential that could do more than the thing it was issued for, on a host
// whose whole job is to read public market data.
const messagingScope = "https://www.googleapis.com/auth/firebase.messaging"

// defaultEndpoint is Google's FCM v1 send API. The project id is substituted
// in; it is a field so a test can point the client at a local server.
const defaultEndpoint = "https://fcm.googleapis.com"

// Config is what the sender needs to reach Firebase.
type Config struct {
	// ProjectId is the Firebase project that owns the device token.
	ProjectId string

	// CredentialsFile is the service account JSON. It is a private key: it is
	// read once at start-up and never logged.
	CredentialsFile string

	// Endpoint overrides the FCM host. Empty means Google's; a test points it
	// at its own server so no test ever needs a network.
	Endpoint string

	// HTTPClient overrides the client used for the send. When it is set, the
	// OAuth2 token source is not consulted — a test supplies its own
	// transport rather than a service account it would have to invent.
	HTTPClient *http.Client

	// Timeout bounds one send. A delivery that hangs would hold the worker
	// and the queue behind it.
	Timeout time.Duration

	// Log carries start-up warnings about the credentials. Optional.
	Log *slog.Logger
}

type sender struct {
	projectId string
	endpoint  string
	client    *http.Client
}

// NewSenderImpl builds an FCM client from a service account file.
//
// The credentials are read and exchanged for a token source here, at
// start-up, so a missing or malformed key file is a refusal to start rather
// than a failure discovered on the first signal — which could be days later,
// and would look like the strategy being quiet.
func NewSenderImpl(ctx context.Context, cfg Config) (notify.Sender, error) {
	if cfg.ProjectId == "" {
		return nil, fmt.Errorf("fcm: no project id")
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	if _, err := url.Parse(endpoint); err != nil {
		return nil, fmt.Errorf("fcm: endpoint %q: %w", endpoint, err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = constants.NotifySendTimeout
	}

	client := cfg.HTTPClient
	if client == nil {
		var err error
		if client, err = authenticatedClient(ctx, cfg.ProjectId, cfg.Log, cfg.CredentialsFile); err != nil {
			return nil, err
		}
	}
	client.Timeout = timeout

	return &sender{projectId: cfg.ProjectId, endpoint: endpoint, client: client}, nil
}

// authenticatedClient exchanges the service account for a token source.
//
// golang.org/x/oauth2/google rather than the Firebase Admin SDK: the SDK
// carries a large dependency tree to make one authenticated POST, and the one
// thing it would do for us is this exchange.
func authenticatedClient(
	ctx context.Context, projectId string, log *slog.Logger, path string,
) (*http.Client, error) {
	if path == "" {
		return nil, fmt.Errorf("fcm: no credentials file")
	}

	// Read from a file rather than taken as a value: a private key in an
	// environment variable ends up in process listings and in crash dumps.
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fcm: read credentials: %w", err)
	}

	if err := checkServiceAccount(key, projectId, log); err != nil {
		// Only the path, never the contents: the error would otherwise carry
		// a private key into the logs and everywhere they are shipped.
		return nil, fmt.Errorf("fcm: %s: %w", path, err)
	}

	credentials, err := google.CredentialsFromJSON(ctx, key, messagingScope)
	if err != nil {
		return nil, fmt.Errorf("fcm: %s is not a usable service account key", path)
	}
	return oauth2.NewClient(ctx, credentials.TokenSource), nil
}

// serviceAccount is the part of the key file that has to be right.
type serviceAccount struct {
	Type        string `json:"type"`
	ProjectId   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

// checkServiceAccount rejects a key file that cannot work.
//
// # Why this is not left to the library
//
// Neither google.CredentialsFromJSON nor google.JWTConfigFromJSON validates
// anything beyond JSON syntax: both accept {"type":"service_account"} with no
// key in it and fail later, at the first token exchange. For this system that
// first exchange is the first signal, which could be days after the deploy —
// and a silent notification path looks exactly like a strategy that has not
// found anything. Refusing to start says which it is, immediately.
//
// This deliberately makes no network call. Google being briefly unreachable at
// boot must not stop the collector, whose other job is storing candles.
func checkServiceAccount(key []byte, projectId string, log *slog.Logger) error {
	var account serviceAccount
	if err := json.Unmarshal(key, &account); err != nil {
		return fmt.Errorf("not JSON: %w", err)
	}

	if account.Type != "service_account" {
		return fmt.Errorf(
			"is a %q credential; FCM needs a service account key, downloaded from "+
				"Firebase console > Project settings > Service accounts", account.Type)
	}
	if account.ClientEmail == "" {
		return errors.New("has no client_email")
	}
	if account.PrivateKey == "" {
		return errors.New("has no private_key")
	}

	block, _ := pem.Decode([]byte(account.PrivateKey))
	if block == nil {
		return errors.New("has a private_key that is not PEM")
	}
	if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
		return errors.New("has a private_key that will not parse")
	}

	// A key belonging to another project is unusual rather than impossible —
	// Firebase can grant one cross-project access — so this is said and not
	// refused. Said loudly, because the alternative is a 403 on the first
	// signal that reads like a Firebase problem.
	if account.ProjectId != "" && account.ProjectId != projectId && log != nil {
		log.Warn("the service account belongs to a different Firebase project",
			"key_project", account.ProjectId,
			"fcm_project_id", projectId,
			"consequence", "delivery will be refused unless the key was granted access to it")
	}
	return nil
}

// Channel is the delivery target this sender serves.
func (s *sender) Channel() constants.NotificationChannel {
	return constants.NotificationChannelFCM
}

// Send delivers one message.
//
// A rejection that retrying cannot fix is wrapped with
// notify.ErrUndeliverable so the caller stops rather than spending its whole
// attempt budget on a device token that no longer exists.
func (s *sender) Send(ctx context.Context, message notify.Message) error {
	if message.Token == "" {
		return fmt.Errorf("fcm: no device token: %w", notify.ErrUndeliverable)
	}

	body, err := json.Marshal(sendRequest{Message: toFCM(message)})
	if err != nil {
		return fmt.Errorf("fcm: encode message: %w", err)
	}

	endpoint := fmt.Sprintf("%s/v1/projects/%s/messages:send",
		strings.TrimSuffix(s.endpoint, "/"), s.projectId)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("fcm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := s.client.Do(req)
	if err != nil {
		// A transport failure is transient by nature: the network, the token
		// exchange, or a timeout. All three are worth another attempt.
		return fmt.Errorf("fcm: send: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusOK {
		return nil
	}
	return classify(res.StatusCode, readError(res.Body))
}

// readError reads a bounded amount of the error body.
//
// Bounded because it is stored in notifications.last_error and shown to a
// person: an unbounded response would put an arbitrary amount of somebody
// else's text into a column meant to hold an explanation.
func readError(r io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(r, constants.NotifyErrorBodyLimit))
	if err != nil {
		return ""
	}

	var parsed errorResponse
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.Error.Message != "" {
		if parsed.Error.Status != "" {
			return parsed.Error.Status + ": " + parsed.Error.Message
		}
		return parsed.Error.Message
	}
	return strings.TrimSpace(string(raw))
}

// classify decides whether a rejection is worth repeating.
//
// The split follows what the status actually means rather than a list of
// codes: 4xx is Google saying the request is wrong, and sending the same
// wrong request again will be wrong again. The two exceptions are the ones
// that are about timing rather than content.
func classify(status int, detail string) error {
	if detail == "" {
		detail = http.StatusText(status)
	}

	switch {
	case status == http.StatusTooManyRequests, status == http.StatusRequestTimeout:
		return fmt.Errorf("fcm: %d %s", status, detail)
	case status >= 400 && status < 500:
		return fmt.Errorf("fcm: %d %s: %w", status, detail, notify.ErrUndeliverable)
	default:
		return fmt.Errorf("fcm: %d %s", status, detail)
	}
}
