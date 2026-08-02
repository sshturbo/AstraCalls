package main

import (
	"net/url"
	"testing"
)

func TestDBProviderNamesAndDSN(t *testing.T) {
	base, err := url.Parse("postgres://user:pass@db:5432/postgres?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	provider := &dbProvider{base: base, ns: "wacalls"}

	if got := provider.mainDBName(); got != "wacalls_main" {
		t.Fatalf("expected main database name wacalls_main, got %q", got)
	}
	if got := provider.sessionDBName("session123"); got != "wacalls_session123" {
		t.Fatalf("expected session database name wacalls_session123, got %q", got)
	}
	if got := provider.dsnFor("wacalls_session123"); got != "postgres://user:pass@db:5432/wacalls_session123?sslmode=disable" {
		t.Fatalf("unexpected session DSN: %q", got)
	}
}

func TestQuoteIdentEscapesQuotes(t *testing.T) {
	if got := quoteIdent(`wa"calls`); got != `"wa""calls"` {
		t.Fatalf("unexpected quoted identifier: %q", got)
	}
}
