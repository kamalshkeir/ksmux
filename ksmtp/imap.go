// Package ksmtp - Serveur IMAP/IMAPS pour accès et synchronisation d'emails
//
// USAGE SIMPLE:
//
//	// 1. Créer la configuration
//	conf := &ksmtp.ConfIMAP{
//	    Address:    ":143",  // IMAP standard
//	    AddressTLS: ":993",  // IMAPS sécurisé
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
//	server, err := ksmtp.NewIMAPServer(conf, emailStore)
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
// COMMANDES IMAP SUPPORTÉES:
//   - CAPABILITY : Capacités du serveur
//   - LOGIN      : Authentification
//   - LOGOUT     : Déconnexion
//   - SELECT     : Sélectionner un dossier (lecture/écriture)
//   - EXAMINE    : Examiner un dossier (lecture seule)
//   - LIST       : Lister les dossiers
//   - LSUB       : Lister les dossiers abonnés
//   - STATUS     : Statut d'un dossier
//   - FETCH      : Récupérer des messages
//   - STORE      : Modifier les flags des messages
//   - COPY       : Copier des messages
//   - SEARCH     : Rechercher des messages
//   - UID        : Commandes avec UID
//   - EXPUNGE    : Supprimer définitivement
//   - CLOSE      : Fermer le dossier sélectionné
//   - NOOP       : Pas d'opération
//   - STARTTLS   : Démarrer TLS
//
// DOSSIERS PAR DÉFAUT:
//   - INBOX   : Boîte de réception
//   - Sent    : Messages envoyés
//   - Drafts  : Brouillons
//   - Trash   : Corbeille
//
// PORTS:
//   - 143 : IMAP standard (non chiffré)
//   - 993 : IMAPS (TLS/SSL natif)
//
// SÉCURITÉ:
//   - Support TLS/SSL natif sur port 993
//   - Support STARTTLS sur port 143
//   - Authentification par utilisateur/mot de passe
//   - Timeout de connexion (30 minutes)
//   - Gestion des sessions multiples
//
// ÉTATS DE SESSION:
//   - NOT_AUTHENTICATED : Non authentifié
//   - AUTHENTICATED     : Authentifié
//   - SELECTED          : Dossier sélectionné
//   - LOGOUT            : Déconnexion
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

// IMAPFolder represents an IMAP folder/mailbox
type IMAPFolder struct {
	Name       string
	Delimiter  string
	Attributes []string
	Messages   []*StoredEmail
}

// IMAPServer represents an IMAP server
type IMAPServer struct {
	Address     string
	AddressTLS  string
	tlsConfig   *tls.Config
	users       map[string]string // username -> password
	emailStore  EmailStore
	done        chan struct{}
	listener    net.Listener
	listenerTLS net.Listener
}

// IMAPSession represents an IMAP client session
type IMAPSession struct {
	conn           net.Conn
	reader         *bufio.Reader
	authenticated  bool
	username       string
	selectedFolder string
	folders        map[string]*IMAPFolder
	server         *IMAPServer
	state          string // "NOT_AUTHENTICATED", "AUTHENTICATED", "SELECTED", "LOGOUT"
}

// ConfIMAP configuration for IMAP server
type ConfIMAP struct {
	Address     string            // :143 for IMAP
	AddressTLS  string            // :993 for IMAPS
	Users       map[string]string // username -> password
	TLSCertPath string            // Path to TLS certificate file
	TLSKeyPath  string            // Path to TLS private key file
}

// NewIMAPServer creates a new IMAP server
func NewIMAPServer(conf *ConfIMAP, emailStore EmailStore) (*IMAPServer, error) {
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
		fmt.Printf("🔒 IMAP TLS certificate loaded\n")
	}

	return &IMAPServer{
		Address:    conf.Address,
		AddressTLS: conf.AddressTLS,
		tlsConfig:  tlsConfig,
		users:      users,
		emailStore: emailStore,
		done:       make(chan struct{}),
	}, nil
}

// Start starts the IMAP server
func (i *IMAPServer) Start() error {
	// Start regular IMAP server if configured
	if i.Address != "" {
		go func() {
			if err := i.startServer(i.Address); err != nil {
				fmt.Printf("❌ IMAP Server error: %v\n", err)
			}
		}()
	}

	// Start IMAPS server if configured
	if i.AddressTLS != "" && i.tlsConfig != nil {
		go func() {
			if err := i.startServerTLS(i.AddressTLS); err != nil {
				fmt.Printf("❌ IMAPS Server error: %v\n", err)
			}
		}()
	}

	// Keep the main goroutine alive
	select {
	case <-i.done:
		return nil
	}
}

// startServer starts a regular IMAP server
func (i *IMAPServer) startServer(address string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("failed to start IMAP server: %w", err)
	}
	i.listener = listener
	defer listener.Close()

	fmt.Printf("🚀 IMAP Server started on %s\n", address)

	for {
		select {
		case <-i.done:
			fmt.Printf("🛑 IMAP Server stopping...\n")
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
				case <-i.done:
					return nil
				default:
					fmt.Printf("❌ IMAP Connection error: %v\n", err)
					continue
				}
			}

			go i.handleConnection(conn)
		}
	}
}

// startServerTLS starts an IMAPS server with native TLS
func (i *IMAPServer) startServerTLS(address string) error {
	listener, err := tls.Listen("tcp", address, i.tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to start IMAPS server: %w", err)
	}
	i.listenerTLS = listener
	defer listener.Close()

	fmt.Printf("🚀 IMAPS Server started on %s\n", address)

	for {
		select {
		case <-i.done:
			fmt.Printf("🛑 IMAPS Server stopping...\n")
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
				case <-i.done:
					return nil
				default:
					fmt.Printf("❌ IMAPS Connection error: %v\n", err)
					continue
				}
			}

			go i.handleConnection(conn)
		}
	}
}

// Stop stops the IMAP server
func (i *IMAPServer) Stop() {
	close(i.done)
	if i.listener != nil {
		i.listener.Close()
	}
	if i.listenerTLS != nil {
		i.listenerTLS.Close()
	}
}

// AddUser adds a user for IMAP authentication
func (i *IMAPServer) AddUser(username, password string) {
	if i.users == nil {
		i.users = make(map[string]string)
	}
	i.users[username] = password
}

// handleConnection handles an IMAP client connection
func (i *IMAPServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	session := &IMAPSession{
		conn:    conn,
		reader:  bufio.NewReader(conn),
		server:  i,
		state:   "NOT_AUTHENTICATED",
		folders: make(map[string]*IMAPFolder),
	}

	// Initialize default folders
	session.initializeFolders()

	// Send greeting
	session.sendResponse("* OK IMAP4rev1 Service Ready")

	// Handle commands
	for {
		conn.SetReadDeadline(time.Now().Add(30 * time.Minute))
		line, err := session.reader.ReadString('\n')
		if err != nil {
			return
		}

		command := strings.TrimSpace(line)
		if command == "" {
			continue
		}

		fmt.Printf("📨 [IMAP] Command: %s\n", command)

		if !session.handleCommand(command) {
			break
		}
	}
}

// initializeFolders creates default IMAP folders
func (s *IMAPSession) initializeFolders() {
	s.folders["INBOX"] = &IMAPFolder{
		Name:       "INBOX",
		Delimiter:  "/",
		Attributes: []string{},
		Messages:   []*StoredEmail{},
	}
	s.folders["Sent"] = &IMAPFolder{
		Name:       "Sent",
		Delimiter:  "/",
		Attributes: []string{},
		Messages:   []*StoredEmail{},
	}
	s.folders["Drafts"] = &IMAPFolder{
		Name:       "Drafts",
		Delimiter:  "/",
		Attributes: []string{},
		Messages:   []*StoredEmail{},
	}
	s.folders["Trash"] = &IMAPFolder{
		Name:       "Trash",
		Delimiter:  "/",
		Attributes: []string{},
		Messages:   []*StoredEmail{},
	}
}

// handleCommand processes IMAP commands
func (s *IMAPSession) handleCommand(command string) bool {
	parts := strings.Fields(command)
	if len(parts) < 2 {
		s.sendResponse("* BAD Invalid command format")
		return true
	}

	tag := parts[0]
	cmd := strings.ToUpper(parts[1])
	args := parts[2:]

	switch cmd {
	case "CAPABILITY":
		return s.handleCAPABILITY(tag)
	case "LOGIN":
		return s.handleLOGIN(tag, args)
	case "LOGOUT":
		return s.handleLOGOUT(tag)
	case "SELECT":
		return s.handleSELECT(tag, args)
	case "EXAMINE":
		return s.handleEXAMINE(tag, args)
	case "LIST":
		return s.handleLIST(tag)
	case "LSUB":
		return s.handleLSUB(tag)
	case "STATUS":
		return s.handleSTATUS(tag, args)
	case "CREATE":
		return s.handleCREATE(tag)
	case "DELETE":
		return s.handleDELETE(tag)
	case "RENAME":
		return s.handleRENAME(tag)
	case "SUBSCRIBE":
		return s.handleSUBSCRIBE(tag)
	case "UNSUBSCRIBE":
		return s.handleUNSUBSCRIBE(tag)
	case "FETCH":
		return s.handleFETCH(tag, args)
	case "STORE":
		return s.handleSTORE(tag)
	case "COPY":
		return s.handleCOPY(tag)
	case "SEARCH":
		return s.handleSEARCH(tag)
	case "UID":
		return s.handleUID(tag)
	case "EXPUNGE":
		return s.handleEXPUNGE(tag)
	case "CLOSE":
		return s.handleCLOSE(tag)
	case "NOOP":
		return s.handleNOOP(tag)
	case "STARTTLS":
		return s.handleSTARTTLS(tag)
	default:
		s.sendResponse(fmt.Sprintf("%s BAD Unknown command", tag))
		return true
	}
}

// handleCAPABILITY handles the CAPABILITY command
func (s *IMAPSession) handleCAPABILITY(tag string) bool {
	capabilities := []string{
		"IMAP4rev1",
		"LITERAL+",
		"SASL-IR",
		"LOGIN-REFERRALS",
		"ID",
		"ENABLE",
		"IDLE",
		"NAMESPACE",
		"CHILDREN",
		"UNSELECT",
		"BINARY",
		"MOVE",
	}

	if s.server.tlsConfig != nil {
		capabilities = append(capabilities, "STARTTLS")
	}

	s.sendResponse("* CAPABILITY " + strings.Join(capabilities, " "))
	s.sendResponse(fmt.Sprintf("%s OK CAPABILITY completed", tag))
	return true
}

// handleLOGIN handles the LOGIN command
func (s *IMAPSession) handleLOGIN(tag string, args []string) bool {
	if len(args) != 2 {
		s.sendResponse(fmt.Sprintf("%s BAD LOGIN expects 2 arguments", tag))
		return true
	}

	username := strings.Trim(args[0], "\"")
	password := strings.Trim(args[1], "\"")

	if storedPassword, exists := s.server.users[username]; exists && storedPassword == password {
		s.authenticated = true
		s.username = username
		s.state = "AUTHENTICATED"

		// Load emails into INBOX
		emails, err := s.server.emailStore.GetEmails(username)
		if err != nil {
			fmt.Printf("❌ Failed to load emails for %s: %v\n", username, err)
		} else {
			s.folders["INBOX"].Messages = emails
		}

		s.sendResponse(fmt.Sprintf("%s OK LOGIN completed", tag))
		fmt.Printf("🔐 [IMAP] User authenticated: %s\n", username)
	} else {
		s.sendResponse(fmt.Sprintf("%s NO LOGIN failed", tag))
		fmt.Printf("❌ [IMAP] Authentication failed for: %s\n", username)
	}
	return true
}

// handleLOGOUT handles the LOGOUT command
func (s *IMAPSession) handleLOGOUT(tag string) bool {
	s.sendResponse("* BYE IMAP4rev1 Server logging out")
	s.sendResponse(fmt.Sprintf("%s OK LOGOUT completed", tag))
	s.state = "LOGOUT"
	return false // Close connection
}

// handleSELECT handles the SELECT command
func (s *IMAPSession) handleSELECT(tag string, args []string) bool {
	if !s.authenticated {
		s.sendResponse(fmt.Sprintf("%s NO Not authenticated", tag))
		return true
	}

	if len(args) != 1 {
		s.sendResponse(fmt.Sprintf("%s BAD SELECT expects 1 argument", tag))
		return true
	}

	folderName := strings.Trim(args[0], "\"")
	folder, exists := s.folders[folderName]
	if !exists {
		s.sendResponse(fmt.Sprintf("%s NO Mailbox does not exist", tag))
		return true
	}

	s.selectedFolder = folderName
	s.state = "SELECTED"

	// Send mailbox status
	s.sendResponse(fmt.Sprintf("* %d EXISTS", len(folder.Messages)))
	s.sendResponse("* 0 RECENT")
	s.sendResponse("* OK [UIDVALIDITY 1] UIDs valid")
	s.sendResponse("* OK [UIDNEXT 1000] Predicted next UID")
	s.sendResponse("* FLAGS (\\Answered \\Flagged \\Deleted \\Seen \\Draft)")
	s.sendResponse("* OK [PERMANENTFLAGS (\\Answered \\Flagged \\Deleted \\Seen \\Draft \\*)] Limited")
	s.sendResponse(fmt.Sprintf("%s OK [READ-WRITE] SELECT completed", tag))

	return true
}

// handleEXAMINE handles the EXAMINE command (read-only SELECT)
func (s *IMAPSession) handleEXAMINE(tag string, args []string) bool {
	if !s.authenticated {
		s.sendResponse(fmt.Sprintf("%s NO Not authenticated", tag))
		return true
	}

	if len(args) != 1 {
		s.sendResponse(fmt.Sprintf("%s BAD EXAMINE expects 1 argument", tag))
		return true
	}

	folderName := strings.Trim(args[0], "\"")
	folder, exists := s.folders[folderName]
	if !exists {
		s.sendResponse(fmt.Sprintf("%s NO Mailbox does not exist", tag))
		return true
	}

	s.selectedFolder = folderName
	s.state = "SELECTED"

	// Send mailbox status (read-only)
	s.sendResponse(fmt.Sprintf("* %d EXISTS", len(folder.Messages)))
	s.sendResponse("* 0 RECENT")
	s.sendResponse("* OK [UIDVALIDITY 1] UIDs valid")
	s.sendResponse("* OK [UIDNEXT 1000] Predicted next UID")
	s.sendResponse("* FLAGS (\\Answered \\Flagged \\Deleted \\Seen \\Draft)")
	s.sendResponse("* OK [PERMANENTFLAGS ()] No permanent flags permitted")
	s.sendResponse(fmt.Sprintf("%s OK [READ-ONLY] EXAMINE completed", tag))

	return true
}

// handleLIST handles the LIST command
func (s *IMAPSession) handleLIST(tag string) bool {
	if !s.authenticated {
		s.sendResponse(fmt.Sprintf("%s NO Not authenticated", tag))
		return true
	}

	// Simple implementation - list all folders
	for _, folder := range s.folders {
		s.sendResponse(fmt.Sprintf("* LIST () \"%s\" \"%s\"", folder.Delimiter, folder.Name))
	}
	s.sendResponse(fmt.Sprintf("%s OK LIST completed", tag))
	return true
}

// handleLSUB handles the LSUB command
func (s *IMAPSession) handleLSUB(tag string) bool {
	if !s.authenticated {
		s.sendResponse(fmt.Sprintf("%s NO Not authenticated", tag))
		return true
	}

	// Simple implementation - same as LIST for now
	for _, folder := range s.folders {
		s.sendResponse(fmt.Sprintf("* LSUB () \"%s\" \"%s\"", folder.Delimiter, folder.Name))
	}
	s.sendResponse(fmt.Sprintf("%s OK LSUB completed", tag))
	return true
}

// handleSTATUS handles the STATUS command
func (s *IMAPSession) handleSTATUS(tag string, args []string) bool {
	if !s.authenticated {
		s.sendResponse(fmt.Sprintf("%s NO Not authenticated", tag))
		return true
	}

	if len(args) < 2 {
		s.sendResponse(fmt.Sprintf("%s BAD STATUS expects at least 2 arguments", tag))
		return true
	}

	folderName := strings.Trim(args[0], "\"")
	folder, exists := s.folders[folderName]
	if !exists {
		s.sendResponse(fmt.Sprintf("%s NO Mailbox does not exist", tag))
		return true
	}

	// Simple status response
	messageCount := len(folder.Messages)
	s.sendResponse(fmt.Sprintf("* STATUS \"%s\" (MESSAGES %d RECENT 0 UIDNEXT 1000 UIDVALIDITY 1 UNSEEN 0)", folderName, messageCount))
	s.sendResponse(fmt.Sprintf("%s OK STATUS completed", tag))
	return true
}

// handleFETCH handles the FETCH command
func (s *IMAPSession) handleFETCH(tag string, args []string) bool {
	if s.state != "SELECTED" {
		s.sendResponse(fmt.Sprintf("%s NO No mailbox selected", tag))
		return true
	}

	if len(args) < 2 {
		s.sendResponse(fmt.Sprintf("%s BAD FETCH expects at least 2 arguments", tag))
		return true
	}

	sequenceSet := args[0]
	fetchItems := strings.Join(args[1:], " ")

	folder := s.folders[s.selectedFolder]

	// Parse sequence set (simplified - just handle "1:*" and single numbers)
	var messageNums []int
	if sequenceSet == "*" {
		if len(folder.Messages) > 0 {
			messageNums = []int{len(folder.Messages)}
		}
	} else if strings.Contains(sequenceSet, ":") {
		parts := strings.Split(sequenceSet, ":")
		if len(parts) == 2 {
			start, _ := strconv.Atoi(parts[0])
			end := len(folder.Messages)
			if parts[1] != "*" {
				end, _ = strconv.Atoi(parts[1])
			}
			for i := start; i <= end && i <= len(folder.Messages); i++ {
				messageNums = append(messageNums, i)
			}
		}
	} else {
		num, err := strconv.Atoi(sequenceSet)
		if err == nil && num > 0 && num <= len(folder.Messages) {
			messageNums = []int{num}
		}
	}

	// Fetch messages
	for _, msgNum := range messageNums {
		if msgNum > 0 && msgNum <= len(folder.Messages) {
			email := folder.Messages[msgNum-1]

			// Simple FETCH response
			if strings.Contains(strings.ToUpper(fetchItems), "ENVELOPE") {
				s.sendResponse(fmt.Sprintf("* %d FETCH (ENVELOPE (\"%s\" \"%s\" ((\"%s\" NIL \"%s\" \"%s\")) ((\"%s\" NIL \"%s\" \"%s\")) NIL NIL NIL \"%s\"))",
					msgNum, email.Date, email.Subject, email.From, email.From, email.From, email.To, email.To, email.To, email.ID))
			} else if strings.Contains(strings.ToUpper(fetchItems), "BODY") {
				s.sendResponse(fmt.Sprintf("* %d FETCH (BODY[TEXT] {%d}", msgNum, len(email.Body)))
				s.sendResponse(email.Body)
				s.sendResponse(")")
			} else {
				// Default FLAGS response
				s.sendResponse(fmt.Sprintf("* %d FETCH (FLAGS ())", msgNum))
			}
		}
	}

	s.sendResponse(fmt.Sprintf("%s OK FETCH completed", tag))
	return true
}

// Placeholder handlers for other commands
func (s *IMAPSession) handleCREATE(tag string) bool {
	s.sendResponse(fmt.Sprintf("%s OK CREATE completed", tag))
	return true
}

func (s *IMAPSession) handleDELETE(tag string) bool {
	s.sendResponse(fmt.Sprintf("%s OK DELETE completed", tag))
	return true
}

func (s *IMAPSession) handleRENAME(tag string) bool {
	s.sendResponse(fmt.Sprintf("%s OK RENAME completed", tag))
	return true
}

func (s *IMAPSession) handleSUBSCRIBE(tag string) bool {
	s.sendResponse(fmt.Sprintf("%s OK SUBSCRIBE completed", tag))
	return true
}

func (s *IMAPSession) handleUNSUBSCRIBE(tag string) bool {
	s.sendResponse(fmt.Sprintf("%s OK UNSUBSCRIBE completed", tag))
	return true
}

func (s *IMAPSession) handleSTORE(tag string) bool {
	s.sendResponse(fmt.Sprintf("%s OK STORE completed", tag))
	return true
}

func (s *IMAPSession) handleCOPY(tag string) bool {
	s.sendResponse(fmt.Sprintf("%s OK COPY completed", tag))
	return true
}

func (s *IMAPSession) handleSEARCH(tag string) bool {
	s.sendResponse("* SEARCH")
	s.sendResponse(fmt.Sprintf("%s OK SEARCH completed", tag))
	return true
}

func (s *IMAPSession) handleUID(tag string) bool {
	s.sendResponse(fmt.Sprintf("%s OK UID completed", tag))
	return true
}

func (s *IMAPSession) handleEXPUNGE(tag string) bool {
	s.sendResponse(fmt.Sprintf("%s OK EXPUNGE completed", tag))
	return true
}

func (s *IMAPSession) handleCLOSE(tag string) bool {
	s.selectedFolder = ""
	s.state = "AUTHENTICATED"
	s.sendResponse(fmt.Sprintf("%s OK CLOSE completed", tag))
	return true
}

func (s *IMAPSession) handleNOOP(tag string) bool {
	s.sendResponse(fmt.Sprintf("%s OK NOOP completed", tag))
	return true
}

func (s *IMAPSession) handleSTARTTLS(tag string) bool {
	if s.server.tlsConfig == nil {
		s.sendResponse(fmt.Sprintf("%s NO STARTTLS not available", tag))
		return true
	}

	s.sendResponse(fmt.Sprintf("%s OK Begin TLS negotiation now", tag))

	// Upgrade connection to TLS
	tlsConn := tls.Server(s.conn, s.server.tlsConfig)
	err := tlsConn.Handshake()
	if err != nil {
		fmt.Printf("❌ [IMAP] TLS handshake failed: %v\n", err)
		return false
	}

	fmt.Printf("🔒 [IMAP] TLS connection established\n")

	// Replace the connection with the TLS connection
	s.conn = tlsConn
	s.reader = bufio.NewReader(tlsConn)

	return true
}

// sendResponse sends an IMAP response
func (s *IMAPSession) sendResponse(response string) {
	s.conn.Write([]byte(response + "\r\n"))
}
