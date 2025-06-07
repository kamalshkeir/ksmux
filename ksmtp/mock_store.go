package ksmtp

import (
	"fmt"
	"time"
)

// MockEmailStore is a simple in-memory implementation of EmailStore for testing
type MockEmailStore struct {
	emails map[string][]*StoredEmail // username -> emails
}

// NewMockEmailStore creates a new mock email store
func NewMockEmailStore() *MockEmailStore {
	store := &MockEmailStore{
		emails: make(map[string][]*StoredEmail),
	}

	// Add some test emails
	store.addTestEmails()

	return store
}

// addTestEmails adds some sample emails for testing
func (m *MockEmailStore) addTestEmails() {
	// Test emails for admin user
	adminEmails := []*StoredEmail{
		{
			ID:      "email_001",
			From:    "test@example.com",
			To:      "admin@kamalshkeir.dev",
			Subject: "Test Email 1",
			Body:    "This is a test email body.\nSecond line of the email.",
			Date:    time.Now().Format(time.RFC1123Z),
			Size:    150,
			Headers: "From: test@example.com\r\nTo: admin@kamalshkeir.dev\r\nSubject: Test Email 1\r\nDate: " + time.Now().Format(time.RFC1123Z) + "\r\n\r\n",
		},
		{
			ID:      "email_002",
			From:    "newsletter@company.com",
			To:      "admin@kamalshkeir.dev",
			Subject: "Weekly Newsletter",
			Body:    "Welcome to our weekly newsletter!\n\nThis week's highlights:\n- Feature updates\n- Bug fixes\n- New documentation",
			Date:    time.Now().Add(-24 * time.Hour).Format(time.RFC1123Z),
			Size:    250,
			Headers: "From: newsletter@company.com\r\nTo: admin@kamalshkeir.dev\r\nSubject: Weekly Newsletter\r\nDate: " + time.Now().Add(-24*time.Hour).Format(time.RFC1123Z) + "\r\n\r\n",
		},
		{
			ID:      "email_003",
			From:    "support@service.com",
			To:      "admin@kamalshkeir.dev",
			Subject: "Support Ticket #12345",
			Body:    "Your support ticket has been resolved.\n\nTicket Details:\nIssue: Login problems\nStatus: Resolved\nResolution: Password reset completed",
			Date:    time.Now().Add(-2 * time.Hour).Format(time.RFC1123Z),
			Size:    200,
			Headers: "From: support@service.com\r\nTo: admin@kamalshkeir.dev\r\nSubject: Support Ticket #12345\r\nDate: " + time.Now().Add(-2*time.Hour).Format(time.RFC1123Z) + "\r\n\r\n",
		},
	}

	// Test emails for contact user
	contactEmails := []*StoredEmail{
		{
			ID:      "email_004",
			From:    "client@business.com",
			To:      "contact@kamalshkeir.dev",
			Subject: "Business Inquiry",
			Body:    "Hello,\n\nI'm interested in your services. Could you please send me more information?\n\nBest regards,\nJohn Doe",
			Date:    time.Now().Add(-3 * time.Hour).Format(time.RFC1123Z),
			Size:    180,
			Headers: "From: client@business.com\r\nTo: contact@kamalshkeir.dev\r\nSubject: Business Inquiry\r\nDate: " + time.Now().Add(-3*time.Hour).Format(time.RFC1123Z) + "\r\n\r\n",
		},
	}

	m.emails["admin"] = adminEmails
	m.emails["contact"] = contactEmails
}

// GetEmails returns all emails for a user
func (m *MockEmailStore) GetEmails(username string) ([]*StoredEmail, error) {
	emails, exists := m.emails[username]
	if !exists {
		return []*StoredEmail{}, nil // Return empty slice if user has no emails
	}

	// Filter out deleted emails
	var activeEmails []*StoredEmail
	for _, email := range emails {
		if !email.Deleted {
			activeEmails = append(activeEmails, email)
		}
	}

	return activeEmails, nil
}

// GetEmailByID returns a specific email by ID
func (m *MockEmailStore) GetEmailByID(username, emailID string) (*StoredEmail, error) {
	emails, exists := m.emails[username]
	if !exists {
		return nil, fmt.Errorf("user not found")
	}

	for _, email := range emails {
		if email.ID == emailID && !email.Deleted {
			return email, nil
		}
	}

	return nil, fmt.Errorf("email not found")
}

// DeleteEmail marks an email as deleted
func (m *MockEmailStore) DeleteEmail(username, emailID string) error {
	emails, exists := m.emails[username]
	if !exists {
		return fmt.Errorf("user not found")
	}

	for _, email := range emails {
		if email.ID == emailID {
			email.Deleted = true
			fmt.Printf("📧 [MockStore] Email %s deleted for user %s\n", emailID, username)
			return nil
		}
	}

	return fmt.Errorf("email not found")
}

// MarkAsRead marks an email as read
func (m *MockEmailStore) MarkAsRead(username, emailID string) error {
	emails, exists := m.emails[username]
	if !exists {
		return fmt.Errorf("user not found")
	}

	for _, email := range emails {
		if email.ID == emailID {
			email.Read = true
			fmt.Printf("📧 [MockStore] Email %s marked as read for user %s\n", emailID, username)
			return nil
		}
	}

	return fmt.Errorf("email not found")
}

// AddEmail adds a new email to the store (useful for testing)
func (m *MockEmailStore) AddEmail(username string, email *StoredEmail) {
	if m.emails[username] == nil {
		m.emails[username] = []*StoredEmail{}
	}

	// Calculate size if not set
	if email.Size == 0 {
		email.Size = len(email.Headers) + len(email.Body)
	}

	m.emails[username] = append(m.emails[username], email)
	fmt.Printf("📧 [MockStore] Email %s added for user %s\n", email.ID, username)
}

// GetUserStats returns email statistics for a user
func (m *MockEmailStore) GetUserStats(username string) (total, unread int) {
	emails, exists := m.emails[username]
	if !exists {
		return 0, 0
	}

	for _, email := range emails {
		if !email.Deleted {
			total++
			if !email.Read {
				unread++
			}
		}
	}

	return total, unread
}
