// Command vapidkeys prints a fresh VAPID key pair.
//
// Web Push identifies the application server by a key pair (RFC 8292): the
// browser subscribes against the public half, and every push is signed with
// the private half. Both are needed before SIGNAL_MODE=notify can do anything,
// and there is nowhere to get them but a generator — which is why this exists
// rather than a line in the runbook telling somebody to find one online.
//
//	make vapid-keys
//
// Run it once. Rotating the pair invalidates every existing subscription,
// because the push service refuses a push signed by a key the subscription was
// not made against; the phone re-registers on its next launch, so the cost is
// a missed signal rather than a manual step, but there is no reason to rotate
// on a schedule.
package main

import (
	"fmt"
	"os"

	push "github.com/SherClockHolmes/webpush-go"
)

func main() {
	private, public, err := push.GenerateVAPIDKeys()
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate a VAPID key pair: %v\n", err)
		os.Exit(1)
	}

	// Printed as .env lines so this can be appended rather than transcribed.
	// A key copied by hand is a key with a missing character, and the symptom
	// is a 403 from the push service that reads like a server problem.
	fmt.Printf("VAPID_PUBLIC_KEY=%s\n", public)
	fmt.Printf("VAPID_PRIVATE_KEY=%s\n", private)
	fmt.Println("VAPID_SUBJECT=mailto:you@example.com")

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Append these to .env and set VAPID_SUBJECT to a real address.")
	fmt.Fprintln(os.Stderr, "The private key is a credential: it is never logged, never served,")
	fmt.Fprintln(os.Stderr, "and must not reach an image layer or the repository.")
}
