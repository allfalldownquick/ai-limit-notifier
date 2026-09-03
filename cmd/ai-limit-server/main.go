// Command ai-limit-server is the hosted/self-hosted API + scheduler
// described in docs/PROTOCOL_V1.md and docs/DISTRIBUTION.md. It listens
// plain HTTP: a production deployment terminates TLS in a reverse proxy in
// front of it (see the deployment plan in the P3 report), so this binary
// never manages certificates itself.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/allfalldownquick/ai-limit-notifier/internal/domain"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/api"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/delivery"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/scheduler"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/store"
	"github.com/allfalldownquick/ai-limit-notifier/internal/server/telegrambot"
)

// stringSliceFlag collects a repeatable flag (e.g. multiple
// --trusted-proxy occurrences) into a slice.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// loggingDelivery makes the local/self-hosted "no bot token configured"
// path observable: every simulated send is printed (destination + the
// server-built message only — never any device-supplied text) instead of
// silently vanishing into FakeDelivery.
type loggingDelivery struct {
	inner delivery.Delivery
}

func (l *loggingDelivery) Send(ctx context.Context, destination, message string) error {
	err := l.inner.Send(ctx, destination, message)
	if err == nil {
		fmt.Printf("delivery: sent to %s: %s\n", destination, message)
	}
	return err
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(3)
	}
	var code int
	switch os.Args[1] {
	case "serve":
		code = runServe(os.Args[2:])
	case "bootstrap-device":
		code = runBootstrapDevice(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		code = 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		code = 3
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprintln(os.Stderr, `ai-limit-server <command>

Commands:
  serve             Run the API server + scheduler.
  bootstrap-device  Create a test user+device and print its token once (local/test use only).`)
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "listen address (plain HTTP; terminate TLS in a reverse proxy)")
	dbPath := fs.String("db", "ai-limit-server.db", "SQLite database path")
	combineWindow := fs.Duration("combine-window", 10*time.Minute, "max reset_at gap to combine same-window-kind, different-provider events")
	pollInterval := fs.Duration("poll-interval", 5*time.Second, "scheduler poll interval")
	threshold := fs.Float64("threshold", domain.DefaultScheduleThreshold, "used_percent at/above which a durable reset event is created (docs/PROTOCOL_V1.md's default is 80)")
	var trustedProxies stringSliceFlag
	fs.Var(&trustedProxies, "trusted-proxy", "CIDR of a reverse proxy allowed to set X-Forwarded-For (repeatable; none trusted by default)")
	if err := fs.Parse(args); err != nil {
		return 3
	}

	// Fail closed: /api/v1/pair must never accept a pairing attempt with no
	// secret configured to verify codes against.
	pairingSecret := os.Getenv("AI_LIMIT_NOTIFIER_PAIRING_SECRET")
	if pairingSecret == "" {
		fmt.Fprintln(os.Stderr, "serve: AI_LIMIT_NOTIFIER_PAIRING_SECRET is required (a long random string, kept out of git)")
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open store: %v\n", err)
		return 1
	}
	defer st.Close()

	botToken := os.Getenv("AI_LIMIT_NOTIFIER_TELEGRAM_BOT_TOKEN")

	var deliv delivery.Delivery
	if botToken != "" {
		deliv = delivery.NewTelegramDelivery(botToken)
		fmt.Println("serve: Telegram delivery enabled")
	} else {
		deliv = &loggingDelivery{inner: delivery.NewFakeDelivery()}
		fmt.Println("serve: no AI_LIMIT_NOTIFIER_TELEGRAM_BOT_TOKEN set — using FakeDelivery (each send is logged to stdout, not actually sent anywhere); the onboarding bot is also disabled")
	}

	apiServer := api.New(st)
	apiServer.CombineWindow = *combineWindow
	apiServer.Threshold = *threshold
	apiServer.SetPairingSecret([]byte(pairingSecret))
	if err := apiServer.SetTrustedProxies(trustedProxies); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}
	if len(trustedProxies) > 0 {
		fmt.Printf("serve: trusting X-Forwarded-For from: %s\n", strings.Join(trustedProxies, ", "))
	}

	sch := scheduler.New(st, deliv)
	sch.PollInterval = *pollInterval
	go sch.Run(ctx)

	if botToken != "" {
		bot := telegrambot.New(botToken, st, []byte(pairingSecret))
		go bot.Run(ctx)
		fmt.Println("serve: Telegram onboarding bot running (long polling)")
	}

	httpServer := apiServer.NewHTTPServer(*addr)

	errCh := make(chan error, 1)
	go func() {
		fmt.Printf("serve: listening on %s (db=%s)\n", *addr, *dbPath)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		fmt.Println("serve: shutting down")
	case err := <-errCh:
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(os.Stderr, "serve: shutdown: %v\n", err)
		return 1
	}
	return 0
}

// runBootstrapDevice is the internal test/local-bring-up mechanism the P3
// report's local integration test uses instead of any network-reachable
// backdoor: it requires direct access to run this binary against the
// target database file, which a remote device credential can never grant.
func runBootstrapDevice(args []string) int {
	fs := flag.NewFlagSet("bootstrap-device", flag.ContinueOnError)
	dbPath := fs.String("db", "ai-limit-server.db", "SQLite database path")
	chatID := fs.String("chat-id", "", "optionally link this user to a Telegram chat id immediately (test/local use)")
	if err := fs.Parse(args); err != nil {
		return 3
	}

	ctx := context.Background()
	st, err := store.Open(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open store: %v\n", err)
		return 1
	}
	defer st.Close()

	userID, err := st.CreateUser(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create user: %v\n", err)
		return 1
	}
	if *chatID != "" {
		if err := st.SetTelegramChatID(ctx, userID, *chatID); err != nil {
			fmt.Fprintf(os.Stderr, "set chat id: %v\n", err)
			return 1
		}
	}

	deviceID, rawToken, err := st.CreateDevice(ctx, userID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create device: %v\n", err)
		return 1
	}

	fmt.Println("bootstrap-device: created (this token is shown once and not recoverable)")
	fmt.Printf("user_id:      %s\n", userID)
	fmt.Printf("device_id:    %s\n", deviceID)
	fmt.Printf("device_token: %s\n", rawToken)
	return 0
}
