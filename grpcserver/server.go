// Package grpcserver provides the gRPC server implementation for GoFlow.
//
// It wraps the GoFlow server engine and exposes it as a gRPC service,
// allowing C#, Python, or any gRPC-compatible language to submit tasks.
//
// # Usage
//
//	engine := server.New(server.Workers(8))
//	engine.Register("process", "Process items", handler)
//
//	srv := grpcserver.New(engine, grpcserver.Port(50051))
//	srv.Start()
package grpcserver

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/kzxl/goflow/server"
)

// Server is a gRPC server wrapper around GoFlow engine.
type Server struct {
	engine  *server.Engine
	port    int
	addr    string
	mu      sync.Mutex
	started bool
}

// Option configures the gRPC server.
type Option func(*Server)

// Port sets the listening port.
func Port(p int) Option {
	return func(s *Server) { s.port = p }
}

// Address sets the listening address (e.g. "0.0.0.0:50051").
func Address(addr string) Option {
	return func(s *Server) { s.addr = addr }
}

// New creates a new gRPC server.
func New(engine *server.Engine, opts ...Option) *Server {
	s := &Server{
		engine: engine,
		port:   50051,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ListenAddr returns the address the server will listen on.
func (s *Server) ListenAddr() string {
	if s.addr != "" {
		return s.addr
	}
	return fmt.Sprintf(":%d", s.port)
}

// Start starts listening for gRPC connections.
// NOTE: This is a framework-ready stub. To fully implement, you need:
//  1. `protoc --go_out=. --go-grpc_out=. proto/goflow.proto`
//  2. Import the generated pb package
//  3. Register the gRPC service
//
// This shows the server lifecycle pattern:
func (s *Server) Start() error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("server already started")
	}
	s.started = true
	s.mu.Unlock()

	lis, err := net.Listen("tcp", s.ListenAddr())
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	defer lis.Close()

	fmt.Printf("GoFlow gRPC server listening on %s\n", s.ListenAddr())
	fmt.Printf("Registered handlers: %d\n", len(s.engine.GetHandlers()))
	for _, h := range s.engine.GetHandlers() {
		fmt.Printf("  - %s: %s\n", h.Name, h.Description)
	}

	// Accept connections in a loop
	for {
		conn, err := lis.Accept()
		if err != nil {
			return err
		}
		go s.handleConnection(conn)
	}
}

// StartAsync starts the server in a background goroutine.
func (s *Server) StartAsync() error {
	go s.Start()
	return nil
}

// handleConnection handles a raw TCP connection.
// In production, this would be replaced by the generated gRPC server.
// This implementation uses a simple JSON-over-TCP protocol for demo:
func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Simple protocol: read task request JSON, process, write result JSON
	buf := make([]byte, 64*1024)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		// Parse request
		var req server.TaskRequest
		if err := server.UnmarshalPayload(buf[:n], &req); err != nil {
			resp := server.TaskResult{Error: "invalid request: " + err.Error()}
			data := server.MarshalPayload(resp)
			conn.Write(data)
			continue
		}

		// Execute
		result := s.engine.Execute(context.Background(), req)
		data := server.MarshalPayload(result)
		conn.Write(data)
	}
}
