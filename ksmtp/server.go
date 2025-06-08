// Package ksmtp - Serveur SMTP complet avec DKIM, templates et anti-spam
//
// USAGE SIMPLE:
//
//	// 1. Créer la configuration
//	conf := &ksmtp.ConfServer{
//	    Domain:    "example.com",
//	    Subdomain: "mail.example.com",
//	    IPv4:      "203.0.113.10",
//	    IPv6:      "2001:db8::1",
//	    Address:   ":25",
//	    Users: map[string]string{
//	        "admin": "password123",
//	        "user":  "mypassword",
//	    },
//	    RequireAuthForExternal: true,  // Auth pour relay
//	    RequireAuthForLocal:    false, // Pas d'auth pour réception
//	    TLSCertPath: "/path/to/cert.pem", // Optionnel
//	    TLSKeyPath:  "/path/to/key.pem",  // Optionnel
//	}
//
//	// 2. Créer le serveur
//	server, err := ksmtp.NewSmtpServer(conf)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// 3. Configurer les callbacks
//	server.OnRecv(func(email *ksmtp.Email) {
//	    fmt.Printf("Email reçu: %s -> %s\n", email.From, email.To)
//	})
//
//	// 4. Enregistrer des templates
//	server.RegisterTemplate("welcome",
//	    "Bienvenue {{.Name}}!",
//	    "Bonjour {{.Name}}, bienvenue!",
//	    "<h1>Bonjour {{.Name}}</h1><p>Bienvenue!</p>")
//
//	// 5. Démarrer le serveur
//	go server.Start()
//
//	// 6. Envoyer des emails
//	email := &ksmtp.Email{
//	    From:    "admin@example.com",
//	    To:      "user@gmail.com",
//	    Subject: "Test",
//	    BodyTXT: "Hello World!",
//	}
//	server.SendEmail(email)
//
//	// 7. Utiliser les templates
//	data := map[string]interface{}{"Name": "John"}
//	server.SendTemplate("welcome", "admin@example.com", "user@gmail.com", data)
//
//	// 8. Gérer l'anti-spam
//	server.AddSpamBlacklistIP("203.0.113.100")
//	server.SetSpamFilterEnabled(true)
//	server.PrintSpamFilterStats()
//
// FONCTIONNALITÉS:
//   - Envoi/réception d'emails avec MIME
//   - Signature DKIM automatique
//   - Templates d'emails avec variables
//   - Filtrage anti-spam intégré
//   - Support TLS/STARTTLS
//   - Authentification SMTP (LOGIN/PLAIN)
//   - Relay intelligent avec MX lookup
//   - Gestion des pièces jointes
//   - Configuration DNS automatique
//
// COMMANDES SMTP SUPPORTÉES:
//   - EHLO/HELO  : Identification
//   - AUTH       : Authentification (LOGIN/PLAIN)
//   - MAIL FROM  : Expéditeur
//   - RCPT TO    : Destinataire
//   - DATA       : Contenu de l'email
//   - STARTTLS   : Chiffrement TLS
//   - RSET       : Reset de session
//   - QUIT       : Fermeture
//   - NOOP       : Pas d'opération
//
// SÉCURITÉ:
//   - Signature DKIM automatique
//   - Filtrage anti-spam multicritères
//   - Support TLS/SSL complet
//   - Authentification requise pour relay
//   - Rate limiting par IP
//   - Validation des commandes SMTP
//   - Timeouts de sécurité
//
// DNS REQUIS:
//   - MX Record : mail.example.com
//   - A/AAAA    : Adresses IP du serveur
//   - SPF       : Autorisation d'envoi
//   - DKIM      : Signature cryptographique
//   - DMARC     : Politique de sécurité
//   - PTR       : Reverse DNS (important!)
package ksmtp

import (
	"bufio"
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"regexp"
	"strings"
	"text/template"
	"time"
)

var (
	MaxEmailSize = 20 * 1024 * 1024
)

const (
	dkimSelector = "ksmtp"
	dkimKeyFile  = "dkim_private.key"
)

// MX Record (pour recevoir)
// A Record pour mail. (pas de sous-domaine 'mail')
// AAAA Record pour mail.

// EmailTemplate represents a template for emails
type EmailTemplate struct {
	Name     string
	Subject  string
	BodyTXT  string
	BodyHTML string
}

type SmtpServer struct {
	Domain    string
	Subdomain string
	Address   string
	IPv4      string
	IPv6      string
	onRecv    func(*Email)
	done      chan struct{}
	listener  net.Listener
	// Authentication
	users               map[string]string // username -> password
	requireAuthForRelay bool
	requireAuthForLocal bool
	// Templates
	templates map[string]*EmailTemplate
	// TLS Configuration
	tlsConfig *tls.Config
	// Anti-spam
	spamFilter *SpamFilter
}

type ConfServer struct {
	Domain    string // example.com
	Subdomain string // mail.example.com
	Address   string // localhost:25/587
	IPv4      string
	IPv6      string
	// Authentication
	Users                  map[string]string // username -> password
	RequireAuthForExternal bool              // Require auth for sending to external domains
	RequireAuthForLocal    bool              // Don't require auth for receiving emails
	// TLS Configuration
	TLSCertPath string // Path to TLS certificate file
	TLSKeyPath  string // Path to TLS private key file
}

func NewSmtpServer(conf *ConfServer) (*SmtpServer, error) {
	if conf == nil {
		return nil, fmt.Errorf("no conf provided")
	}

	if conf.IPv4 == "" {
		return nil, fmt.Errorf("ipv4 is required: ex:178.12.52.12")
	}
	if conf.Subdomain != "" {
		sp := strings.Split(conf.Subdomain, ".")
		if len(sp) < 3 {
			return nil, fmt.Errorf("bad subdomain")
		}
	}

	if conf.Domain == "" {
		sp := strings.Split(conf.Subdomain, ".")
		conf.Domain = sp[len(sp)-2] + "." + sp[len(sp)-1]
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
			ServerName:   conf.Subdomain,
		}
		fmt.Printf("🔒 TLS certificate loaded for STARTTLS support\n")
	}

	// Initialize or load DKIM key
	err := initializeDKIMKey()
	if err != nil {
		fmt.Printf("⚠️ DKIM key initialization failed: %v\n", err)
	}

	srv := &SmtpServer{
		Domain:              conf.Domain,
		Subdomain:           conf.Subdomain,
		Address:             conf.Address,
		IPv4:                conf.IPv4,
		IPv6:                conf.IPv6,
		done:                make(chan struct{}),
		users:               users,
		requireAuthForRelay: conf.RequireAuthForExternal,
		requireAuthForLocal: conf.RequireAuthForLocal,
		templates:           make(map[string]*EmailTemplate),
		tlsConfig:           tlsConfig,
		spamFilter:          NewSpamFilter(),
	}

	return srv, nil
}

func (ss *SmtpServer) GetTLSConfig() *tls.Config {
	return ss.tlsConfig
}

func (ss *SmtpServer) Start() {
	listener, err := net.Listen("tcp", ss.Address)
	if err != nil {
		log.Printf("❌ Failed to start server: %v\n", err)
		return
	}
	ss.listener = listener
	defer listener.Close()

	for {
		select {
		case <-ss.done:
			log.Printf("🛑 SMTP Server stopping...")
			return
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
				case <-ss.done:
					return
				default:
					log.Printf("❌ Connection error: %v\n", err)
					continue
				}
			}

			go ss.handleSMTPConnection(conn)
		}
	}
}

func (ss *SmtpServer) Stop() {
	close(ss.done)
	if ss.listener != nil {
		ss.listener.Close()
	}
}

func (ss *SmtpServer) OnRecv(fn func(*Email)) {
	ss.onRecv = fn
}

func (ss *SmtpServer) PrintDNSConfiguration() {
	fmt.Println()
	fmt.Println("🚀 KSMTP - DNS Configuration Helper 🚀")
	fmt.Println(strings.Repeat("-", 80))
	fmt.Println("Voici la configuration DNS requise pour que votre serveur mail fonctionne.")
	fmt.Println("Copiez-collez ces valeurs dans votre panneau de contrôle (DNS Manager):")
	fmt.Println(strings.Repeat("-", 80))
	fmt.Println()

	sub := strings.TrimSuffix(ss.Subdomain, ".")
	// A Record
	fmt.Println("✅ 1.1. A Record (Adresse IPv4)")
	fmt.Printf("   - Type:      A\n")
	fmt.Printf("   - Hostname:  %s\n", sub)
	fmt.Printf("   - IP Address: %s\n", ss.IPv4)
	fmt.Println()

	fmt.Println("✅ 1.2. AAAA Record (Adresse IPv6)")
	fmt.Printf("   - Type:      AAAA\n")
	fmt.Printf("   - Hostname:  %s\n", sub)
	fmt.Printf("   - IP Address: %s\n", ss.IPv6)
	fmt.Println()

	fmt.Println("✅ 2. SPF Record (Autorisation d'envoi)")
	fmt.Printf("   - Type:      TXT\n")
	fmt.Printf("   - Hostname:  %s\n", ss.Domain)
	fmt.Printf("   - Value:     \"v=spf1 ip4:%s ip6:%s ~all\"\n", ss.IPv4, ss.IPv6)
	fmt.Println()

	dkimPrivateKey, err := loadDKIMPrivateKey()
	if err != nil {
		fmt.Printf("❌ ERREUR DKIM: %v\n", err)
	} else {
		publicKey, err := getDKIMPublicKey(dkimPrivateKey)
		if err != nil {
			fmt.Println("❌ ERREUR DKIM:", err)
		} else {
			fmt.Println("✅ 3. DKIM Record (Signature anti-spam)")
			fmt.Printf("   - Type:      TXT\n")
			fmt.Printf("   - Hostname:  %s._domainkey\n", dkimSelector)
			fmt.Printf("   - Value:     \"v=DKIM1; k=rsa; p=%s\"\n", publicKey)
			fmt.Println()
		}
	}

	fmt.Println("✅ 4. DMARC Record (Politique de sécurité)")
	fmt.Printf("   - Type:      TXT\n")
	fmt.Printf("   - Hostname:  _dmarc\n")
	fmt.Printf("   - Value:     \"v=DMARC1; p=quarantine; rua=mailto:dmarc@%s\"\n", ss.Domain)
	fmt.Println(strings.Repeat("-", 80))
	fmt.Println()
	if ss.listener != nil {
		fmt.Println("✅ 5. MX Record (Pour recevoir les emails)")
		fmt.Printf("   - Type:      MX\n")
		fmt.Printf("   - Mail Server: %s\n", ss.Subdomain)
		fmt.Printf("   - LEAVE SUBDOMAIN EMPTY\n")
		fmt.Printf("   - Priority:  10\n")
		fmt.Println()
	}

	fmt.Println("🔥 ACTION REQUISE - REVERSE DNS (PTR) 🔥")
	fmt.Println("1. Allez dans l'onglet 'Networking' de votre Hebergeur.")
	fmt.Println("2. Trouvez votre adresse IPv6 et cliquez sur 'Edit RDNS'.")
	fmt.Println("3. Entrez '" + ss.Subdomain + "' comme Reverse DNS et sauvegardez.")
	fmt.Println(strings.Repeat("-", 80))
}

func (ss *SmtpServer) ParseRawEmail(from, data string) *Email {
	reader := strings.NewReader(data)
	msg, err := mail.ReadMessage(reader)
	if err != nil {
		return &Email{
			FailedRaw: data,
		}
	}

	var headers strings.Builder
	for key, values := range msg.Header {
		headers.WriteString(fmt.Sprintf("%s: %s\n", key, strings.Join(values, ", ")))
	}

	// --- Process body and attachments ---
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		fmt.Println("❌ Erreur parse email media type:", from)
		body, _ := io.ReadAll(msg.Body)
		return &Email{
			BodyTXT: string(body),
		}
	}

	pe := &Email{
		From:    msg.Header.Get("From"),
		To:      msg.Header.Get("To"),
		CC:      msg.Header.Get("Cc"),
		Subject: msg.Header.Get("Subject"),
		Date:    msg.Header.Get("Date"),
		Headers: headers.String(),
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(msg.Body, params["boundary"])
		partIndex := 0
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				fmt.Printf("❌ Erreur de lecture de la partie multipart: %v\n", err)
				continue
			}
			partMediaType, partParams, err := mime.ParseMediaType(p.Header.Get("Content-Type"))
			if err != nil {
				fmt.Printf("❌ Erreur de parsing du Content-Type de la partie: %v\n", err)
				continue
			}
			disposition, dispParams, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
			filename := dispParams["filename"]

			if disposition == "attachment" || (disposition == "inline" && filename != "") {
				if filename == "" {
					filename = fmt.Sprintf("attachment-part-%d", partIndex)
				}

				// Sécuriser le nom de fichier
				filename = sanitizeFilename(filename)
				attachmentFilename := fmt.Sprintf("mail-%d-%s", time.Now().UnixNano(), filename)

				// Vérifier l'encodage et décoder si nécessaire
				encoding := p.Header.Get("Content-Transfer-Encoding")
				var rrr io.Reader = p
				if strings.ToLower(encoding) == "base64" {
					rrr = base64.NewDecoder(base64.StdEncoding, p)
				}
				data, err := io.ReadAll(rrr)
				if err != nil {
					continue
				}
				pe.Files = append(pe.Files, FileInfo{
					Filename: attachmentFilename,
					Filedata: data,
				})
			} else if strings.HasPrefix(partMediaType, "multipart/alternative") {
				subReader := multipart.NewReader(p, partParams["boundary"])
				for {
					subPart, err := subReader.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						continue
					}
					subPartMediaType, _, _ := mime.ParseMediaType(subPart.Header.Get("Content-Type"))
					if subPartMediaType == "text/plain" {
						body, _ := io.ReadAll(subPart)
						pe.BodyTXT = string(body)
					} else if subPartMediaType == "text/html" {
						body, _ := io.ReadAll(subPart)
						pe.BodyHTML = string(body)
					}
				}
			} else if partMediaType == "text/plain" {
				body, _ := io.ReadAll(p)
				pe.BodyTXT = string(body)
			} else if partMediaType == "text/html" {
				body, _ := io.ReadAll(p)
				pe.BodyHTML = string(body)
			}
			partIndex++
		}
	} else {
		body, _ := io.ReadAll(msg.Body)
		if mediaType == "text/html" {
			pe.BodyHTML = string(body)
		} else {
			pe.BodyTXT = string(body)
		}
	}

	return pe
}

func (ss *SmtpServer) SendEmail(email *Email) error {
	if email == nil {
		return fmt.Errorf("email cannot be nil")
	}

	if email.To == "" {
		return fmt.Errorf("recipient email is required")
	}

	if email.From == "" {
		return fmt.Errorf("sender email is required")
	}

	// Build the raw email data
	var emailData strings.Builder

	// Headers
	emailData.WriteString(fmt.Sprintf("From: %s\r\n", email.From))
	emailData.WriteString(fmt.Sprintf("To: %s\r\n", email.To))
	if email.CC != "" {
		emailData.WriteString(fmt.Sprintf("Cc: %s\r\n", email.CC))
	}
	if email.Subject != "" {
		emailData.WriteString(fmt.Sprintf("Subject: %s\r\n", email.Subject))
	}
	if email.Date != "" {
		emailData.WriteString(fmt.Sprintf("Date: %s\r\n", email.Date))
	} else {
		emailData.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	}

	// MIME headers for multipart if we have HTML, text, or attachments
	hasHTML := email.BodyHTML != ""
	hasText := email.BodyTXT != ""
	hasAttachments := len(email.Files) > 0

	if hasHTML || hasText || hasAttachments {
		boundary := fmt.Sprintf("boundary_%d", time.Now().UnixNano())
		emailData.WriteString("MIME-Version: 1.0\r\n")
		emailData.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary))
		emailData.WriteString("\r\n")

		// Text/HTML parts
		if hasHTML && hasText {
			// Alternative boundary for text/html
			altBoundary := fmt.Sprintf("alt_boundary_%d", time.Now().UnixNano())
			emailData.WriteString(fmt.Sprintf("--%s\r\n", boundary))
			emailData.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n", altBoundary))
			emailData.WriteString("\r\n")

			// Text part
			emailData.WriteString(fmt.Sprintf("--%s\r\n", altBoundary))
			emailData.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
			emailData.WriteString("\r\n")
			emailData.WriteString(email.BodyTXT)
			emailData.WriteString("\r\n")

			// HTML part
			emailData.WriteString(fmt.Sprintf("--%s\r\n", altBoundary))
			emailData.WriteString("Content-Type: text/html; charset=utf-8\r\n")
			emailData.WriteString("\r\n")
			emailData.WriteString(email.BodyHTML)
			emailData.WriteString("\r\n")

			emailData.WriteString(fmt.Sprintf("--%s--\r\n", altBoundary))
		} else if hasHTML {
			emailData.WriteString(fmt.Sprintf("--%s\r\n", boundary))
			emailData.WriteString("Content-Type: text/html; charset=utf-8\r\n")
			emailData.WriteString("\r\n")
			emailData.WriteString(email.BodyHTML)
			emailData.WriteString("\r\n")
		} else if hasText {
			emailData.WriteString(fmt.Sprintf("--%s\r\n", boundary))
			emailData.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
			emailData.WriteString("\r\n")
			emailData.WriteString(email.BodyTXT)
			emailData.WriteString("\r\n")
		}

		// Attachments
		for _, file := range email.Files {
			emailData.WriteString(fmt.Sprintf("--%s\r\n", boundary))
			emailData.WriteString(fmt.Sprintf("Content-Type: application/octet-stream\r\n"))
			emailData.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", file.Filename))
			emailData.WriteString("Content-Transfer-Encoding: base64\r\n")
			emailData.WriteString("\r\n")

			// Encode file data in base64
			encoded := base64.StdEncoding.EncodeToString(file.Filedata)
			// Split into 76-character lines as per RFC
			for i := 0; i < len(encoded); i += 76 {
				end := i + 76
				if end > len(encoded) {
					end = len(encoded)
				}
				emailData.WriteString(encoded[i:end])
				emailData.WriteString("\r\n")
			}
		}

		emailData.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	} else {
		// Simple text email
		emailData.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		emailData.WriteString("\r\n")
		if hasText {
			emailData.WriteString(email.BodyTXT)
		}
		emailData.WriteString("\r\n")
	}

	// Sign with DKIM before sending
	signedData, err := ss.signDKIM(emailData.String())
	if err != nil {
		fmt.Printf("⚠️ DKIM signing failed: %v\n", err)
		// Continue without DKIM signature
		signedData = emailData.String()
	} else {
		fmt.Printf("🔐 DKIM signature applied\n")
	}

	// Use the existing relay function
	return ss.relayEmailData(email.From, email.To, signedData)
}

func (ss *SmtpServer) handleSMTPConnection(conn net.Conn) {
	defer conn.Close()

	clientAddr := conn.RemoteAddr().String()
	clientIP := strings.Split(clientAddr, ":")[0]
	fmt.Printf("📞 [%s] New SMTP connection from %s\n", time.Now().Format("15:04:05"), clientAddr)

	// SMTP greeting
	conn.Write([]byte("220 " + ss.Subdomain + " ESMTP KSMTP Server Ready\r\n"))

	var mailFrom, rcptTo string
	var authenticated bool
	var authenticatedUser string
	reader := bufio.NewReader(conn)

	for {
		// 🛡️ SÉCURITÉ: Timeout plus court
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		line, err := reader.ReadString('\n')
		if err != nil {
			// if err != io.EOF {
			// 	fmt.Printf("❌ [%s] Connection error from %s: %v\n", time.Now().Format("15:04:05"), clientAddr, err)
			// } else {
			// 	fmt.Printf("👋 [%s] Connection closed by %s\n", time.Now().Format("15:04:05"), clientAddr)
			// }
			return
		}

		command := strings.TrimSpace(line)
		// Don't log empty commands which can happen with just \r\n
		if command == "" {
			continue
		}

		// 🛡️ SÉCURITÉ: Valider la commande SMTP
		if !isValidSMTPCommand(command) {
			conn.Write([]byte("500 Invalid command\r\n"))
			continue
		}

		fmt.Printf("📨 [%s] Command: %s\n", time.Now().Format("15:04:05"), command)

		parts := strings.Fields(command)
		if len(parts) == 0 {
			continue
		}

		// Use switch true for prefix matching, which is more robust
		switch {
		case strings.HasPrefix(strings.ToUpper(command), "EHLO"):
			domain := "unknown"
			if len(parts) > 1 {
				domain = parts[1]
			}
			conn.Write([]byte("250-" + ss.Subdomain + " Hello " + domain + "\r\n"))
			conn.Write([]byte("250-SIZE 52428800\r\n"))
			conn.Write([]byte("250-8BITMIME\r\n"))
			conn.Write([]byte("250-PIPELINING\r\n"))
			if ss.tlsConfig != nil {
				conn.Write([]byte("250-STARTTLS\r\n"))
			}
			if len(ss.users) > 0 {
				conn.Write([]byte("250-AUTH LOGIN PLAIN\r\n"))
			}
			conn.Write([]byte("250 HELP\r\n"))

		case strings.HasPrefix(strings.ToUpper(command), "HELO"):
			domain := "unknown"
			if len(parts) > 1 {
				domain = parts[1]
			}
			conn.Write([]byte("250 " + ss.Subdomain + " Hello " + domain + "\r\n"))

		case strings.HasPrefix(strings.ToUpper(command), "MAIL FROM:"):
			mailFrom = extractEmail(command)
			conn.Write([]byte("250 OK\r\n"))
			fmt.Printf("✅ [%s] MAIL FROM: %s\n", time.Now().Format("15:04:05"), mailFrom)

		case strings.HasPrefix(strings.ToUpper(command), "RCPT TO:"):
			rcptTo = extractEmail(command)

			// Check if recipient is for our domain or external
			if strings.Contains(rcptTo, ss.Domain) {
				// Local delivery - check if auth required for local
				if ss.requiresAuthentication(mailFrom, rcptTo) && !authenticated {
					conn.Write([]byte("530 Authentication required\r\n"))
					continue
				}
				conn.Write([]byte("250 OK\r\n"))
				fmt.Printf("✅ [%s] RCPT TO: %s (local)\n", time.Now().Format("15:04:05"), rcptTo)
			} else {
				// 🛡️ SÉCURITÉ: Vérifier si l'authentification est requise pour le relais
				if ss.requiresAuthentication(mailFrom, rcptTo) && !authenticated {
					// logSecurityEvent(clientAddr, "AUTH_REQUIRED", fmt.Sprintf("Relay attempt from %s to %s", mailFrom, rcptTo))
					conn.Write([]byte("530 Authentication required\r\n"))
					continue
				}
				// External email - we'll relay it
				conn.Write([]byte("250 OK - Will relay\r\n"))
				fmt.Printf("🔄 [%s] RCPT TO: %s (relay)\n", time.Now().Format("15:04:05"), rcptTo)
			}

		case strings.HasPrefix(strings.ToUpper(command), "DATA"):
			conn.Write([]byte("354 End data with <CR><LF>.<CR><LF>\r\n"))

			var emailData strings.Builder
			totalSize := 0

			for {
				// 🛡️ SÉCURITÉ: Timeout pour la lecture des données
				conn.SetReadDeadline(time.Now().Add(30 * time.Second))
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					// logSecurityEvent(clientAddr, "DATA_READ_ERROR", fmt.Sprintf("Error: %v", err))
					// fmt.Printf("❌ [%s] Error reading email data from %s: %v\n", time.Now().Format("15:04:05"), clientAddr, err)
					return
				}

				// 🛡️ SÉCURITÉ: Vérifier la taille de l'email
				totalSize += len(dataLine)
				if totalSize > MaxEmailSize {
					// logSecurityEvent(clientAddr, "EMAIL_TOO_LARGE", fmt.Sprintf("Size: %d bytes", totalSize))
					conn.Write([]byte("552 Message size exceeds limit\r\n"))
					return
				}

				// The end of data marker is a line containing only a period.
				if dataLine == ".\r\n" {
					break
				}

				// Handle "dot-stuffing" (lines starting with a dot are sent with two dots)
				if strings.HasPrefix(dataLine, "..") {
					dataLine = strings.TrimPrefix(dataLine, ".")
				}

				emailData.WriteString(dataLine)
			}

			// Process the email
			ss.processEmail(mailFrom, rcptTo, emailData.String(), clientIP)
			conn.Write([]byte("250 OK: Message accepted for delivery\r\n"))
			fmt.Printf("✅ [%s] Email processed: %s → %s\n", time.Now().Format("15:04:05"), mailFrom, rcptTo)
			// After DATA, the state should reset for the next email in the same session
			mailFrom = ""
			rcptTo = ""

		case strings.HasPrefix(strings.ToUpper(command), "RSET"):
			mailFrom = ""
			rcptTo = ""
			conn.Write([]byte("250 OK\r\n"))

		case strings.HasPrefix(strings.ToUpper(command), "QUIT"):
			conn.Write([]byte("221 Bye\r\n"))
			fmt.Printf("👋 [%s] Connection closed gracefully\n", time.Now().Format("15:04:05"))
			return

		case strings.HasPrefix(strings.ToUpper(command), "AUTH"):
			if len(parts) < 2 {
				conn.Write([]byte("501 Syntax error\r\n"))
				continue
			}

			authMethod := strings.ToUpper(parts[1])
			switch authMethod {
			case "LOGIN":
				conn.Write([]byte("334 VXNlcm5hbWU6\r\n")) // "Username:" in base64

				userLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				username, err := base64.StdEncoding.DecodeString(strings.TrimSpace(userLine))
				if err != nil {
					conn.Write([]byte("535 Authentication failed\r\n"))
					continue
				}

				conn.Write([]byte("334 UGFzc3dvcmQ6\r\n")) // "Password:" in base64

				passLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				password, err := base64.StdEncoding.DecodeString(strings.TrimSpace(passLine))
				if err != nil {
					conn.Write([]byte("535 Authentication failed\r\n"))
					continue
				}

				if ss.authenticateUser(string(username), string(password)) {
					authenticated = true
					authenticatedUser = string(username)
					conn.Write([]byte("235 Authentication successful\r\n"))
					fmt.Printf("🔐 [%s] User authenticated: %s\n", time.Now().Format("15:04:05"), authenticatedUser)
				} else {
					conn.Write([]byte("535 Authentication failed\r\n"))
					fmt.Printf("❌ [%s] Authentication failed for: %s\n", time.Now().Format("15:04:05"), string(username))
				}

			case "PLAIN":
				if len(parts) >= 3 {
					// AUTH PLAIN with credentials in same line
					authData, err := base64.StdEncoding.DecodeString(parts[2])
					if err != nil {
						conn.Write([]byte("535 Authentication failed\r\n"))
						continue
					}

					// PLAIN format: \0username\0password
					authParts := strings.Split(string(authData), "\x00")
					if len(authParts) >= 3 {
						username := authParts[1]
						password := authParts[2]

						if ss.authenticateUser(username, password) {
							authenticated = true
							authenticatedUser = username
							conn.Write([]byte("235 Authentication successful\r\n"))
							fmt.Printf("🔐 [%s] User authenticated: %s\n", time.Now().Format("15:04:05"), authenticatedUser)
						} else {
							conn.Write([]byte("535 Authentication failed\r\n"))
							fmt.Printf("❌ [%s] Authentication failed for: %s\n", time.Now().Format("15:04:05"), username)
						}
					} else {
						conn.Write([]byte("535 Authentication failed\r\n"))
					}
				} else {
					conn.Write([]byte("334 \r\n")) // Request auth data

					authLine, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					authData, err := base64.StdEncoding.DecodeString(strings.TrimSpace(authLine))
					if err != nil {
						conn.Write([]byte("535 Authentication failed\r\n"))
						continue
					}

					// PLAIN format: \0username\0password
					authParts := strings.Split(string(authData), "\x00")
					if len(authParts) >= 3 {
						username := authParts[1]
						password := authParts[2]

						if ss.authenticateUser(username, password) {
							authenticated = true
							authenticatedUser = username
							conn.Write([]byte("235 Authentication successful\r\n"))
							fmt.Printf("🔐 [%s] User authenticated: %s\n", time.Now().Format("15:04:05"), authenticatedUser)
						} else {
							conn.Write([]byte("535 Authentication failed\r\n"))
							fmt.Printf("❌ [%s] Authentication failed for: %s\n", time.Now().Format("15:04:05"), username)
						}
					} else {
						conn.Write([]byte("535 Authentication failed\r\n"))
					}
				}

			default:
				conn.Write([]byte("504 Authentication mechanism not supported\r\n"))
			}

		case strings.HasPrefix(strings.ToUpper(command), "STARTTLS"):
			if ss.tlsConfig == nil {
				conn.Write([]byte("454 TLS not available\r\n"))
				continue
			}

			conn.Write([]byte("220 Ready to start TLS\r\n"))

			// Upgrade connection to TLS
			tlsConn := tls.Server(conn, ss.tlsConfig)
			err := tlsConn.Handshake()
			if err != nil {
				fmt.Printf("❌ [%s] TLS handshake failed: %v\n", time.Now().Format("15:04:05"), err)
				return
			}

			fmt.Printf("🔒 [%s] TLS connection established\n", time.Now().Format("15:04:05"))

			// Replace the connection with the TLS connection
			conn = tlsConn
			reader = bufio.NewReader(conn)

			// Reset authentication state after STARTTLS
			authenticated = false
			authenticatedUser = ""
			mailFrom = ""
			rcptTo = ""

		case strings.HasPrefix(strings.ToUpper(command), "NOOP"):
			conn.Write([]byte("250 OK\r\n"))

		default:
			conn.Write([]byte("500 Command not recognized\r\n"))
			fmt.Printf("❓ [%s] Unknown command: %s\n", time.Now().Format("15:04:05"), command)
		}
	}
}

func (ss *SmtpServer) processEmail(from, to, data, clientIP string) {
	if strings.Contains(to, ss.Domain) {
		// We receive an email - apply spam filtering
		if ss.onRecv != nil {
			pe := ss.ParseRawEmail(from, data)
			if pe != nil {
				// Appliquer le filtrage anti-spam
				spamResult := ss.spamFilter.CheckEmail(pe, clientIP)

				fmt.Printf("🛡️ [SPAM] Email from %s: Score=%d, Action=%s\n", from, spamResult.Score, spamResult.Action)
				if len(spamResult.Details) > 0 {
					fmt.Printf("🛡️ [SPAM] Details: %s\n", strings.Join(spamResult.Details, "; "))
				}

				switch spamResult.Action {
				case "REJECT":
					fmt.Printf("❌ [SPAM] Email rejected: %s\n", spamResult.Reason)
					return // Ne pas traiter l'email
				case "QUARANTINE":
					fmt.Printf("⚠️ [SPAM] Email quarantined: %s\n", spamResult.Reason)
					// Marquer l'email comme spam mais le traiter quand même
					pe.Subject = "[SPAM] " + pe.Subject
				case "ACCEPT":
					fmt.Printf("✅ [SPAM] Email accepted: %s\n", spamResult.Reason)
				}

				ss.onRecv(pe)
			}
		}
	} else {
		// We send an email
		fmt.Println("SENDING EMAIL, from:", from, ", to:", to)
		ss.relayEmail(from, to, data)
	}
}

type Email struct {
	From      string
	To        string
	CC        string
	Subject   string
	BodyTXT   string
	BodyHTML  string
	Date      string
	FailedRaw string
	Headers   string
	Files     []FileInfo
}

type FileInfo struct {
	Filename string
	Filedata []byte
}

// Helper function to establish SMTP connection with TLS
func (ss *SmtpServer) connectSMTP(mx *net.MX) (*smtp.Client, error) {
	addr := fmt.Sprintf("%s:25", mx.Host)
	fmt.Printf("📡 Trying to relay via %s (preference %d)...\n", addr, mx.Pref)

	// Connect to the remote SMTP server.
	c, err := smtp.Dial(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", addr, err)
	}

	// We MUST explicitly send a HELO/EHLO with our server's fully qualified domain name.
	if err := c.Hello(ss.Subdomain); err != nil {
		c.Close()
		return nil, fmt.Errorf("HELO command failed for %s: %w", addr, err)
	}

	// Try to enable TLS encryption
	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsConfig := &tls.Config{
			ServerName: mx.Host,
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			fmt.Printf("⚠️ STARTTLS failed for %s: %v\n", mx.Host, err)
			// Continue without TLS if it fails
		} else {
			fmt.Printf("🔒 TLS encryption enabled for %s\n", mx.Host)
		}
	}

	return c, nil
}

func (ss *SmtpServer) relayEmailData(from, to, data string) error {
	fmt.Printf("🔄 Relaying email from <%s> to <%s>...\n", from, to)

	// Extract domain from recipient email
	parts := strings.Split(to, "@")
	if len(parts) != 2 {
		return fmt.Errorf("invalid recipient email format: %s", to)
	}
	domain := parts[1]

	fmt.Printf("🔍 Looking up MX records for %s...\n", domain)
	mxs, err := net.LookupMX(domain)
	if err != nil {
		return fmt.Errorf("failed to look up MX records for %s: %w", domain, err)
	}

	if len(mxs) == 0 {
		return fmt.Errorf("no MX records found for %s", domain)
	}

	var lastErr error
	for _, mx := range mxs {
		c, err := ss.connectSMTP(mx)
		if err != nil {
			lastErr = err
			fmt.Printf("⚠️ %v. Trying next server...\n", lastErr)
			continue
		}

		// Set the sender.
		if err := c.Mail(from); err != nil {
			lastErr = fmt.Errorf("MAIL FROM failed for %s: %w", mx.Host, err)
			c.Close()
			continue
		}

		// Set the recipient.
		if err := c.Rcpt(to); err != nil {
			lastErr = fmt.Errorf("RCPT TO failed for %s: %w", mx.Host, err)
			c.Close()
			continue
		}

		// Send the email body.
		w, err := c.Data()
		if err != nil {
			lastErr = fmt.Errorf("DATA command failed for %s: %w", mx.Host, err)
			c.Close()
			continue
		}

		_, err = w.Write([]byte(data))
		if err != nil {
			w.Close() // Attempt to close to finalize
			lastErr = fmt.Errorf("writing data failed for %s: %w", mx.Host, err)
			c.Close()
			continue
		}
		w.Close()
		c.Quit()

		fmt.Printf("✅ Email successfully sent to <%s> via %s\n", to, mx.Host)
		return nil // Success!
	}

	return fmt.Errorf("all MX servers for %s failed: %w", domain, lastErr)
}

func (ss *SmtpServer) relayEmail(from, to, data string) {
	fmt.Printf("🔄 Relaying email from <%s> to <%s>...\n", from, to)

	// Extract domain from recipient email
	parts := strings.Split(to, "@")
	if len(parts) != 2 {
		fmt.Printf("❌ Invalid recipient email format: %s\n", to)
		return
	}
	domain := parts[1]

	fmt.Printf("🔍 Looking up MX records for %s...\n", domain)
	mxs, err := net.LookupMX(domain)
	if err != nil {
		fmt.Printf("❌ Failed to look up MX records for %s: %v\n", domain, err)
		return
	}

	if len(mxs) == 0 {
		fmt.Printf("❌ No MX records found for %s. Cannot relay.\n", domain)
		return
	}

	var lastErr error
	for _, mx := range mxs {
		c, err := ss.connectSMTP(mx)
		if err != nil {
			lastErr = err
			fmt.Printf("⚠️ %v. Trying next server...\n", lastErr)
			continue
		}

		// Set the sender.
		if err := c.Mail(from); err != nil {
			lastErr = fmt.Errorf("MAIL FROM failed for %s: %w", mx.Host, err)
			c.Close()
			continue
		}

		// Set the recipient.
		if err := c.Rcpt(to); err != nil {
			lastErr = fmt.Errorf("RCPT TO failed for %s: %w", mx.Host, err)
			c.Close()
			continue
		}

		// Send the email body.
		w, err := c.Data()
		if err != nil {
			lastErr = fmt.Errorf("DATA command failed for %s: %w", mx.Host, err)
			c.Close()
			continue
		}

		_, err = w.Write([]byte(data))
		if err != nil {
			w.Close() // Attempt to close to finalize
			lastErr = fmt.Errorf("writing data failed for %s: %w", mx.Host, err)
			c.Close()
			continue
		}
		w.Close()
		c.Quit()

		fmt.Printf("✅ Email successfully relayed to <%s> via %s\n", to, mx.Host)
		return // Success!
	}

	fmt.Printf("❌ All MX servers for %s failed. Could not relay email. Last error: %v\n", domain, lastErr)
}

func extractEmail(command string) string {
	// Extract email from "MAIL FROM:<email>" or "RCPT TO:<email>"
	start := strings.Index(command, "<")
	end := strings.Index(command, ">")
	if start != -1 && end != -1 && end > start {
		return command[start+1 : end]
	}

	// Fallback - extract email pattern
	parts := strings.Fields(command)
	if len(parts) > 1 {
		return strings.Trim(parts[len(parts)-1], "<>")
	}
	return "unknown"
}

func sanitizeFilename(filename string) string {
	// Remplacer les caractères dangereux
	filename = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, filename)

	// Éviter les noms de fichiers vides ou dangereux
	if filename == "" || filename == "." || filename == ".." {
		filename = "attachment"
	}

	// Limiter la longueur
	if len(filename) > 255 {
		filename = filename[:255]
	}

	return filename
}

func isValidSMTPCommand(command string) bool {
	command = strings.ToUpper(strings.TrimSpace(command))

	// Vérifier que c'est un caractère ASCII valide
	for _, r := range command {
		if r > 127 || r < 32 {
			return false
		}
	}

	// Commandes SMTP valides
	validCommands := []string{
		"EHLO", "HELO", "MAIL FROM:", "RCPT TO:", "DATA",
		"RSET", "QUIT", "NOOP", "HELP", "VRFY", "EXPN", "AUTH", "STARTTLS",
	}

	for _, valid := range validCommands {
		if strings.HasPrefix(command, valid) {
			return true
		}
	}

	return false
}

// initializeDKIMKey checks if DKIM key exists, if not generates a new one
func initializeDKIMKey() error {
	// Check if DKIM key file exists
	if _, err := os.Stat(dkimKeyFile); err == nil {
		fmt.Printf("🔑 DKIM key file found: %s\n", dkimKeyFile)
		return nil
	}

	fmt.Printf("🔑 DKIM key file not found, generating new key...\n")

	// Generate new RSA key pair (2048 bits)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate RSA key: %w", err)
	}

	// Convert private key to PEM format
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	// Save private key to file
	err = os.WriteFile(dkimKeyFile, privateKeyPEM, 0600)
	if err != nil {
		return fmt.Errorf("failed to save DKIM private key: %w", err)
	}

	// Generate public key for DNS
	publicKey := &privateKey.PublicKey
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("failed to marshal public key: %w", err)
	}
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKeyBytes)

	fmt.Printf("✅ DKIM key pair generated and saved to %s\n", dkimKeyFile)
	fmt.Printf("📋 Add this DKIM record to your DNS:\n")
	fmt.Printf("   Hostname: %s._domainkey\n", dkimSelector)
	fmt.Printf("   Value: \"v=DKIM1; k=rsa; p=%s\"\n", pubKeyB64)

	return nil
}

// loadDKIMPrivateKey loads the DKIM private key from file
func loadDKIMPrivateKey() (string, error) {
	keyData, err := os.ReadFile(dkimKeyFile)
	if err != nil {
		return "", fmt.Errorf("failed to read DKIM key file: %w", err)
	}
	return string(keyData), nil
}

func getDKIMPublicKey(privateKeyPEM string) (string, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return "", fmt.Errorf("échec du décodage du bloc PEM")
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("échec du parsing de la clé privée PKCS1: %v", err)
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return "", fmt.Errorf("échec du mashalling de la clé publique: %v", err)
	}
	return base64.StdEncoding.EncodeToString(pubBytes), nil
}

// signDKIM adds DKIM signature to email data
func (ss *SmtpServer) signDKIM(emailData string) (string, error) {
	// Load the private key from file
	dkimPrivateKey, err := loadDKIMPrivateKey()
	if err != nil {
		return "", fmt.Errorf("failed to load DKIM private key: %w", err)
	}

	// Parse the private key
	block, _ := pem.Decode([]byte(dkimPrivateKey))
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM block")
	}

	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %v", err)
	}

	// Split headers and body
	parts := strings.SplitN(emailData, "\r\n\r\n", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid email format")
	}

	headers := parts[0]
	body := parts[1]

	// Canonicalize body (simple canonicalization)
	bodyCanon := strings.ReplaceAll(body, "\r\n", "\n")
	bodyCanon = strings.ReplaceAll(bodyCanon, "\n", "\r\n")
	if !strings.HasSuffix(bodyCanon, "\r\n") {
		bodyCanon += "\r\n"
	}

	// Calculate body hash
	bodyHash := sha256.Sum256([]byte(bodyCanon))
	bodyHashB64 := base64.StdEncoding.EncodeToString(bodyHash[:])

	// Extract headers to sign
	headerLines := strings.Split(headers, "\r\n")
	var fromHeader, toHeader, subjectHeader, dateHeader string

	for _, line := range headerLines {
		if strings.HasPrefix(strings.ToLower(line), "from:") {
			fromHeader = line
		} else if strings.HasPrefix(strings.ToLower(line), "to:") {
			toHeader = line
		} else if strings.HasPrefix(strings.ToLower(line), "subject:") {
			subjectHeader = line
		} else if strings.HasPrefix(strings.ToLower(line), "date:") {
			dateHeader = line
		}
	}

	// Create DKIM signature header (without signature value)
	timestamp := time.Now().Unix()
	dkimHeader := fmt.Sprintf("DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/simple; d=%s; s=%s; t=%d; bh=%s; h=from:to:subject:date; b=",
		ss.Domain, dkimSelector, timestamp, bodyHashB64)

	// Canonicalize headers for signing (relaxed canonicalization)
	var canonHeaders []string
	if fromHeader != "" {
		canonHeaders = append(canonHeaders, canonicalizeHeader(fromHeader))
	}
	if toHeader != "" {
		canonHeaders = append(canonHeaders, canonicalizeHeader(toHeader))
	}
	if subjectHeader != "" {
		canonHeaders = append(canonHeaders, canonicalizeHeader(subjectHeader))
	}
	if dateHeader != "" {
		canonHeaders = append(canonHeaders, canonicalizeHeader(dateHeader))
	}

	// Add the DKIM header itself (without the signature value)
	canonHeaders = append(canonHeaders, canonicalizeHeader(dkimHeader))

	// Create the string to sign
	signString := strings.Join(canonHeaders, "\r\n")

	// Sign the string
	hash := sha256.Sum256([]byte(signString))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("failed to sign: %v", err)
	}

	// Encode signature
	signatureB64 := base64.StdEncoding.EncodeToString(signature)

	// Complete DKIM header
	completeDKIMHeader := fmt.Sprintf("DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/simple; d=%s; s=%s; t=%d; bh=%s; h=from:to:subject:date; b=%s",
		ss.Domain, dkimSelector, timestamp, bodyHashB64, signatureB64)

	// Add DKIM header to the email
	signedEmail := completeDKIMHeader + "\r\n" + emailData

	return signedEmail, nil
}

// canonicalizeHeader performs relaxed canonicalization on a header
func canonicalizeHeader(header string) string {
	// Split header name and value
	parts := strings.SplitN(header, ":", 2)
	if len(parts) != 2 {
		return header
	}

	name := strings.ToLower(strings.TrimSpace(parts[0]))
	value := strings.TrimSpace(parts[1])

	// Collapse whitespace in value
	re := regexp.MustCompile(`\s+`)
	value = re.ReplaceAllString(value, " ")

	return name + ":" + value
}

func (ss *SmtpServer) SendEmailViaTunnel(email *Email) error {
	if email == nil {
		return fmt.Errorf("email cannot be nil")
	}

	if email.To == "" {
		return fmt.Errorf("recipient email is required")
	}

	if email.From == "" {
		return fmt.Errorf("sender email is required")
	}

	// Build the raw email data (same as SendEmail)
	var emailData strings.Builder

	// Headers
	emailData.WriteString(fmt.Sprintf("From: %s\r\n", email.From))
	emailData.WriteString(fmt.Sprintf("To: %s\r\n", email.To))
	if email.CC != "" {
		emailData.WriteString(fmt.Sprintf("Cc: %s\r\n", email.CC))
	}
	if email.Subject != "" {
		emailData.WriteString(fmt.Sprintf("Subject: %s\r\n", email.Subject))
	}
	if email.Date != "" {
		emailData.WriteString(fmt.Sprintf("Date: %s\r\n", email.Date))
	} else {
		emailData.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	}

	// MIME headers for multipart if we have HTML, text, or attachments
	hasHTML := email.BodyHTML != ""
	hasText := email.BodyTXT != ""
	hasAttachments := len(email.Files) > 0

	if hasHTML || hasText || hasAttachments {
		boundary := fmt.Sprintf("boundary_%d", time.Now().UnixNano())
		emailData.WriteString("MIME-Version: 1.0\r\n")
		emailData.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary))
		emailData.WriteString("\r\n")

		// Text/HTML parts
		if hasHTML && hasText {
			// Alternative boundary for text/html
			altBoundary := fmt.Sprintf("alt_boundary_%d", time.Now().UnixNano())
			emailData.WriteString(fmt.Sprintf("--%s\r\n", boundary))
			emailData.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n", altBoundary))
			emailData.WriteString("\r\n")

			// Text part
			emailData.WriteString(fmt.Sprintf("--%s\r\n", altBoundary))
			emailData.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
			emailData.WriteString("\r\n")
			emailData.WriteString(email.BodyTXT)
			emailData.WriteString("\r\n")

			// HTML part
			emailData.WriteString(fmt.Sprintf("--%s\r\n", altBoundary))
			emailData.WriteString("Content-Type: text/html; charset=utf-8\r\n")
			emailData.WriteString("\r\n")
			emailData.WriteString(email.BodyHTML)
			emailData.WriteString("\r\n")

			emailData.WriteString(fmt.Sprintf("--%s--\r\n", altBoundary))
		} else if hasHTML {
			emailData.WriteString(fmt.Sprintf("--%s\r\n", boundary))
			emailData.WriteString("Content-Type: text/html; charset=utf-8\r\n")
			emailData.WriteString("\r\n")
			emailData.WriteString(email.BodyHTML)
			emailData.WriteString("\r\n")
		} else if hasText {
			emailData.WriteString(fmt.Sprintf("--%s\r\n", boundary))
			emailData.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
			emailData.WriteString("\r\n")
			emailData.WriteString(email.BodyTXT)
			emailData.WriteString("\r\n")
		}

		// Attachments
		for _, file := range email.Files {
			emailData.WriteString(fmt.Sprintf("--%s\r\n", boundary))
			emailData.WriteString(fmt.Sprintf("Content-Type: application/octet-stream\r\n"))
			emailData.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", file.Filename))
			emailData.WriteString("Content-Transfer-Encoding: base64\r\n")
			emailData.WriteString("\r\n")

			// Encode file data in base64
			encoded := base64.StdEncoding.EncodeToString(file.Filedata)
			// Split into 76-character lines as per RFC
			for i := 0; i < len(encoded); i += 76 {
				end := i + 76
				if end > len(encoded) {
					end = len(encoded)
				}
				emailData.WriteString(encoded[i:end])
				emailData.WriteString("\r\n")
			}
		}

		emailData.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	} else {
		// Simple text email
		emailData.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		emailData.WriteString("\r\n")
		if hasText {
			emailData.WriteString(email.BodyTXT)
		}
		emailData.WriteString("\r\n")
	}

	// Sign with DKIM before sending
	signedData, err := ss.signDKIM(emailData.String())
	if err != nil {
		fmt.Printf("⚠️ DKIM signing failed: %v\n", err)
		// Continue without DKIM signature
		signedData = emailData.String()
	} else {
		fmt.Printf("🔐 DKIM signature applied\n")
	}

	// Connect directly to localhost:25 (SSH tunnel)
	fmt.Printf("🚇 Connecting via SSH tunnel to localhost:25...\n")

	c, err := smtp.Dial("localhost:25")
	if err != nil {
		return fmt.Errorf("failed to connect to SSH tunnel: %w", err)
	}
	defer c.Quit()

	// HELO
	if err := c.Hello(ss.Subdomain); err != nil {
		return fmt.Errorf("HELO command failed: %w", err)
	}

	// Set sender
	if err := c.Mail(email.From); err != nil {
		return fmt.Errorf("MAIL FROM failed: %w", err)
	}

	// Set recipient
	if err := c.Rcpt(email.To); err != nil {
		return fmt.Errorf("RCPT TO failed: %w", err)
	}

	// Send data
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA command failed: %w", err)
	}

	_, err = w.Write([]byte(signedData))
	if err != nil {
		w.Close()
		return fmt.Errorf("writing data failed: %w", err)
	}
	w.Close()

	fmt.Printf("✅ Email sent via SSH tunnel!\n")
	return nil
}

// AddUser adds a user for SMTP authentication
func (ss *SmtpServer) AddUser(username, password string) {
	if ss.users == nil {
		ss.users = make(map[string]string)
	}
	ss.users[username] = password
}

// authenticateUser checks if username/password combination is valid
func (ss *SmtpServer) authenticateUser(username, password string) bool {
	if ss.users == nil {
		return false
	}
	storedPassword, exists := ss.users[username]
	return exists && storedPassword == password
}

// requiresAuthentication checks if authentication is required for this email
func (ss *SmtpServer) requiresAuthentication(from, to string) bool {
	isLocalRecipient := strings.Contains(to, ss.Domain)
	isLocalSender := strings.Contains(from, ss.Domain)

	// Local to local: check requireAuthForLocal
	if isLocalRecipient && isLocalSender {
		return ss.requireAuthForLocal
	}

	// External to local (incoming): check requireAuthForLocal
	if isLocalRecipient && !isLocalSender {
		return ss.requireAuthForLocal
	}

	// Local to external OR external to external: check requireAuthForRelay
	return ss.requireAuthForRelay
}

// RegisterTemplate registers a new email template
func (ss *SmtpServer) RegisterTemplate(name, subject, bodyTXT, bodyHTML string) {
	if ss.templates == nil {
		ss.templates = make(map[string]*EmailTemplate)
	}
	ss.templates[name] = &EmailTemplate{
		Name:     name,
		Subject:  subject,
		BodyTXT:  bodyTXT,
		BodyHTML: bodyHTML,
	}
	fmt.Printf("📧 Template '%s' registered successfully\n", name)
}

// SendTemplate sends an email using a registered template with data substitution
func (ss *SmtpServer) SendTemplate(templateName, from, to string, data map[string]interface{}) error {
	// Check if template exists
	tmpl, exists := ss.templates[templateName]
	if !exists {
		return fmt.Errorf("template '%s' not found", templateName)
	}

	// Create email from template
	email := &Email{
		From: from,
		To:   to,
	}

	// Process subject template
	if tmpl.Subject != "" {
		subjectTmpl, err := template.New("subject").Parse(tmpl.Subject)
		if err != nil {
			return fmt.Errorf("failed to parse subject template: %w", err)
		}
		var subjectBuf bytes.Buffer
		if err := subjectTmpl.Execute(&subjectBuf, data); err != nil {
			return fmt.Errorf("failed to execute subject template: %w", err)
		}
		email.Subject = subjectBuf.String()
	}

	// Process text body template
	if tmpl.BodyTXT != "" {
		txtTmpl, err := template.New("bodyTXT").Parse(tmpl.BodyTXT)
		if err != nil {
			return fmt.Errorf("failed to parse text body template: %w", err)
		}
		var txtBuf bytes.Buffer
		if err := txtTmpl.Execute(&txtBuf, data); err != nil {
			return fmt.Errorf("failed to execute text body template: %w", err)
		}
		email.BodyTXT = txtBuf.String()
	}

	// Process HTML body template
	if tmpl.BodyHTML != "" {
		htmlTmpl, err := template.New("bodyHTML").Parse(tmpl.BodyHTML)
		if err != nil {
			return fmt.Errorf("failed to parse HTML body template: %w", err)
		}
		var htmlBuf bytes.Buffer
		if err := htmlTmpl.Execute(&htmlBuf, data); err != nil {
			return fmt.Errorf("failed to execute HTML body template: %w", err)
		}
		email.BodyHTML = htmlBuf.String()
	}

	// Send the email
	fmt.Printf("📧 Sending email using template '%s' from %s to %s\n", templateName, from, to)
	return ss.SendEmail(email)
}

// GetTemplates returns a list of all registered template names
func (ss *SmtpServer) GetTemplates() []string {
	var names []string
	for name := range ss.templates {
		names = append(names, name)
	}
	return names
}

// GetTemplate returns a specific template by name
func (ss *SmtpServer) GetTemplate(name string) (*EmailTemplate, bool) {
	tmpl, exists := ss.templates[name]
	return tmpl, exists
}

// SendTemplateViaTunnel sends an email using a registered template via SSH tunnel
func (ss *SmtpServer) SendTemplateViaTunnel(templateName, from, to string, data map[string]interface{}) error {
	// Check if template exists
	tmpl, exists := ss.templates[templateName]
	if !exists {
		return fmt.Errorf("template '%s' not found", templateName)
	}

	// Create email from template
	email := &Email{
		From: from,
		To:   to,
	}

	// Process subject template
	if tmpl.Subject != "" {
		subjectTmpl, err := template.New("subject").Parse(tmpl.Subject)
		if err != nil {
			return fmt.Errorf("failed to parse subject template: %w", err)
		}
		var subjectBuf bytes.Buffer
		if err := subjectTmpl.Execute(&subjectBuf, data); err != nil {
			return fmt.Errorf("failed to execute subject template: %w", err)
		}
		email.Subject = subjectBuf.String()
	}

	// Process text body template
	if tmpl.BodyTXT != "" {
		txtTmpl, err := template.New("bodyTXT").Parse(tmpl.BodyTXT)
		if err != nil {
			return fmt.Errorf("failed to parse text body template: %w", err)
		}
		var txtBuf bytes.Buffer
		if err := txtTmpl.Execute(&txtBuf, data); err != nil {
			return fmt.Errorf("failed to execute text body template: %w", err)
		}
		email.BodyTXT = txtBuf.String()
	}

	// Process HTML body template
	if tmpl.BodyHTML != "" {
		htmlTmpl, err := template.New("bodyHTML").Parse(tmpl.BodyHTML)
		if err != nil {
			return fmt.Errorf("failed to parse HTML body template: %w", err)
		}
		var htmlBuf bytes.Buffer
		if err := htmlTmpl.Execute(&htmlBuf, data); err != nil {
			return fmt.Errorf("failed to execute HTML body template: %w", err)
		}
		email.BodyHTML = htmlBuf.String()
	}

	// Send the email via tunnel
	fmt.Printf("🚇 Sending email using template '%s' via tunnel from %s to %s\n", templateName, from, to)
	return ss.SendEmailViaTunnel(email)
}

// DeleteTemplate removes a template by name
func (ss *SmtpServer) DeleteTemplate(name string) bool {
	if ss.templates == nil {
		return false
	}
	if _, exists := ss.templates[name]; exists {
		delete(ss.templates, name)
		fmt.Printf("🗑️ Template '%s' deleted successfully\n", name)
		return true
	}
	return false
}

// UpdateTemplate updates an existing template
func (ss *SmtpServer) UpdateTemplate(name, subject, bodyTXT, bodyHTML string) error {
	if ss.templates == nil {
		return fmt.Errorf("no templates registered")
	}
	if _, exists := ss.templates[name]; !exists {
		return fmt.Errorf("template '%s' not found", name)
	}

	ss.templates[name] = &EmailTemplate{
		Name:     name,
		Subject:  subject,
		BodyTXT:  bodyTXT,
		BodyHTML: bodyHTML,
	}
	fmt.Printf("✏️ Template '%s' updated successfully\n", name)
	return nil
}

// ValidateTemplate checks if a template is valid by testing it with sample data
func (ss *SmtpServer) ValidateTemplate(name string, sampleData map[string]interface{}) error {
	tmpl, exists := ss.templates[name]
	if !exists {
		return fmt.Errorf("template '%s' not found", name)
	}

	// Test subject template
	if tmpl.Subject != "" {
		subjectTmpl, err := template.New("subject").Parse(tmpl.Subject)
		if err != nil {
			return fmt.Errorf("invalid subject template: %w", err)
		}
		var subjectBuf bytes.Buffer
		if err := subjectTmpl.Execute(&subjectBuf, sampleData); err != nil {
			return fmt.Errorf("subject template execution failed: %w", err)
		}
	}

	// Test text body template
	if tmpl.BodyTXT != "" {
		txtTmpl, err := template.New("bodyTXT").Parse(tmpl.BodyTXT)
		if err != nil {
			return fmt.Errorf("invalid text body template: %w", err)
		}
		var txtBuf bytes.Buffer
		if err := txtTmpl.Execute(&txtBuf, sampleData); err != nil {
			return fmt.Errorf("text body template execution failed: %w", err)
		}
	}

	// Test HTML body template
	if tmpl.BodyHTML != "" {
		htmlTmpl, err := template.New("bodyHTML").Parse(tmpl.BodyHTML)
		if err != nil {
			return fmt.Errorf("invalid HTML body template: %w", err)
		}
		var htmlBuf bytes.Buffer
		if err := htmlTmpl.Execute(&htmlBuf, sampleData); err != nil {
			return fmt.Errorf("HTML body template execution failed: %w", err)
		}
	}

	fmt.Printf("✅ Template '%s' validation successful\n", name)
	return nil
}

// GetSpamFilter returns the spam filter instance
func (ss *SmtpServer) GetSpamFilter() *SpamFilter {
	return ss.spamFilter
}

// SetSpamFilterEnabled enables or disables spam filtering
func (ss *SmtpServer) SetSpamFilterEnabled(enabled bool) {
	ss.spamFilter.SetEnabled(enabled)
	if enabled {
		fmt.Printf("🛡️ Spam filtering enabled\n")
	} else {
		fmt.Printf("⚠️ Spam filtering disabled\n")
	}
}

// AddSpamBlacklistIP adds an IP to the spam blacklist
func (ss *SmtpServer) AddSpamBlacklistIP(ip string) {
	ss.spamFilter.AddBlacklistedIP(ip)
	fmt.Printf("🚫 IP %s added to spam blacklist\n", ip)
}

// AddSpamBlacklistDomain adds a domain to the spam blacklist
func (ss *SmtpServer) AddSpamBlacklistDomain(domain string) {
	ss.spamFilter.AddBlacklistedDomain(domain)
	fmt.Printf("🚫 Domain %s added to spam blacklist\n", domain)
}

// RemoveSpamBlacklistIP removes an IP from the spam blacklist
func (ss *SmtpServer) RemoveSpamBlacklistIP(ip string) {
	ss.spamFilter.RemoveBlacklistedIP(ip)
	fmt.Printf("✅ IP %s removed from spam blacklist\n", ip)
}

// RemoveSpamBlacklistDomain removes a domain from the spam blacklist
func (ss *SmtpServer) RemoveSpamBlacklistDomain(domain string) {
	ss.spamFilter.RemoveBlacklistedDomain(domain)
	fmt.Printf("✅ Domain %s removed from spam blacklist\n", domain)
}

// GetSpamFilterStats returns spam filter statistics
func (ss *SmtpServer) GetSpamFilterStats() map[string]interface{} {
	return ss.spamFilter.GetStats()
}

// PrintSpamFilterStats prints spam filter statistics
func (ss *SmtpServer) PrintSpamFilterStats() {
	stats := ss.GetSpamFilterStats()
	fmt.Println()
	fmt.Println("🛡️ SPAM FILTER STATISTICS 🛡️")
	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("Status: %v\n", stats["enabled"])
	fmt.Printf("Blacklisted IPs: %d\n", stats["blacklisted_ips"])
	fmt.Printf("Blacklisted Domains: %d\n", stats["blacklisted_domains"])
	fmt.Printf("Spam Keywords: %d\n", stats["spam_keywords"])
	fmt.Printf("Rate Limited IPs: %d\n", stats["rate_limited_ips"])
	fmt.Printf("Max Email Size: %d bytes\n", stats["max_email_size"])
	fmt.Println(strings.Repeat("-", 50))
}
