package osupdate

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestANewSearchEndsTheOneBefore(t *testing.T) {
	previous := searchAPK
	t.Cleanup(func() { searchAPK = previous })
	started := make(chan struct{}, 2)
	searchAPK = func(ctx context.Context, query string) (string, error) {
		started <- struct{}{}
		if query == "first" {
			<-ctx.Done() // a long scan, until something ends it
			return "", ctx.Err()
		}
		return "", nil
	}
	first := make(chan error, 1)
	go func() {
		_, err := newestSearch(context.Background(), "first")
		first <- err
	}()
	<-started
	if _, err := newestSearch(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-first:
		if !errors.Is(err, ErrSearchSuperseded) {
			t.Fatalf("the first search ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first search kept running")
	}
}

func TestASearchEndsWithItsRequest(t *testing.T) {
	previous := searchAPK
	t.Cleanup(func() { searchAPK = previous })
	searchAPK = func(ctx context.Context, query string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := newestSearch(ctx, "gone")
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the search outlived its request")
	}
}
