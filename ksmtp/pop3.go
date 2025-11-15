// Package ksmtp - Serveur POP3/POP3S pour récupération d'emails
//
// USAGE SIMPLE:
//
//	// 1. Créer la configuration
//	conf := &ksmtp.ConfPOP3{
//	    Address:    ":110",  // POP3 standard
//	    AddressTLS: ":995",  // POP3S sécurisé
//	    Users: map[string]string{
//	        "admin": "password123",
//	        "user":  "mypassword",
//	    },
//	    TLSCertPath: "/path/to/cert.pem", // Optionnel
//	    TLSKeyPath:  "/path/to/key.pem",  // Optionnel
//	}
//
//	// 2. Créer le store d'emails
//	emailStore := ksmtp.NewMockEmailStore() // ou votre implémentation
//
//	// 3. Créer et démarrer le serveur
//	server, err := ksmtp.NewPOP3Server(conf, emailStore)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// 4. Démarrer le serveur (bloquant)
//	go server.Start()
//
//	// 5. Gérer les utilisateurs
//	server.AddUser("newuser", "newpass")
//
//	// 6. Arrêter le serveur
//	server.Stop()
//
// COMMANDES POP3 SUPPORTÉES:
//   - USER/PASS : Authentification
//   - STAT       : Nombre et taille des messages
//   - LIST       : Liste des messages
//   - RETR       : Récupérer un message
//   - DELE       : Marquer pour suppression
//   - NOOP       : Pas d'opération
//   - RSET       : Annuler les suppressions
//   - QUIT       : Fermer et appliquer suppressions
//   - CAPA       : Capacités du serveur
//   - UIDL       : Identifiants uniques
//   - TOP        : En-têtes + N lignes du corps
//
// PORTS:
//   - 110 : POP3 standard (non chiffré)
//   - 995 : POP3S (TLS/SSL natif)
//
// SÉCURITÉ:
//   - Support TLS/SSL natif sur port 995
//   - Authentification par utilisateur/mot de passe
//   - Timeout de connexion (10 minutes)
//   - Gestion des sessions multiples
package ksmtp

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// EmailStore interface for retrieving emails (to be implemented with SQLite later)
type EmailStore interface {
	GetEmails(username string) ([]*StoredEmail, error)
	DeleteEmail(username, emailID string) error
	MarkAsRead(username, emailID string) error
	GetEmailByID(username, emailID string) (*StoredEmail, error)
}

// StoredEmail represents an email stored in the database
type StoredEmail struct {
	ID      string
	From    string
	To      string
	Subject string
	Body    string
	Date    string
	Size    int
	Deleted bool
	Read    bool
	Headers string
}

// POP3Server represents a POP3 server
type POP3Server struct {
	Address     string
	AddressTLS  string
	tlsConfig   *tls.Config
	users       map[string]string // username -> password
	emailStore  EmailStore
	done        chan struct{}
	listener    net.Listener
	listenerTLS net.Listener
}

// POP3Session represents a POP3 client session
type POP3Session struct {
	conn          net.Conn
	reader        *bufio.Reader
	authenticated bool
	username      string
	emails        []*StoredEmail
	deletedEmails map[string]bool
	server        *POP3Server
}

// ConfPOP3 configuration for POP3 server
type ConfPOP3 struct {
	Address     string            // :110 for POP3
	AddressTLS  string            // :995 for POP3S
	Users       map[string]string // username -> password
	TLSCertPath string            // Path to TLS certificate file
	TLSKeyPath  string            // Path to TLS private key file
}

// NewPOP3Server creates a new POP3 server
func NewPOP3Server(conf *ConfPOP3, emailStore EmailStore) (*POP3Server, error) {
	if conf == nil {
		return nil, fmt.Errorf("no configuration provided")
	}

	if emailStore == nil {
		return nil, fmt.Errorf("email store is required")
	}

	// Initialize users map
	users := make(map[string]string)
	if conf.Users != nil {
		for username, password := range conf.Users {
			users[username] = password
		}
	}

	// Load TLS configuration if provided
	var tlsConfig *tls.Config
	if conf.TLSCertPath != "" && conf.TLSKeyPath != "" {
		cert, err := tls.LoadX509KeyPair(conf.TLSCertPath, conf.TLSKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
		}
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
		fmt.Printf("🔒 POP3 TLS certificate loaded\n")
	}

	return &POP3Server{
		Address:    conf.Address,
		AddressTLS: conf.AddressTLS,
		tlsConfig:  tlsConfig,
		users:      users,
		emailStore: emailStore,
		done:       make(chan struct{}),
	}, nil
}

// Start starts the POP3 server
func (p *POP3Server) Start() error {
	// Start regular POP3 server if configured
	if p.Address != "" {
		go func() {
			if err := p.startServer(p.Address); err != nil {
				fmt.Printf("❌ POP3 Server error: %v\n", err)
			}
		}()
	}

	// Start POP3S server if configured
	if p.AddressTLS != "" && p.tlsConfig != nil {
		go func() {
			if err := p.startServerTLS(p.AddressTLS); err != nil {
				fmt.Printf("❌ POP3S Server error: %v\n", err)
			}
		}()
	}

	// Keep the main goroutine alive
	<-p.done
	return nil
}

// startServer starts a regular POP3 server
func (p *POP3Server) startServer(address string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("failed to start POP3 server: %w", err)
	}
	p.listener = listener
	defer listener.Close()

	fmt.Printf("🚀 POP3 Server started on %s\n", address)

	for {
		select {
		case <-p.done:
			fmt.Printf("🛑 POP3 Server stopping...\n")
			return nil
		default:
			// Set a timeout for Accept to allow checking the done channel
			if tcpListener, ok := listener.(*net.TCPListener); ok {
				tcpListener.SetDeadline(time.Now().Add(1 * time.Second))
			}

			conn, err := listener.Accept()
			if err != nil {
				// Check if it's a timeout error and continue if server is not stopping
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				select {
				case <-p.done:
					return nil
				default:
					fmt.Printf("❌ POP3 Connection error: %v\n", err)
					continue
				}
			}

			go p.handleConnection(conn)
		}
	}
}

// startServerTLS starts a POP3S server with native TLS
func (p *POP3Server) startServerTLS(address string) error {
	listener, err := tls.Listen("tcp", address, p.tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to start POP3S server: %w", err)
	}
	p.listenerTLS = listener
	defer listener.Close()

	fmt.Printf("🚀 POP3S Server started on %s\n", address)

	for {
		select {
		case <-p.done:
			fmt.Printf("🛑 POP3S Server stopping...\n")
			return nil
		default:
			// Set a timeout for Accept to allow checking the done channel
			if tcpListener, ok := listener.(*net.TCPListener); ok {
				tcpListener.SetDeadline(time.Now().Add(1 * time.Second))
			}

			conn, err := listener.Accept()
			if err != nil {
				// Check if it's a timeout error and continue if server is not stopping
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				select {
				case <-p.done:
					return nil
				default:
					fmt.Printf("❌ POP3S Connection error: %v\n", err)
					continue
				}
			}

			go p.handleConnection(conn)
		}
	}
}

// Stop stops the POP3 server
func (p *POP3Server) Stop() {
	close(p.done)
	if p.listener != nil {
		p.listener.Close()
	}
	if p.listenerTLS != nil {
		p.listenerTLS.Close()
	}
}

// AddUser adds a user for POP3 authentication
func (p *POP3Server) AddUser(username, password string) {
	if p.users == nil {
		p.users = make(map[string]string)
	}
	p.users[username] = password
}

// handleConnection handles a POP3 client connection
func (p *POP3Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	session := &POP3Session{
		conn:          conn,
		reader:        bufio.NewReader(conn),
		authenticated: false,
		deletedEmails: make(map[string]bool),
		server:        p,
	}

	// Send greeting
	session.sendResponse("+OK POP3 server ready")

	// Handle commands
	for {
		conn.SetReadDeadline(time.Now().Add(10 * time.Minute))
		line, err := session.reader.ReadString('\n')
		if err != nil {
			return
		}

		command := strings.TrimSpace(line)
		if command == "" {
			continue
		}

		fmt.Printf("📨 [POP3] Command: %s\n", command)

		if !session.handleCommand(command) {
			break
		}
	}
}

// handleCommand processes POP3 commands
func (s *POP3Session) handleCommand(command string) bool {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		s.sendResponse("-ERR Invalid command")
		return true
	}

	cmd := strings.ToUpper(parts[0]) // Only convert the command to uppercase, not the parameters

	switch cmd {
	case "USER":
		return s.handleUSER(parts)
	case "PASS":
		return s.handlePASS(parts)
	case "STAT":
		return s.handleSTAT()
	case "LIST":
		return s.handleLIST(parts)
	case "RETR":
		return s.handleRETR(parts)
	case "DELE":
		return s.handleDELE(parts)
	case "NOOP":
		return s.handleNOOP()
	case "RSET":
		return s.handleRSET()
	case "QUIT":
		return s.handleQUIT()
	case "CAPA":
		return s.handleCAPA()
	case "UIDL":
		return s.handleUIDL(parts)
	case "TOP":
		return s.handleTOP(parts)
	default:
		s.sendResponse("-ERR Unknown command")
		return true
	}
}

// handleUSER handles the USER command
func (s *POP3Session) handleUSER(parts []string) bool {
	if len(parts) != 2 {
		s.sendResponse("-ERR Syntax: USER username")
		return true
	}

	s.username = parts[1]
	s.sendResponse("+OK User accepted")
	return true
}

// handlePASS handles the PASS command
func (s *POP3Session) handlePASS(parts []string) bool {
	if len(parts) != 2 {
		s.sendResponse("-ERR Syntax: PASS password")
		return true
	}

	if s.username == "" {
		s.sendResponse("-ERR USER command must come first")
		return true
	}

	password := parts[1]
	if storedPassword, exists := s.server.users[s.username]; exists && storedPassword == password {
		s.authenticated = true

		// Load emails for this user
		emails, err := s.server.emailStore.GetEmails(s.username)
		if err != nil {
			fmt.Printf("❌ Failed to load emails for %s: %v\n", s.username, err)
			s.emails = []*StoredEmail{} // Empty list on error
		} else {
			s.emails = emails
		}

		s.sendResponse(fmt.Sprintf("+OK Mailbox ready, %d messages", len(s.emails)))
		fmt.Printf("🔐 [POP3] User authenticated: %s (%d emails)\n", s.username, len(s.emails))
	} else {
		s.sendResponse("-ERR Authentication failed")
		fmt.Printf("❌ [POP3] Authentication failed for: %s\n", s.username)
	}
	return true
}

// handleSTAT handles the STAT command
func (s *POP3Session) handleSTAT() bool {
	if !s.authenticated {
		s.sendResponse("-ERR Not authenticated")
		return true
	}

	totalSize := 0
	count := 0
	for _, email := range s.emails {
		if !s.deletedEmails[email.ID] {
			totalSize += email.Size
			count++
		}
	}

	s.sendResponse(fmt.Sprintf("+OK %d %d", count, totalSize))
	return true
}

// handleLIST handles the LIST command
func (s *POP3Session) handleLIST(parts []string) bool {
	if !s.authenticated {
		s.sendResponse("-ERR Not authenticated")
		return true
	}

	if len(parts) == 1 {
		// LIST all messages
		count := 0
		for _, email := range s.emails {
			if !s.deletedEmails[email.ID] {
				count++
			}
		}

		s.sendResponse(fmt.Sprintf("+OK %d messages", count))

		for i, email := range s.emails {
			if !s.deletedEmails[email.ID] {
				s.sendLine(fmt.Sprintf("%d %d", i+1, email.Size))
			}
		}
		s.sendLine(".")
	} else if len(parts) == 2 {
		// LIST specific message
		msgNum, err := strconv.Atoi(parts[1])
		if err != nil || msgNum < 1 || msgNum > len(s.emails) {
			s.sendResponse("-ERR Invalid message number")
			return true
		}

		email := s.emails[msgNum-1]
		if s.deletedEmails[email.ID] {
			s.sendResponse("-ERR Message deleted")
			return true
		}

		s.sendResponse(fmt.Sprintf("+OK %d %d", msgNum, email.Size))
	}
	return true
}

// handleRETR handles the RETR command
func (s *POP3Session) handleRETR(parts []string) bool {
	if !s.authenticated {
		s.sendResponse("-ERR Not authenticated")
		return true
	}

	if len(parts) != 2 {
		s.sendResponse("-ERR Syntax: RETR message-number")
		return true
	}

	msgNum, err := strconv.Atoi(parts[1])
	if err != nil || msgNum < 1 || msgNum > len(s.emails) {
		s.sendResponse("-ERR Invalid message number")
		return true
	}

	email := s.emails[msgNum-1]
	if s.deletedEmails[email.ID] {
		s.sendResponse("-ERR Message deleted")
		return true
	}

	// Get full email content
	fullEmail, err := s.server.emailStore.GetEmailByID(s.username, email.ID)
	if err != nil {
		s.sendResponse("-ERR Failed to retrieve message")
		return true
	}

	s.sendResponse(fmt.Sprintf("+OK %d octets", fullEmail.Size))

	// Send headers
	if fullEmail.Headers != "" {
		s.sendLine(fullEmail.Headers)
	} else {
		// Basic headers if none stored
		s.sendLine(fmt.Sprintf("From: %s", fullEmail.From))
		s.sendLine(fmt.Sprintf("To: %s", fullEmail.To))
		s.sendLine(fmt.Sprintf("Subject: %s", fullEmail.Subject))
		s.sendLine(fmt.Sprintf("Date: %s", fullEmail.Date))
		s.sendLine("")
	}

	// Send body
	s.sendLine(fullEmail.Body)
	s.sendLine(".")

	return true
}

// handleDELE handles the DELE command
func (s *POP3Session) handleDELE(parts []string) bool {
	if !s.authenticated {
		s.sendResponse("-ERR Not authenticated")
		return true
	}

	if len(parts) != 2 {
		s.sendResponse("-ERR Syntax: DELE message-number")
		return true
	}

	msgNum, err := strconv.Atoi(parts[1])
	if err != nil || msgNum < 1 || msgNum > len(s.emails) {
		s.sendResponse("-ERR Invalid message number")
		return true
	}

	email := s.emails[msgNum-1]
	if s.deletedEmails[email.ID] {
		s.sendResponse("-ERR Message already deleted")
		return true
	}

	s.deletedEmails[email.ID] = true
	s.sendResponse(fmt.Sprintf("+OK Message %d deleted", msgNum))
	return true
}

// handleNOOP handles the NOOP command
func (s *POP3Session) handleNOOP() bool {
	if !s.authenticated {
		s.sendResponse("-ERR Not authenticated")
		return true
	}
	s.sendResponse("+OK")
	return true
}

// handleRSET handles the RSET command
func (s *POP3Session) handleRSET() bool {
	if !s.authenticated {
		s.sendResponse("-ERR Not authenticated")
		return true
	}

	s.deletedEmails = make(map[string]bool)
	s.sendResponse("+OK")
	return true
}

// handleQUIT handles the QUIT command
func (s *POP3Session) handleQUIT() bool {
	if s.authenticated {
		// Apply deletions
		for emailID := range s.deletedEmails {
			err := s.server.emailStore.DeleteEmail(s.username, emailID)
			if err != nil {
				fmt.Printf("❌ Failed to delete email %s: %v\n", emailID, err)
			}
		}
	}

	s.sendResponse("+OK POP3 server signing off")
	return false // Close connection
}

// handleCAPA handles the CAPA command
func (s *POP3Session) handleCAPA() bool {
	s.sendResponse("+OK Capability list follows")
	s.sendLine("TOP")
	s.sendLine("UIDL")
	s.sendLine("RESP-CODES")
	s.sendLine("AUTH-RESP-CODE")
	if s.server.tlsConfig != nil {
		s.sendLine("STLS")
	}
	s.sendLine(".")
	return true
}

// handleUIDL handles the UIDL command
func (s *POP3Session) handleUIDL(parts []string) bool {
	if !s.authenticated {
		s.sendResponse("-ERR Not authenticated")
		return true
	}

	if len(parts) == 1 {
		// UIDL all messages
		count := 0
		for _, email := range s.emails {
			if !s.deletedEmails[email.ID] {
				count++
			}
		}

		s.sendResponse(fmt.Sprintf("+OK %d messages", count))

		for i, email := range s.emails {
			if !s.deletedEmails[email.ID] {
				s.sendLine(fmt.Sprintf("%d %s", i+1, email.ID))
			}
		}
		s.sendLine(".")
	} else if len(parts) == 2 {
		// UIDL specific message
		msgNum, err := strconv.Atoi(parts[1])
		if err != nil || msgNum < 1 || msgNum > len(s.emails) {
			s.sendResponse("-ERR Invalid message number")
			return true
		}

		email := s.emails[msgNum-1]
		if s.deletedEmails[email.ID] {
			s.sendResponse("-ERR Message deleted")
			return true
		}

		s.sendResponse(fmt.Sprintf("+OK %d %s", msgNum, email.ID))
	}
	return true
}

// handleTOP handles the TOP command
func (s *POP3Session) handleTOP(parts []string) bool {
	if !s.authenticated {
		s.sendResponse("-ERR Not authenticated")
		return true
	}

	if len(parts) != 3 {
		s.sendResponse("-ERR Syntax: TOP message-number lines")
		return true
	}

	msgNum, err := strconv.Atoi(parts[1])
	if err != nil || msgNum < 1 || msgNum > len(s.emails) {
		s.sendResponse("-ERR Invalid message number")
		return true
	}

	lines, err := strconv.Atoi(parts[2])
	if err != nil || lines < 0 {
		s.sendResponse("-ERR Invalid line count")
		return true
	}

	email := s.emails[msgNum-1]
	if s.deletedEmails[email.ID] {
		s.sendResponse("-ERR Message deleted")
		return true
	}

	// Get full email content
	fullEmail, err := s.server.emailStore.GetEmailByID(s.username, email.ID)
	if err != nil {
		s.sendResponse("-ERR Failed to retrieve message")
		return true
	}

	s.sendResponse("+OK Top of message follows")

	// Send headers
	if fullEmail.Headers != "" {
		s.sendLine(fullEmail.Headers)
	} else {
		s.sendLine(fmt.Sprintf("From: %s", fullEmail.From))
		s.sendLine(fmt.Sprintf("To: %s", fullEmail.To))
		s.sendLine(fmt.Sprintf("Subject: %s", fullEmail.Subject))
		s.sendLine(fmt.Sprintf("Date: %s", fullEmail.Date))
		s.sendLine("")
	}

	// Send limited body lines
	bodyLines := strings.Split(fullEmail.Body, "\n")
	for i, line := range bodyLines {
		if i >= lines {
			break
		}
		s.sendLine(line)
	}
	s.sendLine(".")

	return true
}

// sendResponse sends a POP3 response
func (s *POP3Session) sendResponse(response string) {
	s.conn.Write([]byte(response + "\r\n"))
}

// sendLine sends a line (used for multi-line responses)
func (s *POP3Session) sendLine(line string) {
	s.conn.Write([]byte(line + "\r\n"))
}
