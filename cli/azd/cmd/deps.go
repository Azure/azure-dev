package cmd

import (
	"net/http"

	"github.com/benbjohnson/clock"
)

func createHttpClient() *http.Client {
	return &http.Client{}
}

func createClock() clock.Clock {
	return clock.New()
}
