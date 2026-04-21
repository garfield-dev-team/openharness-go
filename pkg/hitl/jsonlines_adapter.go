package hitl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/openharness/openharness/pkg/protocol"
)

// JSONLinesAdapter handles HITL via JSON-Lines protocol over io.Reader/Writer.
type JSONLinesAdapter struct {
	reader     io.Reader
	writer     io.Writer
	manager    *Manager
	scanOnce   sync.Once
	scanner    *bufio.Scanner
	incomingCh chan *protocol.FrontendRequest
}

func NewJSONLinesAdapter(r io.Reader, w io.Writer) *JSONLinesAdapter {
	return &JSONLinesAdapter{
		reader:     r,
		writer:     w,
		incomingCh: make(chan *protocol.FrontendRequest, 32),
	}
}

func (a *JSONLinesAdapter) SetManager(m *Manager) { a.manager = m }

func (a *JSONLinesAdapter) StartReadLoop(ctx context.Context) error {
	a.scanOnce.Do(func() {
		a.scanner = bufio.NewScanner(a.reader)
		a.scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !a.scanner.Scan() {
			if err := a.scanner.Err(); err != nil {
				return fmt.Errorf("jsonlines read error: %w", err)
			}
			return io.EOF
		}
		line := a.scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		req, err := protocol.ParseFrontendRequest(line)
		if err != nil {
			a.emit(&protocol.BackendEvent{
				Type:  protocol.BEError,
				Error: fmt.Sprintf("invalid request: %v", err),
			})
			continue
		}
		switch req.Type {
		case protocol.FRQuestionResponse, protocol.FRPermissionResponse:
			if a.manager != nil {
				if routeErr := a.manager.HandleFrontendRequest(req); routeErr != nil {
					a.emit(&protocol.BackendEvent{
						Type:  protocol.BEError,
						Error: routeErr.Error(),
					})
				}
			}
		default:
			select {
			case a.incomingCh <- req:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (a *JSONLinesAdapter) emit(event *protocol.BackendEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintf(a.writer, "%s\n", data)
}

func (a *JSONLinesAdapter) EmitFn() func(event *protocol.BackendEvent) { return a.emit }

func (a *JSONLinesAdapter) IncomingRequests() <-chan *protocol.FrontendRequest {
	return a.incomingCh
}