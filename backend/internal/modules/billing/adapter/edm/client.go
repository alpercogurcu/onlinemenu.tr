// Package edm provides a SOAP client for the EDM Bilisim e-invoice platform.
// Ported from onlinemenu.b2b with multi-tenant session management via Redis.
package edm

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// client is the internal SOAP client.  Sessions are cached in Redis by the session manager.
type client struct {
	httpClient *http.Client
	endpoint   string
}

func newClient(endpoint string) *client {
	return &client{
		endpoint:   endpoint,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// call sends a SOAP request and returns the raw inner Body XML.
func (c *client) call(soapAction, bodyXML string) ([]byte, error) {
	payload := soapEnvelope(bodyXML)

	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader([]byte(payload)))
	if err != nil {
		return nil, fmt.Errorf("%w: new request: %v", ErrEDMConnection, err)
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", soapAction)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEDMConnection, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrEDMConnection, err)
	}

	if resp.StatusCode != http.StatusOK || bytes.Contains(body, []byte("<s:Fault>")) || bytes.Contains(body, []byte("<Fault>")) {
		return nil, parseFault(body)
	}

	return extractBodyContent(body), nil
}

// soapEnvelope wraps inner XML in a SOAP 1.1 envelope.
func soapEnvelope(bodyXML string) string {
	return `<?xml version="1.0" encoding="utf-8"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">` +
		`<s:Body xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">` +
		bodyXML +
		`</s:Body></s:Envelope>`
}

// requestHeader builds the common REQUEST_HEADER block required by every EDM operation.
func requestHeader(sessionID string) string {
	return fmt.Sprintf(`<REQUEST_HEADER xmlns=""><SESSION_ID>%s</SESSION_ID>`+
		`<CLIENT_TXN_ID>%s</CLIENT_TXN_ID>`+
		`<ACTION_DATE>%s</ACTION_DATE>`+
		`<REASON>E-Fatura Entegrasyonu</REASON>`+
		`<APPLICATION_NAME>ONLINEMENU</APPLICATION_NAME>`+
		`<HOSTNAME>API-FINANCE</HOSTNAME>`+
		`<CHANNEL_NAME>ERP</CHANNEL_NAME>`+
		`<COMPRESSED>N</COMPRESSED></REQUEST_HEADER>`,
		escapeXML(sessionID),
		uuid.New().String(),
		time.Now().Format("2006-01-02T15:04:05.0000000-07:00"),
	)
}

// parseFault extracts error information from a SOAP Fault response.
func parseFault(body []byte) error {
	s := string(body)

	if strings.Contains(s, "SESSION_ID") && strings.Contains(s, "expire") {
		return ErrSessionExpired
	}
	if strings.Contains(s, "Oturum bulunamad") || strings.Contains(s, "Session bulunamadi") || strings.Contains(s, "10011") {
		return ErrSessionExpired
	}
	if strings.Contains(s, "not authenticated") || strings.Contains(s, "InvalidSecurity") {
		return ErrSessionExpired
	}

	if code := extractXMLValue(s, "ERROR_CODE"); code != "" {
		desc := extractXMLValue(s, "ERROR_LONG_DES")
		if desc == "" {
			desc = extractXMLValue(s, "ERROR_SHORT_DES")
		}
		return fmt.Errorf("%w: [%s] %s", ErrSOAPFault, code, desc)
	}
	if fault := extractXMLValue(s, "faultstring"); fault != "" {
		return fmt.Errorf("%w: %s", ErrSOAPFault, fault)
	}
	end := len(body)
	if end > 500 {
		end = 500
	}
	return fmt.Errorf("%w: HTTP error: %s", ErrEDMConnection, body[:end])
}

// extractBodyContent returns the content between SOAP Body tags.
func extractBodyContent(body []byte) []byte {
	s := string(body)
	bodyStart := strings.Index(s, "<s:Body")
	if bodyStart == -1 {
		bodyStart = strings.Index(s, "<Body")
	}
	if bodyStart == -1 {
		return body
	}
	tagEnd := strings.Index(s[bodyStart:], ">")
	if tagEnd == -1 {
		return body
	}
	contentStart := bodyStart + tagEnd + 1

	bodyEnd := strings.Index(s, "</s:Body>")
	if bodyEnd == -1 {
		bodyEnd = strings.Index(s, "</Body>")
	}
	if bodyEnd == -1 {
		return body
	}
	return []byte(s[contentStart:bodyEnd])
}

// extractXMLValue extracts the text content of the first occurrence of tagName.
func extractXMLValue(xmlStr, tagName string) string {
	start := strings.Index(xmlStr, "<"+tagName)
	if start == -1 {
		return ""
	}
	tagEnd := strings.Index(xmlStr[start:], ">")
	if tagEnd == -1 {
		return ""
	}
	contentStart := start + tagEnd + 1
	end := strings.Index(xmlStr[contentStart:], "</"+tagName+">")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(xmlStr[contentStart : contentStart+end])
}

// escapeXML escapes special XML characters in attribute and text values.
func escapeXML(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s)) //nolint:errcheck
	return b.String()
}
