//go:build ignore

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/X1aSheng/shark-mqtt/client"
)

func main() {
	addr := flag.String("addr", "localhost:1883", "broker address")
	flag.Parse()

	id := fmt.Sprintf("smoke-test-%d", time.Now().UnixNano())
	cli := client.New(
		client.WithAddr(*addr),
		client.WithClientID(id),
		client.WithCleanSession(true),
	)

	ctx := context.Background()

	fmt.Printf("Connecting to %s (clientID=%s)...\n", *addr, id)
	if err := cli.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: connect: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("PASS: connect")

	if _, err := cli.Subscribe(ctx, []client.TopicSubscription{{Topic: "smoke/test", QoS: 0}}); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: subscribe: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("PASS: subscribe")

	payload := fmt.Sprintf("smoke-%d", time.Now().UnixMilli())
	if err := cli.Publish(ctx, "smoke/test", 0, false, []byte(payload)); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: publish: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("PASS: publish (payload=%s)\n", payload)

	if err := cli.Disconnect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: disconnect: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("PASS: disconnect")
	fmt.Println("\nAll smoke tests passed.")
}
