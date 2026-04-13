package app

import "context"

type Options struct {
	RetryPath          string
	RetryOutPath       string
	RetryFailedOutPath string
}

// Run executes the application flow (full crawl or retry mode).
// Implementation is added during refactor; kept as a separate package so main stays small.
func Run(ctx context.Context, opts Options) error {
	_ = ctx
	_ = opts
	return nil
}

