package server

import (
	"bufio"
	"fmt"
	"net"
	"sync"

	"github.com/helios/helios/internal/atlas/protocol"
)

// Server is the TCP server for ATLAS
type Server struct {
	address  string
	listener net.Listener
	handler  CommandHandler
	wg       sync.WaitGroup
	stopCh   chan struct{}
}

// CommandHandler processes commands
type CommandHandler interface {
	Handle(*protocol.Command) *protocol.Response
}

// New creates a new server
func New(address string, handler CommandHandler) *Server {
	return &Server{
		address: address,
		handler: handler,
		stopCh:  make(chan struct{}),
	}
}

// Start begins listening for connections
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	s.listener = listener
	fmt.Printf("ATLAS server listening on %s\n", s.address)

	go s.acceptLoop()
	return nil
}

// Stop gracefully stops the server
func (s *Server) Stop() error {
	close(s.stopCh)
	if s.listener != nil {
		s.listener.Close()
	}
	s.wg.Wait()
	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stopCh:
				return
			default:
				fmt.Printf("accept error: %v\n", err)
				continue
			}
		}

		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	writer := bufio.NewWriter(conn)

	for scanner.Scan() {
		line := scanner.Text()

		cmd, err := protocol.Parse(line)
		if err != nil {
			resp := protocol.NewErrorResponse(err)
			s.sendResponse(writer, resp)
			continue
		}

		resp := s.handler.Handle(cmd)
		s.sendResponse(writer, resp)
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("connection error: %v\n", err)
	}
}

func (s *Server) sendResponse(writer *bufio.Writer, resp *protocol.Response) {
	respStr, err := protocol.SerializeResponse(resp)
	if err != nil {
		fmt.Printf("failed to serialize response: %v\n", err)
		return
	}

	writer.WriteString(respStr + "\n")
	writer.Flush()
}
