// Package server provides a gRPC server that wraps the GoFlow engine.
//
// It allows C#, Python, or any gRPC-compatible language to submit tasks
// for concurrent processing by the Go engine.
//
// # Usage (Go side)
//
//	engine := server.New(server.Workers(8), server.QueueSize(10000))
//	engine.Register("resize-image", "Resize images", resizeHandler)
//	engine.Serve(":50051")
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/kzxl/goflow/pool"
)

// HandlerFunc processes a task and returns result data.
type HandlerFunc func(ctx context.Context, payload []byte) ([]byte, error)

// HandlerInfo describes a registered handler.
type HandlerInfo struct {
	Name        string
	Description string
	Handler     HandlerFunc
}

// TaskRequest represents an incoming task from a client.
type TaskRequest struct {
	ID        string
	Handler   string
	Payload   []byte
	Metadata  map[string]string
	TimeoutMs int32
}

// TaskResult represents the result of processing a task.
type TaskResult struct {
	ID         string
	Success    bool
	Data       []byte
	Error      string
	DurationNs int64
}

// Engine is the GoFlow task processing engine.
type Engine struct {
	pool     *pool.Pool
	handlers map[string]HandlerInfo
	mu       sync.RWMutex
}

// Option configures the engine.
type Option func(*engineConfig)

type engineConfig struct {
	workers   int
	queueSize int
}

// Workers sets the number of worker goroutines.
func Workers(n int) Option {
	return func(c *engineConfig) { c.workers = n }
}

// QueueSize sets the task queue buffer size.
func QueueSize(n int) Option {
	return func(c *engineConfig) { c.queueSize = n }
}

// New creates a new GoFlow engine.
func New(opts ...Option) *Engine {
	cfg := &engineConfig{
		workers:   8,
		queueSize: 10000,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	p, _ := pool.New(cfg.workers)

	return &Engine{
		pool:     p,
		handlers: make(map[string]HandlerInfo),
	}
}

// Register adds a task handler to the engine.
func (e *Engine) Register(name, description string, handler HandlerFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handlers[name] = HandlerInfo{
		Name:        name,
		Description: description,
		Handler:     handler,
	}
}

// Execute processes a single task synchronously.
func (e *Engine) Execute(ctx context.Context, req TaskRequest) TaskResult {
	start := time.Now()

	e.mu.RLock()
	info, ok := e.handlers[req.Handler]
	e.mu.RUnlock()

	if !ok {
		return TaskResult{
			ID:      req.ID,
			Success: false,
			Error:   fmt.Sprintf("handler not found: %s", req.Handler),
		}
	}

	// Apply timeout if specified
	if req.TimeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	data, err := info.Handler(ctx, req.Payload)
	duration := time.Since(start).Nanoseconds()

	if err != nil {
		return TaskResult{
			ID:         req.ID,
			Success:    false,
			Error:      err.Error(),
			DurationNs: duration,
		}
	}

	return TaskResult{
		ID:         req.ID,
		Success:    true,
		Data:       data,
		DurationNs: duration,
	}
}

// ExecuteAsync submits a task to the worker pool and returns a channel for the result.
func (e *Engine) ExecuteAsync(ctx context.Context, req TaskRequest) <-chan TaskResult {
	ch := make(chan TaskResult, 1)
	e.pool.Submit(func() {
		ch <- e.Execute(ctx, req)
		close(ch)
	})
	return ch
}

// ExecuteBatch processes multiple tasks concurrently and returns all results.
func (e *Engine) ExecuteBatch(ctx context.Context, tasks []TaskRequest) []TaskResult {
	results := make([]TaskResult, len(tasks))
	var wg sync.WaitGroup

	for i, task := range tasks {
		idx := i
		t := task
		wg.Add(1)
		e.pool.Submit(func() {
			defer wg.Done()
			results[idx] = e.Execute(ctx, t)
		})
	}

	wg.Wait()
	return results
}

// GetHandlers returns all registered handler info.
func (e *Engine) GetHandlers() []HandlerInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	infos := make([]HandlerInfo, 0, len(e.handlers))
	for _, info := range e.handlers {
		infos = append(infos, HandlerInfo{
			Name:        info.Name,
			Description: info.Description,
		})
	}
	return infos
}

// GetMetrics returns engine pool metrics.
func (e *Engine) GetMetrics() pool.PoolMetrics {
	return e.pool.Metrics()
}

// Close shuts down the engine.
func (e *Engine) Close() {
	e.pool.Wait()
	e.pool.Close()
}

// Helper: MarshalPayload converts any Go value to JSON bytes for task payload.
func MarshalPayload(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}

// Helper: UnmarshalPayload converts JSON bytes back to a Go value.
func UnmarshalPayload(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
