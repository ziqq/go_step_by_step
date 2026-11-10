package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// shutdownService is called once by the service owner.
// stopWorkers stops admission and signals workers without waiting.
// Its channel closes only after every worker stops using resource.
func shutdownService(
	ctx context.Context,
	markUnready func(),
	stopWorkers func() <-chan struct{},
	drainHTTP func(context.Context) error,
	resource io.Closer,
) error {
	markUnready()
	workersDone := stopWorkers()

	if err := drainHTTP(ctx); err != nil {
		return fmt.Errorf("drain HTTP: %w", err)
	}

	select {
	case <-workersDone:
	case <-ctx.Done():
		return fmt.Errorf("join workers: %w", ctx.Err())
	}
	// A select may choose workersDone when both channels are ready.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("finish drain: %w", err)
	}

	if err := resource.Close(); err != nil {
		return fmt.Errorf("close shared resource: %w", err)
	}
	return nil
}

type demoStore struct{}

func (demoStore) Close() error {
	fmt.Println("store closed")
	return nil
}

func main() {
	srv := &http.Server{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-stop
		fmt.Println("worker stopped")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := shutdownService(ctx,
		func() { fmt.Println("readiness off") },
		func() <-chan struct{} { close(stop); return done },
		srv.Shutdown,
		demoStore{},
	)
	if err != nil {
		log.Fatal(err)
	}
}
