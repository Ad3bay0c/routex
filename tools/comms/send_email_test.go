package comms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func mustUnmarshal(t *testing.T, data json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func TestSendGrid_SendsEmail(t *testing.T) {
	var capturedBody map[string]any
	var capturedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("X-Message-Id", "msg-abc-123")
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	provider := &sendGridProvider{
		client: srv.Client(),
		url:    srv.URL,
		apiKey: "test-api-key",
	}

	tool := &SendEmailTool{
		provider:  provider,
		fromEmail: "agent@example.com",
		fromName:  "Test Agent",
	}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"to":      "recipient@example.com",
		"subject": "Test Subject",
		"body":    "Hello from Routex",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out sendEmailOutput
	mustUnmarshal(t, result, &out)

	if !out.Success {
		t.Error("Success = false, want true")
	}
	if out.MessageID != "msg-abc-123" {
		t.Errorf("MessageID = %q, want %q", out.MessageID, "msg-abc-123")
	}
	if out.To != "recipient@example.com" {
		t.Errorf("To = %q, want %q", out.To, "recipient@example.com")
	}
	if out.Provider != "sendgrid" {
		t.Errorf("Provider = %q, want %q", out.Provider, "sendgrid")
	}

	// Verify auth header
	if !strings.HasPrefix(capturedAuth, "Bearer ") {
		t.Errorf("Authorization = %q, should start with Bearer", capturedAuth)
	}

	// Verify from address in request body
	fromField := capturedBody["from"].(map[string]any)
	if fromField["email"] != "agent@example.com" {
		t.Errorf("from.email = %v, want agent@example.com", fromField["email"])
	}
	if fromField["name"] != "Test Agent" {
		t.Errorf("from.name = %v, want Test Agent", fromField["name"])
	}
}

func TestSendGrid_HTMLEmail(t *testing.T) {
	var capturedContent []any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		capturedContent = body["content"].([]any)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	provider := &sendGridProvider{client: srv.Client(), url: srv.URL}
	tool := &SendEmailTool{provider: provider, fromEmail: "a@b.com"}

	tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"to":      "r@example.com",
		"subject": "HTML Test",
		"body":    "<h1>Hello</h1>",
		"is_html": true,
	}))

	if len(capturedContent) == 0 {
		t.Fatal("no content in request")
	}
	contentBlock := capturedContent[0].(map[string]any)
	if contentBlock["type"] != "text/html" {
		t.Errorf("content type = %q, want text/html", contentBlock["type"])
	}
}

func TestSendGrid_InvalidAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	provider := &sendGridProvider{client: srv.Client(), url: srv.URL}
	tool := &SendEmailTool{provider: provider, fromEmail: "a@b.com"}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"to": "r@example.com", "subject": "Test", "body": "hello",
	}))
	if err == nil {
		t.Error("should error for 401")
	}
}

func TestResend_SendsEmail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Resend auth
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("missing Bearer auth")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		// Resend expects from as "Name <email>" format when name is set
		from := body["from"].(string)
		if !strings.Contains(from, "agent@example.com") {
			t.Errorf("from = %q, should contain email", from)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"id": "resend-msg-456"})
	}))
	t.Cleanup(srv.Close)

	provider := &resendProvider{client: srv.Client(), url: srv.URL, apiKey: "test-api-key"}
	tool := &SendEmailTool{
		provider:  provider,
		fromEmail: "agent@example.com",
		fromName:  "Test Agent",
	}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"to":      "recipient@example.com",
		"subject": "Resend Test",
		"body":    "Hello via Resend",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out sendEmailOutput
	mustUnmarshal(t, result, &out)

	if !out.Success {
		t.Error("Success = false, want true")
	}
	if out.MessageID != "resend-msg-456" {
		t.Errorf("MessageID = %q, want %q", out.MessageID, "resend-msg-456")
	}
	if out.Provider != "resend" {
		t.Errorf("Provider = %q, want %q", out.Provider, "resend")
	}
}

func TestResend_HTMLEmail(t *testing.T) {
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"id": "x"})
	}))
	t.Cleanup(srv.Close)

	provider := &resendProvider{client: srv.Client(), url: srv.URL}
	tool := &SendEmailTool{provider: provider, fromEmail: "a@b.com"}

	tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"to": "r@x.com", "subject": "Test", "body": "<b>bold</b>", "is_html": true,
	}))

	// Resend uses "html" key for HTML emails
	if capturedBody["html"] == nil {
		t.Error("html field should be set for HTML emails in Resend")
	}
	if capturedBody["text"] != nil {
		t.Error("text field should not be set for HTML emails in Resend")
	}
}

func TestResend_InvalidAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	provider := &resendProvider{client: srv.Client(), url: srv.URL}
	tool := &SendEmailTool{provider: provider, fromEmail: "a@b.com"}

	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"to": "r@x.com", "subject": "Test", "body": "hello",
	}))
	if err == nil {
		t.Error("should error for 401")
	}
}

func TestNewSendEmailTool_SendGrid(t *testing.T) {
	tool, err := NewSendEmailTool("sendgrid", "sg-key", "agent@example.com", "Agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tool.provider.name() != "sendgrid" {
		t.Errorf("provider = %q, want sendgrid", tool.provider.name())
	}
}

func TestNewSendEmailTool_Resend(t *testing.T) {
	tool, err := NewSendEmailTool("resend", "re-key", "agent@example.com", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tool.provider.name() != "resend" {
		t.Errorf("provider = %q, want resend", tool.provider.name())
	}
}

func TestNewSendEmailTool_DefaultsToSendGrid(t *testing.T) {
	tool, err := NewSendEmailTool("", "key", "agent@example.com", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tool.provider.name() != "sendgrid" {
		t.Errorf("empty provider should default to sendgrid, got %q", tool.provider.name())
	}
}

func TestNewSendEmailTool_UnknownProvider(t *testing.T) {
	_, err := NewSendEmailTool("mailchimp", "key", "agent@example.com", "")
	if err == nil {
		t.Fatal("should error for unknown provider")
	}
	if !strings.Contains(err.Error(), "valid values: sendgrid, resend") {
		t.Fatalf("should error for unknown provider, got %v", err)
	}
}

func TestNewSendEmailTool_MissingFromEmail(t *testing.T) {
	_, err := NewSendEmailTool("sendgrid", "key", "", "")
	if err == nil {
		t.Fatal("should error when from_email is missing")
	}
	if err.Error() != "send_email: from_email is required" {
		t.Fatalf("should error when from_email is missing, got %v", err)
	}
}

func TestSendEmail_MissingTo(t *testing.T) {
	tool, _ := NewSendEmailTool("sendgrid", "key", "a@b.com", "")
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"subject": "Test", "body": "hello",
	}))
	if err == nil {
		t.Fatal("should error when to is missing")
	}
	if err.Error() != "send_email: to is required" {
		t.Errorf("error = %v, want %v", err, "send_email: to is required")
	}
}

func TestSendEmail_MissingSubject(t *testing.T) {
	tool, _ := NewSendEmailTool("sendgrid", "key", "a@b.com", "")
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"to": "r@x.com", "body": "hello",
	}))
	if err == nil {
		t.Fatal("should error when subject is missing")
	}
	if err.Error() != "send_email: subject is required" {
		t.Errorf("error = %v, want %v", err, "send_email: subject is required")
	}
}

func TestSendEmail_MissingBody(t *testing.T) {
	tool, _ := NewSendEmailTool("sendgrid", "key", "a@b.com", "")
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"to": "r@x.com", "subject": "Test",
	}))
	if err == nil {
		t.Fatal("should error when body is missing")
	}

	if err.Error() != "send_email: body is required" {
		t.Errorf("error = %v, want %v", err, "send_email: body is required")
	}
}

func TestSendEmail_NameAndSchema(t *testing.T) {
	tool, _ := NewSendEmailTool("sendgrid", "key", "a@b.com", "")
	if tool.Name() != "send_email" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "send_email")
	}
	schema := tool.Schema()
	if schema.Description == "" {
		t.Error("Schema.Description should not be empty")
	}
	for _, p := range []string{"to", "subject", "body"} {
		if param, ok := schema.Parameters[p]; !ok {
			t.Errorf("schema missing parameter %q", p)
		} else if !param.Required {
			t.Errorf("parameter %q should be Required=true", p)
		}
	}
}
