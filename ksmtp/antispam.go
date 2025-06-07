// Package ksmtp - Système de filtrage anti-spam
//
// USAGE SIMPLE:
//
//	// 1. Créer un filtre anti-spam
//	spamFilter := ksmtp.NewSpamFilter()
//
//	// 2. Vérifier un email
//	result := spamFilter.CheckEmail(email, "192.168.1.10")
//
//	// 3. Traiter selon le résultat
//	switch result.Action {
//	case "REJECT":
//	    // Bloquer l'email
//	    return fmt.Errorf("spam détecté: %s", result.Reason)
//	case "QUARANTINE":
//	    // Marquer comme spam mais livrer
//	    email.Subject = "[SPAM] " + email.Subject
//	case "ACCEPT":
//	    // Email légitime, livrer normalement
//	}
//
//	// 4. Gérer les blacklists
//	spamFilter.AddBlacklistedIP("203.0.113.100")
//	spamFilter.AddBlacklistedDomain("spam-domain.com")
//	spamFilter.RemoveBlacklistedIP("192.168.1.10")
//
//	// 5. Contrôler le filtre
//	spamFilter.SetEnabled(false)  // Désactiver
//	stats := spamFilter.GetStats() // Statistiques
//
// SCORES:
//
//	0-49:  Email accepté
//	50-99: Email mis en quarantaine (marqué [SPAM])
//	100+:  Email rejeté
//
// DÉTECTIONS:
//   - Blacklists IP/domaines
//   - Mots-clés spam (32 mots FR/EN)
//   - Structure suspecte (CAPS, exclamations)
//   - URLs raccourcies/suspectes
//   - Taille excessive (>10MB)
//   - Rate limiting (50 emails/heure/IP)
//   - En-têtes manquants
//   - DNSBL (listes noires DNS)
package ksmtp

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

// SpamFilter represents an anti-spam filter
type SpamFilter struct {
	enabled            bool
	blacklistedIPs     map[string]bool
	blacklistedDomains map[string]bool
	spamKeywords       []string
	maxEmailSize       int
	rateLimiter        map[string]*RateLimit
	dnsblServers       []string
}

// RateLimit tracks email sending rate per IP
type RateLimit struct {
	count     int
	lastReset time.Time
	maxEmails int
	window    time.Duration
}

// SpamResult represents the result of spam filtering
type SpamResult struct {
	IsSpam  bool
	Score   int
	Reason  string
	Action  string // "REJECT", "QUARANTINE", "ACCEPT"
	Details []string
}

// NewSpamFilter creates a new spam filter
func NewSpamFilter() *SpamFilter {
	return &SpamFilter{
		enabled:            true,
		blacklistedIPs:     make(map[string]bool),
		blacklistedDomains: make(map[string]bool),
		rateLimiter:        make(map[string]*RateLimit),
		maxEmailSize:       10 * 1024 * 1024, // 10MB
		spamKeywords: []string{
			// Mots-clés spam courants
			"viagra", "cialis", "lottery", "winner", "congratulations",
			"free money", "click here", "urgent", "act now", "limited time",
			"make money fast", "work from home", "get rich quick",
			"no obligation", "risk free", "guarantee", "amazing deal",
			"once in a lifetime", "exclusive offer", "special promotion",
			// Mots en français
			"gratuit", "urgent", "félicitations", "gagnant", "loterie",
			"argent facile", "offre spéciale", "promotion exclusive",
			"cliquez ici", "sans engagement", "garantie", "opportunité unique",
		},
		dnsblServers: []string{
			"zen.spamhaus.org",
			"bl.spamcop.net",
			"dnsbl.sorbs.net",
			"b.barracudacentral.org",
		},
	}
}

// CheckEmail performs comprehensive spam filtering
func (sf *SpamFilter) CheckEmail(email *Email, senderIP string) *SpamResult {
	if !sf.enabled {
		return &SpamResult{
			IsSpam: false,
			Score:  0,
			Action: "ACCEPT",
			Reason: "Spam filtering disabled",
		}
	}

	result := &SpamResult{
		IsSpam:  false,
		Score:   0,
		Action:  "ACCEPT",
		Details: []string{},
	}

	// 1. Vérifier la taille de l'email
	sf.checkEmailSize(email, result)

	// 2. Vérifier l'IP de l'expéditeur
	sf.checkSenderIP(senderIP, result)

	// 3. Vérifier le domaine de l'expéditeur
	sf.checkSenderDomain(email.From, result)

	// 4. Vérifier les mots-clés spam
	sf.checkSpamKeywords(email, result)

	// 5. Vérifier la structure de l'email
	sf.checkEmailStructure(email, result)

	// 6. Vérifier le rate limiting
	sf.checkRateLimit(senderIP, result)

	// 7. Vérifier les DNSBL (DNS Blacklists)
	sf.checkDNSBL(senderIP, result)

	// 8. Vérifier les en-têtes suspects
	sf.checkSuspiciousHeaders(email, result)

	// Déterminer l'action finale
	sf.determineAction(result)

	return result
}

// checkEmailSize vérifie la taille de l'email
func (sf *SpamFilter) checkEmailSize(email *Email, result *SpamResult) {
	totalSize := len(email.BodyTXT) + len(email.BodyHTML) + len(email.Headers)
	for _, file := range email.Files {
		totalSize += len(file.Filedata)
	}

	if totalSize > sf.maxEmailSize {
		result.Score += 50
		result.Details = append(result.Details, fmt.Sprintf("Email too large: %d bytes", totalSize))
	}
}

// checkSenderIP vérifie l'IP de l'expéditeur
func (sf *SpamFilter) checkSenderIP(senderIP string, result *SpamResult) {
	if sf.blacklistedIPs[senderIP] {
		result.Score += 100
		result.Details = append(result.Details, fmt.Sprintf("IP blacklisted: %s", senderIP))
		return
	}

	// Vérifier si c'est une IP privée (souvent légitime)
	ip := net.ParseIP(senderIP)
	if ip != nil && (ip.IsPrivate() || ip.IsLoopback()) {
		result.Score -= 10
		result.Details = append(result.Details, "Private/local IP (trusted)")
	}
}

// checkSenderDomain vérifie le domaine de l'expéditeur
func (sf *SpamFilter) checkSenderDomain(from string, result *SpamResult) {
	if from == "" {
		result.Score += 30
		result.Details = append(result.Details, "Missing sender address")
		return
	}

	parts := strings.Split(from, "@")
	if len(parts) != 2 {
		result.Score += 40
		result.Details = append(result.Details, "Invalid sender address format")
		return
	}

	domain := strings.ToLower(parts[1])
	if sf.blacklistedDomains[domain] {
		result.Score += 80
		result.Details = append(result.Details, fmt.Sprintf("Domain blacklisted: %s", domain))
	}

	// Vérifier les domaines suspects
	suspiciousDomains := []string{
		".tk", ".ml", ".ga", ".cf", // Domaines gratuits souvent utilisés pour le spam
	}
	for _, suspicious := range suspiciousDomains {
		if strings.HasSuffix(domain, suspicious) {
			result.Score += 20
			result.Details = append(result.Details, fmt.Sprintf("Suspicious domain TLD: %s", suspicious))
		}
	}
}

// checkSpamKeywords vérifie les mots-clés spam
func (sf *SpamFilter) checkSpamKeywords(email *Email, result *SpamResult) {
	content := strings.ToLower(email.Subject + " " + email.BodyTXT + " " + email.BodyHTML)

	keywordCount := 0
	foundKeywords := []string{}

	for _, keyword := range sf.spamKeywords {
		if strings.Contains(content, strings.ToLower(keyword)) {
			keywordCount++
			foundKeywords = append(foundKeywords, keyword)
			if keywordCount >= 5 { // Limite pour éviter une liste trop longue
				break
			}
		}
	}

	if keywordCount > 0 {
		score := keywordCount * 15
		result.Score += score
		result.Details = append(result.Details, fmt.Sprintf("Spam keywords found (%d): %s", keywordCount, strings.Join(foundKeywords, ", ")))
	}
}

// checkEmailStructure vérifie la structure de l'email
func (sf *SpamFilter) checkEmailStructure(email *Email, result *SpamResult) {
	// Vérifier le sujet
	if email.Subject == "" {
		result.Score += 20
		result.Details = append(result.Details, "Missing subject")
	} else {
		// Sujet tout en majuscules
		if email.Subject == strings.ToUpper(email.Subject) && len(email.Subject) > 10 {
			result.Score += 25
			result.Details = append(result.Details, "Subject in ALL CAPS")
		}

		// Trop d'exclamations
		exclamationCount := strings.Count(email.Subject, "!")
		if exclamationCount > 3 {
			result.Score += exclamationCount * 5
			result.Details = append(result.Details, fmt.Sprintf("Excessive exclamations in subject: %d", exclamationCount))
		}
	}

	// Vérifier le contenu
	if email.BodyTXT == "" && email.BodyHTML == "" {
		result.Score += 30
		result.Details = append(result.Details, "Empty email body")
	}

	// Vérifier les URLs suspectes
	sf.checkSuspiciousURLs(email, result)
}

// checkSuspiciousURLs vérifie les URLs suspectes
func (sf *SpamFilter) checkSuspiciousURLs(email *Email, result *SpamResult) {
	content := email.BodyTXT + " " + email.BodyHTML

	// Regex pour détecter les URLs
	urlRegex := regexp.MustCompile(`https?://[^\s<>"]+`)
	urls := urlRegex.FindAllString(content, -1)

	suspiciousPatterns := []string{
		"bit.ly", "tinyurl.com", "t.co", // URL shorteners
		"click", "redirect", "track", // Mots suspects dans les URLs
	}

	for _, url := range urls {
		for _, pattern := range suspiciousPatterns {
			if strings.Contains(strings.ToLower(url), pattern) {
				result.Score += 15
				result.Details = append(result.Details, fmt.Sprintf("Suspicious URL: %s", url))
				break
			}
		}
	}

	// Trop d'URLs
	if len(urls) > 10 {
		result.Score += 20
		result.Details = append(result.Details, fmt.Sprintf("Too many URLs: %d", len(urls)))
	}
}

// checkRateLimit vérifie le rate limiting
func (sf *SpamFilter) checkRateLimit(senderIP string, result *SpamResult) {
	now := time.Now()

	if limit, exists := sf.rateLimiter[senderIP]; exists {
		// Reset si la fenêtre est expirée
		if now.Sub(limit.lastReset) > limit.window {
			limit.count = 0
			limit.lastReset = now
		}

		limit.count++

		if limit.count > limit.maxEmails {
			result.Score += 60
			result.Details = append(result.Details, fmt.Sprintf("Rate limit exceeded: %d emails", limit.count))
		}
	} else {
		// Créer une nouvelle entrée de rate limiting
		sf.rateLimiter[senderIP] = &RateLimit{
			count:     1,
			lastReset: now,
			maxEmails: 50,        // 50 emails max
			window:    time.Hour, // par heure
		}
	}
}

// checkDNSBL vérifie les DNS Blacklists
func (sf *SpamFilter) checkDNSBL(senderIP string, result *SpamResult) {
	ip := net.ParseIP(senderIP)
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() {
		return // Skip DNSBL pour les IPs privées
	}

	// Inverser l'IP pour la requête DNSBL
	reversedIP := reverseIP(senderIP)
	if reversedIP == "" {
		return
	}

	for _, dnsbl := range sf.dnsblServers {
		query := reversedIP + "." + dnsbl

		// Timeout court pour éviter de bloquer
		go func(query, dnsbl string) {
			_, err := net.LookupHost(query)
			if err == nil {
				result.Score += 40
				result.Details = append(result.Details, fmt.Sprintf("Listed in DNSBL: %s", dnsbl))
			}
		}(query, dnsbl)
	}
}

// checkSuspiciousHeaders vérifie les en-têtes suspects
func (sf *SpamFilter) checkSuspiciousHeaders(email *Email, result *SpamResult) {
	headers := strings.ToLower(email.Headers)

	// Vérifier les en-têtes manquants ou suspects
	if !strings.Contains(headers, "message-id:") {
		result.Score += 15
		result.Details = append(result.Details, "Missing Message-ID header")
	}

	if !strings.Contains(headers, "date:") {
		result.Score += 10
		result.Details = append(result.Details, "Missing Date header")
	}

	// Vérifier les en-têtes de spam connus
	spamHeaders := []string{
		"x-spam", "x-advertisement", "x-bulk", "precedence: bulk",
	}

	for _, spamHeader := range spamHeaders {
		if strings.Contains(headers, spamHeader) {
			result.Score += 25
			result.Details = append(result.Details, fmt.Sprintf("Spam header found: %s", spamHeader))
		}
	}
}

// determineAction détermine l'action finale basée sur le score
func (sf *SpamFilter) determineAction(result *SpamResult) {
	if result.Score >= 100 {
		result.IsSpam = true
		result.Action = "REJECT"
		result.Reason = "High spam score - email rejected"
	} else if result.Score >= 50 {
		result.IsSpam = true
		result.Action = "QUARANTINE"
		result.Reason = "Medium spam score - email quarantined"
	} else {
		result.IsSpam = false
		result.Action = "ACCEPT"
		result.Reason = "Low spam score - email accepted"
	}
}

// reverseIP inverse une adresse IP pour les requêtes DNSBL
func reverseIP(ip string) string {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return ""
	}

	// Inverser l'ordre des octets
	return parts[3] + "." + parts[2] + "." + parts[1] + "." + parts[0]
}

// AddBlacklistedIP ajoute une IP à la blacklist
func (sf *SpamFilter) AddBlacklistedIP(ip string) {
	sf.blacklistedIPs[ip] = true
}

// AddBlacklistedDomain ajoute un domaine à la blacklist
func (sf *SpamFilter) AddBlacklistedDomain(domain string) {
	sf.blacklistedDomains[strings.ToLower(domain)] = true
}

// RemoveBlacklistedIP retire une IP de la blacklist
func (sf *SpamFilter) RemoveBlacklistedIP(ip string) {
	delete(sf.blacklistedIPs, ip)
}

// RemoveBlacklistedDomain retire un domaine de la blacklist
func (sf *SpamFilter) RemoveBlacklistedDomain(domain string) {
	delete(sf.blacklistedDomains, strings.ToLower(domain))
}

// SetEnabled active/désactive le filtrage spam
func (sf *SpamFilter) SetEnabled(enabled bool) {
	sf.enabled = enabled
}

// GetStats retourne les statistiques du filtre
func (sf *SpamFilter) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"enabled":             sf.enabled,
		"blacklisted_ips":     len(sf.blacklistedIPs),
		"blacklisted_domains": len(sf.blacklistedDomains),
		"spam_keywords":       len(sf.spamKeywords),
		"rate_limited_ips":    len(sf.rateLimiter),
		"max_email_size":      sf.maxEmailSize,
	}
}
