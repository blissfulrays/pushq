package main

import (
	"github.com/blissfulrays/pushq/internal/store"
	"github.com/blissfulrays/pushq/internal/store/postgres"
)

func openPostgres(dsn string) (store.Store, error) {
	return postgres.Open(dsn)
}
