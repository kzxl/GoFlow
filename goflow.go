// Package goflow provides a high-performance concurrency toolkit for Go.
//
// GoFlow offers production-ready primitives for parallel processing,
// worker pools, pipelines, retry logic, rate limiting, and more.
//
// # Quick Start
//
//	// Parallel map over a slice
//	results, errs := parallel.Map(urls, fetchURL, parallel.Workers(10))
//
//	// Worker pool for continuous task processing
//	pool := pool.New(pool.Workers(8), pool.QueueSize(1000))
//	pool.Submit(func() { processItem(item) })
//
//	// Retry with exponential backoff
//	result, err := retry.Do(func() (string, error) { return callAPI() })
package goflow
