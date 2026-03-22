package server

import (
	"context"
	"testing"
)

func TestEngineRegisterAndExecute(t *testing.T) {
	e := New(Workers(2))
	defer e.Close()

	e.Register("greet", "Greet handler", func(ctx context.Context, payload []byte) ([]byte, error) {
		return []byte("Hello, " + string(payload)), nil
	})

	result := e.Execute(context.Background(), TaskRequest{
		ID: "1", Handler: "greet", Payload: []byte("World"),
	})

	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if string(result.Data) != "Hello, World" {
		t.Errorf("expected 'Hello, World', got %q", string(result.Data))
	}
}

func TestEngineHandlerNotFound(t *testing.T) {
	e := New()
	defer e.Close()

	result := e.Execute(context.Background(), TaskRequest{
		ID: "1", Handler: "nonexistent",
	})

	if result.Success {
		t.Error("expected failure for missing handler")
	}
	if result.Error == "" {
		t.Error("expected error message")
	}
}

func TestEngineBatch(t *testing.T) {
	e := New(Workers(4))
	defer e.Close()

	e.Register("double", "Double", func(_ context.Context, payload []byte) ([]byte, error) {
		n := int(payload[0])
		return []byte{byte(n * 2)}, nil
	})

	tasks := make([]TaskRequest, 5)
	for i := range tasks {
		tasks[i] = TaskRequest{ID: string(rune(i + '0')), Handler: "double", Payload: []byte{byte(i + 1)}}
	}

	results := e.ExecuteBatch(context.Background(), tasks)
	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}

	for i, r := range results {
		if !r.Success {
			t.Errorf("[%d] expected success, got error: %s", i, r.Error)
		}
	}
}

func TestEngineAsync(t *testing.T) {
	e := New(Workers(2))
	defer e.Close()

	e.Register("echo", "Echo", func(_ context.Context, payload []byte) ([]byte, error) {
		return payload, nil
	})

	ch := e.ExecuteAsync(context.Background(), TaskRequest{
		ID: "1", Handler: "echo", Payload: []byte("async"),
	})

	result := <-ch
	if !result.Success || string(result.Data) != "async" {
		t.Errorf("expected 'async', got %q (err: %s)", string(result.Data), result.Error)
	}
}

func TestEngineGetHandlers(t *testing.T) {
	e := New()
	e.Register("a", "Handler A", nil)
	e.Register("b", "Handler B", nil)

	handlers := e.GetHandlers()
	if len(handlers) != 2 {
		t.Errorf("expected 2 handlers, got %d", len(handlers))
	}
}

func TestMarshalUnmarshal(t *testing.T) {
	type Data struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	original := Data{Name: "test", Age: 25}
	payload := MarshalPayload(original)

	var decoded Data
	err := UnmarshalPayload(payload, &decoded)

	if err != nil {
		t.Errorf("unmarshal error: %v", err)
	}
	if decoded.Name != "test" || decoded.Age != 25 {
		t.Errorf("expected {test, 25}, got %+v", decoded)
	}
}
