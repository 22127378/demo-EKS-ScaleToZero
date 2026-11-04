package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCredProviderReusesWithinTTLAndRefreshesAfter(t *testing.T) {
	calls := 0
	pass := "old"
	p := newCredProvider(func(context.Context) (string, string, error) {
		calls++
		return "postgres", pass, nil
	}, time.Minute)
	clock := time.Unix(1000, 0)
	p.now = func() time.Time { return clock }

	if _, got, _ := p.get(context.Background()); got != "old" {
		t.Fatalf("first password = %q, want old", got)
	}
	pass = "rotated"
	clock = clock.Add(30 * time.Second)
	if _, got, _ := p.get(context.Background()); got != "old" || calls != 1 {
		t.Fatalf("inside the TTL: password = %q, fetches = %d; want the cached one and 1 fetch", got, calls)
	}
	clock = clock.Add(31 * time.Second)
	if _, got, _ := p.get(context.Background()); got != "rotated" || calls != 2 {
		t.Fatalf("after the TTL: password = %q, fetches = %d; want the rotated one and 2 fetches", got, calls)
	}
}

func TestCredProviderKeepsLastCredentialsWhenRefreshFails(t *testing.T) {
	fail := false
	p := newCredProvider(func(context.Context) (string, string, error) {
		if fail {
			return "", "", errors.New("secrets manager unavailable")
		}
		return "postgres", "pw", nil
	}, time.Minute)
	clock := time.Unix(1000, 0)
	p.now = func() time.Time { return clock }

	if _, _, err := p.get(context.Background()); err != nil {
		t.Fatal(err)
	}
	fail = true
	clock = clock.Add(2 * time.Minute)
	if _, got, err := p.get(context.Background()); err != nil || got != "pw" {
		t.Fatalf("stale fallback: password = %q, err = %v; want pw and no error", got, err)
	}
}

func TestCredProviderFirstFetchErrorIsReturned(t *testing.T) {
	p := newCredProvider(func(context.Context) (string, string, error) {
		return "", "", errors.New("boom")
	}, time.Minute)
	if _, _, err := p.get(context.Background()); err == nil {
		t.Fatal("expected an error when there is nothing cached")
	}
}
